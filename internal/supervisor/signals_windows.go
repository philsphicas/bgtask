//go:build windows

package supervisor

import "syscall"

// Signal constants for pause/resume. SIGUSR1/SIGUSR2 do not exist on Windows;
// define placeholder values that will never be delivered. The Windows code path
// uses control-file polling instead of signals (see the ctl file handler in
// supervisor.go).
const (
	sigUSR1 = syscall.Signal(0x1e) // never delivered on Windows
	sigUSR2 = syscall.Signal(0x1f) // never delivered on Windows
)
