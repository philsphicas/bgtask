//go:build !windows

package supervisor

import "syscall"

const sigHUP = syscall.SIGHUP
