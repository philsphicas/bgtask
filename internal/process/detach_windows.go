//go:build windows

package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const stateDirOverrideEnv = "BGTASK_STATE_DIR"

// Detach starts a detached child process that survives the parent's exit.
// The returned bool indicates whether breakaway from the parent job failed;
// when true the supervisor may not survive SSH session exit.
//
// On Windows, SSH servers (OpenSSH) place child processes in a Job Object.
// When the session closes, all processes in the job are terminated.
// CREATE_BREAKAWAY_FROM_JOB allows the supervisor to escape this job,
// surviving SSH disconnects. If the parent job doesn't allow breakaway,
// we fall back to launching without it.
func Detach(args []string) (*os.Process, bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, false, fmt.Errorf("resolve executable: %w", err)
	}

	baseFlags := uint32(syscall.CREATE_NEW_PROCESS_GROUP) | windows.DETACHED_PROCESS

	cmd := newDetachedCmd(exe, args, baseFlags|windows.CREATE_BREAKAWAY_FROM_JOB)
	if err := cmd.Start(); err != nil {
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, false, fmt.Errorf("start detached: %w", err)
		}
		// Breakaway denied — retry without it. The supervisor may not
		// survive session exit, but it will at least start.
		cmd = newDetachedCmd(exe, args, baseFlags)
		if err := cmd.Start(); err != nil {
			return nil, false, fmt.Errorf("start detached (fallback): %w", err)
		}
		pid := cmd.Process.Pid
		_ = cmd.Process.Release()
		return &os.Process{Pid: pid}, true, nil
	}

	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return &os.Process{Pid: pid}, false, nil
}

func newDetachedCmd(exe string, args []string, flags uint32) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd
}

// SignalRestart writes a restart command to the control file.
//
// Deprecated: this scans every task directory under the default procs
// directory looking for one whose supervisor.pid matches pid, which is
// wasteful when the caller already knows the task directory. Prefer
// SignalRestartDir.
func SignalRestart(pid int) error {
	return writeCtlFile(pid, "restart")
}

// SignalStop writes a stop command to the control file.
//
// Deprecated: see SignalRestart; prefer SignalStopDir.
func SignalStop(pid int) error {
	return writeCtlFile(pid, "stop")
}

// SignalRestartDir writes a restart command directly to taskDir/ctl. Unlike
// SignalRestart, this does not need to resolve the default config directory
// or scan every task's supervisor.pid to find a match. pid is accepted for
// API symmetry with SignalRestart and other platforms but is unused here:
// the control file is targeted by directory, not by PID.
func SignalRestartDir(taskDir string, _ int) error {
	return writeCtlFileDir(taskDir, "restart")
}

// SignalStopDir writes a stop command directly to taskDir/ctl. Unlike
// SignalStop, this does not need to resolve the default config directory or
// scan every task's supervisor.pid to find a match. pid is accepted for API
// symmetry; see SignalRestartDir.
func SignalStopDir(taskDir string, _ int) error {
	return writeCtlFileDir(taskDir, "stop")
}

// writeCtlFileDir writes action to taskDir/ctl using an atomic replace so a
// concurrent reader never observes a partial or missing file.
func writeCtlFileDir(taskDir, action string) error {
	return AtomicReplace(filepath.Join(taskDir, "ctl"), []byte(action), 0o600)
}

func writeCtlFile(pid int, action string) error {
	// Resolve the procs directory using the same logic as state.configDir().
	// We can't import internal/state (would create a cycle), so we duplicate
	// the directory resolution here.
	var procsDir string
	if override := os.Getenv(stateDirOverrideEnv); override != "" {
		procsDir = filepath.Join(override, "procs")
	} else if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		procsDir = filepath.Join(xdg, "bgtask", "procs")
	} else {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		procsDir = filepath.Join(appData, "bgtask", "procs")
	}

	entries, err := os.ReadDir(procsDir)
	if err != nil {
		return fmt.Errorf("read procs dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pidFile := filepath.Join(procsDir, e.Name(), "supervisor.pid")
		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == fmt.Sprintf("%d", pid) {
			return writeCtlFileDir(filepath.Join(procsDir, e.Name()), action)
		}
	}
	return fmt.Errorf("no task found with supervisor PID %d", pid)
}
