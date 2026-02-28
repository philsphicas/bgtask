package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateLogIfNeeded_NoFile(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}
	// Missing file: ShouldRotateLog returns error, which is expected for nonexistent files.
	should, err := s.ShouldRotateLog("nonexistent", 1024)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ShouldRotateLog on missing file: unexpected error: %v", err)
	}
	if should {
		t.Fatal("ShouldRotateLog should return false for nonexistent file")
	}
}

func TestRotateLogIfNeeded_UnderLimit(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}
	id := "test-rotate"
	taskDir := filepath.Join(dir, id)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}

	logPath := s.OutputPath(id)
	if err := os.WriteFile(logPath, []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}

	should, err := s.ShouldRotateLog(id, 1024)
	if err != nil {
		t.Fatalf("ShouldRotateLog: %v", err)
	}
	if should {
		t.Fatal("should not need rotation for small file")
	}

	// File should still exist at original path.
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("log file should still exist: %v", err)
	}
}

func TestRotateLogIfNeeded_OverLimit(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}
	id := "test-rotate"
	taskDir := filepath.Join(dir, id)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}

	logPath := s.OutputPath(id)
	// Write a file larger than 10 bytes.
	if err := os.WriteFile(logPath, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}

	should, err := s.ShouldRotateLog(id, 10)
	if err != nil {
		t.Fatalf("ShouldRotateLog: %v", err)
	}
	if !should {
		t.Fatal("should need rotation for oversized file")
	}
	if err := s.RotateLog(id); err != nil {
		t.Fatalf("RotateLog: %v", err)
	}

	// Original should be gone (rotated).
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("original log should have been rotated away")
	}
	// .1 should exist.
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Errorf("rotated log .1 should exist: %v", err)
	}
}

func TestRotateLogShifts(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}
	id := "test-shift"
	taskDir := filepath.Join(dir, id)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}

	logPath := s.OutputPath(id)

	// Create .1 and .2 with known content.
	if err := os.WriteFile(logPath+".1", []byte("old-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath+".2", []byte("old-2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}

	should, err := s.ShouldRotateLog(id, 10)
	if err != nil {
		t.Fatalf("ShouldRotateLog: %v", err)
	}
	if !should {
		t.Fatal("should need rotation")
	}
	if err := s.RotateLog(id); err != nil {
		t.Fatalf("RotateLog: %v", err)
	}

	// .1 should have the current content.
	data, err := os.ReadFile(logPath + ".1") //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != strings.Repeat("x", 100) {
		t.Errorf(".1 should have current content, got %q", string(data))
	}
	// .2 should have old .1 content.
	data, err = os.ReadFile(logPath + ".2") //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-1" {
		t.Errorf(".2 should have old-1 content, got %q", string(data))
	}
	// .3 should have old .2 content.
	data, err = os.ReadFile(logPath + ".3") //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-2" {
		t.Errorf(".3 should have old-2 content, got %q", string(data))
	}
}

func TestListLogFiles(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}
	id := "test-list-logs"
	taskDir := filepath.Join(dir, id)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}

	logPath := s.OutputPath(id)
	if err := os.WriteFile(logPath, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath+".1", []byte("rotated-1"), 0o600); err != nil {
		t.Fatal(err)
	}

	files := s.ListLogFiles(id)
	if len(files) != 2 {
		t.Errorf("expected 2 log files, got %d", len(files))
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:      GenerateID(),
		Name:    "to-remove",
		Command: []string{"echo"},
		Cwd:     "/tmp",
	}
	if err := s.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Write some files.
	if err := s.WritePID(meta.ID, "supervisor.pid", 12345); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.OutputPath(meta.ID), []byte("log data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.Remove(meta.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Directory should be gone.
	if _, err := os.Stat(s.TaskDir(meta.ID)); !os.IsNotExist(err) {
		t.Errorf("task dir should be removed")
	}
}

func TestResolveAmbiguous(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	// Create two tasks with the same name (by writing meta directly).
	for i := 0; i < 2; i++ {
		meta := &Meta{
			ID:      fmt.Sprintf("id-%d", i),
			Name:    "duplicate",
			Command: []string{"echo"},
			Cwd:     "/tmp",
		}
		if err := s.Create(meta); err != nil {
			t.Fatal(err)
		}
	}

	_, _, err := s.Resolve("duplicate")
	if err == nil {
		t.Fatal("expected error for ambiguous name")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}
}
