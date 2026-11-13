package state

import (
	"fmt"
	"os"
	"testing"
)

func TestLogRotationRetriesWithoutDiscardingMoreBackups(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := "retry"
	if err := os.MkdirAll(s.TaskDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	path := s.OutputPath(id)
	for i := 1; i <= MaxLogFiles; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.%d", path, i), []byte(fmt.Sprint(i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rotation := s.NewLogRotation(id)
	// The missing current file forces failure after the backups are shifted.
	for i := 0; i < 4; i++ {
		if err := rotation.Rotate(); err == nil {
			t.Fatal("expected current-file rename failure")
		}
		for suffix, want := range map[string]string{".2": "1", ".3": "2"} {
			data, err := os.ReadFile(path + suffix)
			if err != nil || string(data) != want {
				t.Fatalf("retry %d: %s = %q, err = %v", i, suffix, data, err)
			}
		}
	}
	if err := os.WriteFile(path, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rotation.Rotate(); err != nil {
		t.Fatal(err)
	}
	for suffix, want := range map[string]string{".1": "current", ".2": "1", ".3": "2"} {
		data, err := os.ReadFile(path + suffix)
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, err = %v", suffix, data, err)
		}
	}
}

func TestOpenLogAllowsRotation(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := "open-reader"
	if err := os.MkdirAll(s.TaskDir(id), 0o700); err != nil {
		t.Fatal(err)
	}

	path := s.OutputPath(id)
	if err := os.WriteFile(path, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := s.RotateLog(id); err != nil {
		t.Fatalf("rotation with open log reader: %v", err)
	}
	data := make([]byte, len("current"))
	if _, err := f.Read(data); err != nil || string(data) != "current" {
		t.Fatalf("read rotated open file: %q, %v", data, err)
	}
}
