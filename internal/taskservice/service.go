package taskservice

import (
	"context"
	"runtime"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
)

// Service is the taskservice front end: it composes a state.Store with a
// fakeable Launcher and ProcessController so business logic (locking,
// status resolution, process control) lives in one place regardless of
// which adapter (CLI, HTTP, MCP) drives it.
type Service struct {
	Store    *state.Store
	Launcher Launcher
	Process  ProcessController

	// StopTimeout is the default graceful-shutdown timeout used when a
	// request does not specify one (e.g. Remove, Run's replace-existing
	// path). Zero-value Services (as built directly by tests) fall back
	// to defaultStopTimeout at the call site.
	StopTimeout time.Duration

	// StartupCheckDelay is how long Run waits, *after* a launched
	// supervisor has been confirmed ready, before reading exit.json to
	// report an immediate exit (a bad command exits within a few
	// milliseconds of becoming ready). Zero disables the settle delay
	// entirely, which is useful in tests.
	StartupCheckDelay time.Duration

	// StartupReadyTimeout bounds how long Run and Start wait for a freshly
	// launched supervisor to signal readiness -- i.e. to write its own PID
	// to supervisor.pid, to write exit.json, or (for an --rm task) to
	// finish and remove its own state directory. Zero falls back to
	// defaultStartupReadyTimeout.
	StartupReadyTimeout time.Duration

	// StartupPollInterval is how often the readiness wait re-checks
	// on-disk launch evidence. Zero falls back to
	// defaultStartupPollInterval.
	StartupPollInterval time.Duration

	// LockWaitTimeout bounds nested lock acquisitions taken while the
	// store-wide lock is already held (Run's old-task lock, Run's new-task
	// lock, Rename's task lock), so a stuck per-task lock can never pin
	// the global lock indefinitely. Zero falls back to
	// defaultLockWaitTimeout.
	LockWaitTimeout time.Duration
}

// defaultStopTimeout is used whenever a caller-supplied timeout is <= 0.
const defaultStopTimeout = 10 * time.Second

// defaultStartupCheckDelay mirrors the CLI's historical brief pause after
// launching a supervisor, giving it time to write exit.json if the command
// is immediately broken (typo, missing binary).
const defaultStartupCheckDelay = 100 * time.Millisecond

// defaultStartupReadyTimeout bounds the post-launch readiness wait. It is
// generous relative to how long a supervisor takes to write supervisor.pid
// (single-digit milliseconds) so a heavily loaded machine does not produce
// spurious "not ready" failures.
const defaultStartupReadyTimeout = 10 * time.Second

// defaultStartupPollInterval is how often the readiness wait re-checks disk.
const defaultStartupPollInterval = 10 * time.Millisecond

// defaultLockWaitTimeout bounds nested lock acquisitions performed while
// the store-wide lock is held.
const defaultLockWaitTimeout = 5 * time.Second

// New builds a production Service around store, wired to the real OS
// process launcher and controller.
func New(store *state.Store) *Service {
	return &Service{
		Store:               store,
		Launcher:            processLauncher{},
		Process:             osProcessController{},
		StopTimeout:         defaultStopTimeout,
		StartupCheckDelay:   defaultStartupCheckDelay,
		StartupReadyTimeout: defaultStartupReadyTimeout,
		StartupPollInterval: defaultStartupPollInterval,
		LockWaitTimeout:     defaultLockWaitTimeout,
	}
}

// stopTimeout returns req's timeout, or the service default if unset.
func (s *Service) stopTimeout(requested time.Duration) time.Duration {
	if requested > 0 {
		return requested
	}
	if s.StopTimeout > 0 {
		return s.StopTimeout
	}
	return defaultStopTimeout
}

// startupReadyTimeout returns the configured readiness bound, or the default.
func (s *Service) startupReadyTimeout() time.Duration {
	if s.StartupReadyTimeout > 0 {
		return s.StartupReadyTimeout
	}
	return defaultStartupReadyTimeout
}

// startupPollInterval returns the configured readiness poll interval, or the
// default.
func (s *Service) startupPollInterval() time.Duration {
	if s.StartupPollInterval > 0 {
		return s.StartupPollInterval
	}
	return defaultStartupPollInterval
}

// lockWaitTimeout returns the configured nested-lock bound, or the default.
func (s *Service) lockWaitTimeout() time.Duration {
	if s.LockWaitTimeout > 0 {
		return s.LockWaitTimeout
	}
	return defaultLockWaitTimeout
}

// lockTaskBounded acquires a per-task lock with a bounded wait derived from
// ctx. It is used for every per-task acquisition taken while the store-wide
// lock is held, so contention on one task can never pin the global lock for
// longer than LockWaitTimeout.
func (s *Service) lockTaskBounded(ctx context.Context, id string) (*state.Lease, error) {
	wctx, cancel := context.WithTimeout(ctx, s.lockWaitTimeout())
	defer cancel()
	return s.Store.LockTaskContext(wctx, id)
}

// toTask builds the canonical Task DTO for id/meta, resolving its current
// runtime status and log path.
func (s *Service) toTask(id string, meta *state.Meta) Task {
	return Task{
		ID:      id,
		Meta:    meta,
		Status:  s.resolveStatus(id),
		LogPath: s.Store.OutputPath(id),
	}
}

