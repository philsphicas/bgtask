//go:build !windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Detach starts a detached child process that survives the parent's exit.
func Detach(args []string) (*os.Process, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start detached: %w", err)
	}

	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return &os.Process{Pid: pid}, nil
}

// SignalPause sends SIGUSR1 to a process (pause).
func SignalPause(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGUSR1)
}

// SignalResume sends SIGUSR2 to a process (resume).
func SignalResume(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGUSR2)
}
