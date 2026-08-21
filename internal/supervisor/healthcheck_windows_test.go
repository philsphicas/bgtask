//go:build windows

package supervisor

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNewHealthCheckCommandDoesNotCreateWindow(t *testing.T) {
	const comspec = `C:\Windows\System32\cmd.exe`
	t.Setenv("COMSPEC", comspec)

	cmd := newHealthCheckCommand(context.Background(), "echo healthy")
	if len(cmd.Args) != 3 || cmd.Args[0] != comspec || cmd.Args[1] != "/c" || cmd.Args[2] != "echo healthy" {
		t.Fatalf("args = %q, want [%q /c \"echo healthy\"]", cmd.Args, comspec)
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Errorf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

func TestNewHealthCheckCommandFallsBackToCmdExe(t *testing.T) {
	t.Setenv("COMSPEC", "")

	cmd := newHealthCheckCommand(context.Background(), "echo healthy")
	if len(cmd.Args) != 3 || cmd.Args[0] != "cmd.exe" || cmd.Args[1] != "/c" || cmd.Args[2] != "echo healthy" {
		t.Fatalf("args = %q, want [cmd.exe /c \"echo healthy\"]", cmd.Args)
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Errorf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}
