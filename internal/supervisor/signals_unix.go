//go:build !windows

package supervisor

import "syscall"

// Signal constants for pause/resume. On Unix systems we use the
// platform-native SIGUSR1/SIGUSR2 values from the syscall package.
const (
	sigUSR1 = syscall.SIGUSR1
	sigUSR2 = syscall.SIGUSR2
)
