//go:build windows

package supervisor

import (
	"context"
	"testing"
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
	const createNoWindow = 0x08000000
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Errorf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

func TestNewHealthCheckCommandFallsBackToCmdExe(t *testing.T) {
	t.Setenv("COMSPEC", "")

	cmd := newHealthCheckCommand(context.Background(), "echo healthy")
	if cmd.Args[0] != "cmd.exe" {
		t.Errorf("executable = %q, want cmd.exe", cmd.Args[0])
	}
}
