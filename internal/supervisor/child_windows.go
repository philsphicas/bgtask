//go:build windows

package supervisor

import "syscall"

// childSysProcAttr returns SysProcAttr for child processes on Windows.
// CREATE_NO_WINDOW prevents Windows from allocating a console (conhost.exe)
// for the child. Without this, conhost holds pipe handles open and the
// supervisor's stream capture goroutines never see EOF.
func childSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
