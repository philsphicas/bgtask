//go:build windows

package supervisor

import "syscall"

// SIGHUP is not delivered on Windows; the ctl file handler in supervisor.go
// polls for a "restart" action instead.
const sigHUP = syscall.Signal(0x01)
