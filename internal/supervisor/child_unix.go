//go:build !windows

package supervisor

import "syscall"

func childSysProcAttr() *syscall.SysProcAttr {
	return nil
}
