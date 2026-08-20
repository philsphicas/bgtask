//go:build windows

package process

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSignalStopDir_WritesCtlFileDirectly(t *testing.T) {
	dir := t.TempDir()

	if err := SignalStopDir(dir, 12345); err != nil {
		t.Fatalf("SignalStopDir: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "ctl")) //nolint:gosec // test file
	if err != nil {
		t.Fatalf("ReadFile ctl: %v", err)
	}
	if string(data) != "stop" {
		t.Errorf("ctl content = %q, want %q", data, "stop")
	}
}

func TestSignalRestartDir_WritesCtlFileDirectly(t *testing.T) {
	dir := t.TempDir()

	if err := SignalRestartDir(dir, 12345); err != nil {
		t.Fatalf("SignalRestartDir: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "ctl")) //nolint:gosec // test file
	if err != nil {
		t.Fatalf("ReadFile ctl: %v", err)
	}
	if string(data) != "restart" {
		t.Errorf("ctl content = %q, want %q", data, "restart")
	}
}

func TestSignalStopDir_ReplacesExistingCtlFile(t *testing.T) {
	dir := t.TempDir()
	ctlPath := filepath.Join(dir, "ctl")
	if err := os.WriteFile(ctlPath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SignalStopDir(dir, 1); err != nil {
		t.Fatalf("SignalStopDir: %v", err)
	}

	data, err := os.ReadFile(ctlPath) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "stop" {
		t.Errorf("ctl content = %q, want %q", data, "stop")
	}
}

// TestSignalStop_FindsTaskByPID verifies the legacy PID-scanning entry point
// (used by callers not yet migrated to the taskDir-based API) still locates
// the right task directory and writes its ctl file.
func TestSignalStop_FindsTaskByPID(t *testing.T) {
	xdgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgHome)
	// writeCtlFile resolves the procs dir as XDG_CONFIG_HOME/bgtask/procs,
	// mirroring state.configDir()'s layout.
	taskDir := filepath.Join(xdgHome, "bgtask", "procs", "task-1")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "supervisor.pid"), []byte("54321"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SignalStop(54321); err != nil {
		t.Fatalf("SignalStop: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(taskDir, "ctl")) //nolint:gosec // test file
	if err != nil {
		t.Fatalf("ReadFile ctl: %v", err)
	}
	if string(data) != "stop" {
		t.Errorf("ctl content = %q, want %q", data, "stop")
	}
}

func TestSignalStop_NoMatchingTask(t *testing.T) {
	procsDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", procsDir)
	bgtaskProcs := filepath.Join(procsDir, "bgtask", "procs")
	if err := os.MkdirAll(bgtaskProcs, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := SignalStop(999999); err == nil {
		t.Fatal("expected error when no task matches the PID")
	}
}
