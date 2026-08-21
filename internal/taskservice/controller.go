package taskservice

import (
	"context"

	"github.com/philsphicas/bgtask/internal/process"
)

// Launcher starts a new detached supervisor process for a task. Production
// code uses processLauncher, which wraps process.Detach; tests substitute a
// fake to avoid spawning real processes.
type Launcher interface {
	// Launch starts the hidden "supervisor" re-exec for the task with the
	// given ID, whose state lives under storeRoot. It returns the new
	// supervisor's PID, whether it failed to break away from a Windows job
	// object (see process.Detach), and any error.
	Launch(ctx context.Context, storeRoot, taskID string) (pid int, noBreakaway bool, err error)
}

// processLauncher is the production Launcher: it re-execs the current
// binary as a hidden "supervisor" subcommand via process.Detach.
type processLauncher struct{}

func (processLauncher) Launch(_ context.Context, storeRoot, taskID string) (int, bool, error) {
	proc, noBreakaway, err := process.Detach([]string{"supervisor", storeRoot, taskID})
	if err != nil {
		return 0, false, err
	}
	return proc.Pid, noBreakaway, nil
}

// ProcessController abstracts process lifecycle queries and signals so they
// can be faked in tests. Production code (osProcessController) is
// taskDir-aware: on Windows, SignalStop/SignalRestart write to the task's
// control file (see process.SignalStopDir/SignalRestartDir) instead of
// signaling a PID directly, since Windows has no equivalent of SIGTERM/
// SIGHUP.
type ProcessController interface {
	// IsAlive reports whether pid currently refers to a live process.
	IsAlive(pid int) bool

	// VerifyPID reports whether pid still refers to the same process that
	// had the given createTime, guarding against PID reuse.
	VerifyPID(pid int, createTime int64) bool

	// CreateTime returns an opaque process creation-time identifier for
	// pid, or 0 if unavailable.
	CreateTime(pid int) int64

	// SignalStop asks the process to stop gracefully (SIGTERM on Unix; a
	// "stop" control-file command on Windows, using taskDir).
	SignalStop(taskDir string, pid int) error

	// SignalRestart asks the supervisor to restart its child (SIGHUP on
	// Unix; a "restart" control-file command on Windows, using taskDir).
	SignalRestart(taskDir string, pid int) error

	// SignalKill forcefully terminates pid (SIGKILL on Unix; TerminateProcess
	// on Windows).
	SignalKill(pid int) error

	// ListeningPorts returns the TCP ports pid is listening on.
	ListeningPorts(pid int) []uint32
}

// osProcessController is the production ProcessController, backed by
// internal/process.
type osProcessController struct{}

func (osProcessController) IsAlive(pid int) bool { return process.IsAlive(pid) }
func (osProcessController) VerifyPID(pid int, createTime int64) bool {
	return process.VerifyPID(pid, createTime)
}
func (osProcessController) CreateTime(pid int) int64 { return process.CreateTime(pid) }
func (osProcessController) SignalStop(taskDir string, pid int) error {
	return process.SignalStopDir(taskDir, pid)
}
func (osProcessController) SignalRestart(taskDir string, pid int) error {
	return process.SignalRestartDir(taskDir, pid)
}
func (osProcessController) SignalKill(pid int) error { return process.SignalKill(pid) }
func (osProcessController) ListeningPorts(pid int) []uint32 {
	return process.ListeningPorts(pid)
}
