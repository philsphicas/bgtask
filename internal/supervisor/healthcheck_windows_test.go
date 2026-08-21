//go:build windows

package supervisor

import (
	"context"
	"strings"
	"testing"
)

func TestNewHealthCheckCommandDoesNotCreateWindow(t *testing.T) {
	cmd := newHealthCheckCommand(context.Background(), "echo healthy")

	if !strings.EqualFold(cmd.Args[0], "cmd") {
		t.Fatalf("executable = %q, want cmd", cmd.Args[0])
	}
	if len(cmd.Args) != 3 || cmd.Args[1] != "/c" || cmd.Args[2] != "echo healthy" {
		t.Fatalf("args = %q, want [cmd /c \"echo healthy\"]", cmd.Args)
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	const createNoWindow = 0x08000000
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Errorf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}
