// Package supervisor implements the background supervisor shim that manages
// child process lifecycle, captures interleaved stdout/stderr to JSONL, and
// handles restart/pause/resume signals.
package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/philsphicas/bgtask/internal/process"
	"github.com/philsphicas/bgtask/internal/state"
)

// LogEntry is a single line in the JSONL output log.
type LogEntry struct {
	Time    time.Time `json:"t"`
	Stream  string    `json:"s"` // "o"=stdout, "e"=stderr, "x"=lifecycle
	Data    string    `json:"d"` // output text or event description
	Code    *int      `json:"code,omitempty"`
	Attempt *int      `json:"attempt,omitempty"`
	Delay   string    `json:"delay,omitempty"`
	Message string    `json:"message,omitempty"` // health check output, error details
}

// Config holds the supervisor configuration, read from meta.json.
type Config struct {
	StateDir       string
	Meta           *state.Meta
	Store          *state.Store
	Restart        string
	RestartDelay   time.Duration
	HealthCheck    string
	HealthInterval time.Duration
}

// Run is the main supervisor loop. It is called by the hidden supervisor
// subcommand after re-exec.
func Run(cfg *Config) error {
	// Write supervisor PID and start time (for PID reuse protection).
	pid := os.Getpid()
	if err := cfg.Store.WritePID(cfg.Meta.ID, "supervisor.pid", pid); err != nil {
		return fmt.Errorf("write supervisor pid: %w", err)
	}
	createTime := process.CreateTime(pid)
	if createTime > 0 {
		if err := cfg.Store.WriteCreateTime(cfg.Meta.ID, createTime); err != nil {
			return fmt.Errorf("write create time: %w", err)
		}
	}

	// Open the JSONL log file.
	logFile, err := os.OpenFile(cfg.Store.OutputPath(cfg.Meta.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	// logFile is managed explicitly: closed during rotation and on exit.
	// Do not defer logFile.Close() -- rotation reassigns logFile, so the
	// deferred close would act on a stale descriptor.

	logger := &jsonlWriter{f: logFile}

	// Set up signal handling.
	var paused atomic.Bool
	var stopRequested atomic.Bool

	sigCh := make(chan os.Signal, 16)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	if runtime.GOOS != "windows" {
		// SIGUSR1 = pause, SIGUSR2 = resume.
		// Can't use syscall.SIGUSR1/2 directly -- not defined on Windows.
		signal.Notify(sigCh, sigUSR1, sigUSR2)
	}

	// done is closed when Run returns, so background goroutines exit cleanly.
	done := make(chan struct{})
	defer func() {
		signal.Stop(sigCh)
		close(done)
	}()

	// Channel to signal the child should be killed for pause.
	pauseCh := make(chan struct{}, 1)
	resumeCh := make(chan struct{}, 1)

	// Signal handler goroutine. Non-blocking sends to avoid deadlock.
	go func() {
		for {
			select {
			case <-done:
				return
			case sig, ok := <-sigCh:
				if !ok {
					return
				}
				switch sig {
				case syscall.SIGTERM, syscall.SIGINT:
					stopRequested.Store(true)
					select {
					case pauseCh <- struct{}{}:
					default:
					}
				default:
					sigNum, _ := sig.(syscall.Signal)
					switch sigNum {
					case sigUSR1: // pause
						paused.Store(true)
						logger.WriteEvent("paused", nil, nil, "")
						select {
						case pauseCh <- struct{}{}:
						default:
						}
					case sigUSR2: // resume
						if paused.Load() {
							paused.Store(false)
							logger.WriteEvent("resumed", nil, nil, "")
							select {
							case resumeCh <- struct{}{}:
							default:
							}
						}
					}
				}
			}
		}
	}()

	// On Windows, poll a control file for pause/resume since SIGUSR doesn't exist.
	if runtime.GOOS == "windows" {
		ctlPath := filepath.Join(cfg.StateDir, "ctl")
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
				}
				// Atomic read: rename ctl -> ctl.processing, then read + delete.
				procPath := ctlPath + ".processing"
				if err := os.Rename(ctlPath, procPath); err != nil {
					continue
				}
				data, err := os.ReadFile(procPath) //nolint:gosec // path is constructed internally
				_ = os.Remove(procPath)
				if err != nil {
					continue
				}
				action := strings.TrimSpace(string(data))
				switch action {
				case "pause":
					paused.Store(true)
					logger.WriteEvent("paused", nil, nil, "")
					select {
					case pauseCh <- struct{}{}:
					default:
					}
				case "resume":
					if paused.Load() {
						paused.Store(false)
						logger.WriteEvent("resumed", nil, nil, "")
						select {
						case resumeCh <- struct{}{}:
						default:
						}
					}
				}
			}
		}()
	}

	// Health check goroutine.
	if cfg.HealthCheck != "" {
		go func() {
			interval := cfg.HealthInterval
			if interval <= 0 {
				interval = 30 * time.Second
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
				}
				if paused.Load() {
					continue
				}
				timeout := cfg.HealthInterval
				if timeout <= 0 {
					timeout = 30 * time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				var healthCmd *exec.Cmd
				if runtime.GOOS == "windows" {
					healthCmd = exec.CommandContext(ctx, "cmd", "/c", cfg.HealthCheck)
				} else {
					healthCmd = exec.CommandContext(ctx, "sh", "-c", cfg.HealthCheck)
				}
				out, err := healthCmd.CombinedOutput()
				cancel()
				if err != nil {
					msg := strings.TrimSpace(string(out))
					if msg == "" {
						msg = err.Error()
					}
					logger.WriteHealthEvent("health_fail", msg)
				} else {
					logger.WriteHealthEvent("health_ok", "")
				}
			}
		}()
	}

	// closeLogFile is a helper that ensures the current logFile is closed
	// exactly once before returning from Run.
	closeLogFile := func() {
		logger.close(logFile)
	}

	attempt := 0
	for {
		attempt++
		exitCode, err := runChild(cfg, logger, pauseCh, &stopRequested)

		if stopRequested.Load() {
			// Explicit stop -- write exit and terminate.
			sig := "SIGTERM"
			exit := &state.Exit{Code: exitCode, Signal: sig, ExitedAt: time.Now()}
			_ = cfg.Store.WriteExit(cfg.Meta.ID, exit)
			logger.WriteEvent("stopped", &exitCode, nil, "")
			closeLogFile()
			return nil
		}

		if paused.Load() {
			// Paused -- wait for resume or stop signal.
			logger.WriteEvent("child_exited", &exitCode, &attempt, "")
			select {
			case <-resumeCh:
				attempt = 0
				continue
			case <-pauseCh:
				// SIGTERM arrived while paused.
				if stopRequested.Load() {
					exit := &state.Exit{Code: exitCode, ExitedAt: time.Now()}
					_ = cfg.Store.WriteExit(cfg.Meta.ID, exit)
					logger.WriteEvent("stopped", &exitCode, nil, "")
					closeLogFile()
					return nil
				}
			}
			continue
		}

		if err != nil {
			logger.WriteHealthEvent("child_error", err.Error())
		}

		// Child exited on its own. Decide whether to restart.
		shouldRestart := cfg.Restart == "always" || (cfg.Restart == "on-failure" && exitCode != 0)
		if !shouldRestart {
			exit := &state.Exit{Code: exitCode, ExitedAt: time.Now()}
			_ = cfg.Store.WriteExit(cfg.Meta.ID, exit)
			logger.WriteEvent("exited", &exitCode, nil, "")
			if cfg.Meta.AutoRm {
				closeLogFile()
				_ = cfg.Store.Remove(cfg.Meta.ID)
			} else {
				closeLogFile()
			}
			return nil
		}

		// Restart with backoff.
		delay := cfg.computeDelay(attempt)
		delayStr := delay.String()
		logger.WriteEvent("restarting", &exitCode, &attempt, delayStr)

		// Rotate log if needed before restarting. On Windows, files can't be
		// renamed while open, so close first, rotate, then reopen.
		if shouldRotate, _ := cfg.Store.ShouldRotateLog(cfg.Meta.ID, state.MaxLogSize); shouldRotate {
			logger.close(logFile)
			_ = cfg.Store.RotateLog(cfg.Meta.ID)
			newLogFile, openErr := os.OpenFile(cfg.Store.OutputPath(cfg.Meta.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if openErr == nil {
				logger.setFile(newLogFile)
				logFile = newLogFile
			} else {
				// Rotation failed to reopen -- reopen the original path.
				newLogFile, _ = os.OpenFile(cfg.Store.OutputPath(cfg.Meta.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				if newLogFile != nil {
					logger.setFile(newLogFile)
					logFile = newLogFile
				}
			}
		}

		// Wait for delay or stop signal.
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			// Continue to restart.
		case <-pauseCh:
			timer.Stop()
			if stopRequested.Load() {
				exit := &state.Exit{Code: exitCode, ExitedAt: time.Now()}
				_ = cfg.Store.WriteExit(cfg.Meta.ID, exit)
				closeLogFile()
				return nil
			}
			if paused.Load() {
				// Wait for resume or stop while paused during restart delay.
				select {
				case <-resumeCh:
					attempt = 0
				case <-pauseCh:
					if stopRequested.Load() {
						exit := &state.Exit{Code: exitCode, ExitedAt: time.Now()}
						_ = cfg.Store.WriteExit(cfg.Meta.ID, exit)
						closeLogFile()
						return nil
					}
				}
			}
		}
	}
}

func (cfg *Config) computeDelay(attempt int) time.Duration {
	if cfg.RestartDelay > 0 {
		return cfg.RestartDelay
	}
	// Exponential backoff: 1s, 2s, 4s, 8s, ..., capped at 60s.
	// Cap shift to avoid overflow (time.Second << 6 = 64s).
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	d := time.Second << uint(shift)
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// runChild starts the child process, captures its output, and waits for it
// to exit or be killed via pauseCh.
func runChild(cfg *Config, logger *jsonlWriter, pauseCh <-chan struct{}, stopRequested *atomic.Bool) (int, error) {
	cmd := buildCmd(cfg.Meta)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start: %w", err)
	}

	// Write child PID.
	_ = cfg.Store.WritePID(cfg.Meta.ID, "child.pid", cmd.Process.Pid)

	// Capture output concurrently.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		captureStream(stdout, "o", logger)
	}()
	go func() {
		defer wg.Done()
		captureStream(stderr, "e", logger)
	}()

	// Monitor for pause/stop signal: kill the child if received.
	var killed atomic.Bool
	done := make(chan struct{})
	go func() {
		select {
		case <-pauseCh:
			killed.Store(true)
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		case <-done:
		}
	}()

	// Drain pipes BEFORE cmd.Wait(). Per Go docs, cmd.Wait() closes the
	// pipe read ends (closeAfterWait), so scanners must finish first.
	// The scanners get EOF once the child exits (or is killed), which
	// closes the write end of the pipe.
	wg.Wait()
	waitErr := cmd.Wait()
	close(done)

	if killed.Load() {
		return -1, nil
	}
	return exitCodeFrom(waitErr), nil
}

func buildCmd(meta *state.Meta) *exec.Cmd {
	if len(meta.Command) == 0 {
		// Should never happen: RunCmd validates this. Guard against
		// corrupted meta.json causing a panic.
		return exec.Command("false")
	}
	cmd := exec.Command(meta.Command[0], meta.Command[1:]...)
	cmd.Dir = meta.Cwd
	cmd.SysProcAttr = childSysProcAttr()

	// Apply environment overrides, replacing existing values.
	if len(meta.EnvOverrides) > 0 {
		env := os.Environ()
		// Build a map to deduplicate.
		envMap := make(map[string]string, len(env))
		for _, e := range env {
			if k, v, ok := strings.Cut(e, "="); ok {
				envMap[k] = v
			}
		}
		for k, v := range meta.EnvOverrides {
			envMap[k] = v
		}
		cmd.Env = make([]string, 0, len(envMap))
		for k, v := range envMap {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	return cmd
}

func captureStream(r io.Reader, stream string, logger *jsonlWriter) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		logger.WriteOutput(stream, scanner.Text()+"\n")
	}
}

func exitCodeFrom(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

// jsonlWriter writes log entries to a JSONL file, thread-safe.
type jsonlWriter struct {
	f  *os.File
	mu sync.Mutex
}

func (w *jsonlWriter) WriteOutput(stream, data string) {
	w.write(LogEntry{
		Time:   time.Now(),
		Stream: stream,
		Data:   data,
	})
}

func (w *jsonlWriter) WriteEvent(event string, code *int, attempt *int, delay string) {
	e := LogEntry{
		Time:    time.Now(),
		Stream:  "x",
		Data:    event,
		Code:    code,
		Attempt: attempt,
		Delay:   delay,
	}
	w.write(e)
}

func (w *jsonlWriter) WriteHealthEvent(event string, message string) {
	e := LogEntry{
		Time:    time.Now(),
		Stream:  "x",
		Data:    event,
		Message: message,
	}
	w.write(e)
}

func (w *jsonlWriter) close(f *os.File) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = f.Close()
	w.f = nil
}

func (w *jsonlWriter) setFile(f *os.File) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.f = f
}

func (w *jsonlWriter) write(entry LogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		_, _ = w.f.Write(append(data, '\n'))
	}
}