// resolveStatus builds a state.TaskStatus for a task ID by consulting
// exit.json (exited), then the supervisor PID's liveness (running/dead),
// falling back to "unknown" if neither is determinable.
func (s *Service) resolveStatus(id string) state.TaskStatus {
	return s.resolveStatusWithPorts(id, true)
}

func (s *Service) resolveStatusWithPorts(id string, withPorts bool) state.TaskStatus {
	exit, _ := s.Store.ReadExit(id)
	if exit != nil {
		return state.TaskStatus{
			State: "exited",
			Exited: &state.ExitedInfo{
				Code:   exit.Code,
				Signal: exit.Signal,
				At:     exit.ExitedAt,
			},
		}
	}

	pid, alive := s.verifyAndGetPID(id)
	if pid > 0 {
		if alive {
			childPID, _ := s.Store.ReadPID(id, "child.pid")
			var ports []uint32
			if withPorts && childPID > 0 {
				ports = s.Process.ListeningPorts(childPID)
			}
			since := s.Store.ReadChildStartTime(id)
			var sincePtr *time.Time
			if !since.IsZero() {
				sincePtr = &since
			}
			return state.TaskStatus{
				State: "running",
				Running: &state.RunningInfo{
					SupervisorPID: pid,
					ChildPID:      childPID,
					Ports:         ports,
					Since:         sincePtr,
				},
			}
		}
		return state.TaskStatus{
			State: "dead",
			Dead: &state.DeadInfo{
				Message: "supervisor process no longer exists",
			},
		}
	}

	return state.TaskStatus{State: "unknown"}
}

// verifyAndGetPID reads the supervisor PID for id and verifies it hasn't
// been reused by an unrelated process (via create-time comparison).
// Returns the PID and true if the process is alive and verified.
func (s *Service) verifyAndGetPID(id string) (int, bool) {
	pid, err := s.Store.ReadPID(id, "supervisor.pid")
	if err != nil || pid <= 0 {
		return 0, false
	}
	if !s.Process.IsAlive(pid) {
		return pid, false
	}
	savedCreate := s.Store.ReadCreateTime(id)
	if !s.Process.VerifyPID(pid, savedCreate) {
		return pid, false
	}
	return pid, true
}

// killChildIfVerified kills the child process for id only after verifying
// the PID hasn't been reused by an unrelated process.
func (s *Service) killChildIfVerified(id string) {
	childPID, _ := s.Store.ReadPID(id, "child.pid")
	if childPID <= 0 || !s.Process.IsAlive(childPID) {
		return
	}
	savedCreate := s.Store.ReadChildCreateTime(id)
	if s.Process.VerifyPID(childPID, savedCreate) {
		_ = s.Process.SignalKill(childPID)
	}
}

// gracefulStop sends a stop signal to the supervisor, waits up to timeout,
// then escalates to SIGKILL, finally ensuring the child is also terminated.
//
// This is destructive process cleanup that, once the stop signal has been
// sent, must run to completion regardless of the calling request's
// context: an in-flight caller cancellation must never leave an
// orphaned/zombie supervisor or child behind. Accordingly gracefulStop
// takes no context and uses only bounded internal timers.
//
// It returns true if the graceful path had to escalate to SIGKILL (i.e.
// the supervisor did not exit within timeout on its own).
func (s *Service) gracefulStop(taskDir, id string, supervisorPID int, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	forced := false

	if runtime.GOOS == "windows" {
		// On Windows, TerminateProcess kills instantly without cleanup.
		// Try the ctl file first for graceful shutdown, then fall back to
		// TerminateProcess if the supervisor doesn't exit within the timeout.
		ctlWorked := false
		if err := s.Process.SignalStop(taskDir, supervisorPID); err == nil {
			ctlTicks := int(timeout / (100 * time.Millisecond))
			if ctlTicks < 1 {
				ctlTicks = 1
			}
			for i := 0; i < ctlTicks; i++ {
				if !s.Process.IsAlive(supervisorPID) {
					ctlWorked = true
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		if !ctlWorked {
			_ = s.Process.SignalStop(taskDir, supervisorPID)
			time.Sleep(200 * time.Millisecond)
		}
	} else {
		_ = s.Process.SignalStop(taskDir, supervisorPID)
	}

	// Wait for the process to exit (may already be done from ctl file path).
	ticks := int(timeout / (100 * time.Millisecond))
	if ticks < 1 {
		ticks = 1
	}
	for i := 0; i < ticks; i++ {
		if !s.Process.IsAlive(supervisorPID) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s.Process.IsAlive(supervisorPID) {
		_ = s.Process.SignalKill(supervisorPID)
		forced = true
		// On Windows, give the OS a moment to finalize process termination.
		if runtime.GOOS == "windows" {
			time.Sleep(500 * time.Millisecond)
		}
	}
	// Ensure the child is also terminated in case the supervisor didn't get
	// a chance to clean up (e.g., it was killed by the SIGKILL escalation).
	s.killChildIfVerified(id)
	return forced
}
