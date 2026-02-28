//go:build !windows

package process

import "syscall"

// IsAlive checks if a process with the given PID is still running.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Signal 0 checks for process existence without sending a signal.
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// SignalTerm sends SIGTERM to a process.
func SignalTerm(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// SignalKill sends SIGKILL to a process.
func SignalKill(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
