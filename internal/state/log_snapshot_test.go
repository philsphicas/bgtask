package state

import (
	"errors"
	"os"
	"testing"
)

func TestOpenLogFilesDeduplicatesFileMovedDuringScan(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := "snapshot"
	if err := os.MkdirAll(s.TaskDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	path := s.OutputPath(id)
	if err := os.WriteFile(path, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}
	open := func(name string) (*os.File, error) {
		if name == path+".1" {
			if err := s.RotateLog(id); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return OpenLog(name)
	}
	files := openLogSnapshot(path, open)
	defer CloseLogSnapshot(files)
	if len(files) != 2 {
		t.Fatalf("got %d files, want two distinct files", len(files))
	}
	for i, want := range []string{"current", "older"} {
		if files[i].File == nil {
			t.Fatalf("file %d failed to open: %v", i, files[i].Err)
		}
		data := make([]byte, len(want))
		if _, err := files[i].File.Read(data); err != nil || string(data) != want {
			t.Fatalf("file %d = %q, err = %v", i, data, err)
		}
	}
}

func TestOpenLogSnapshotKeepsUnopenableFileAsError(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := "unreadable"
	if err := os.MkdirAll(s.TaskDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	path := s.OutputPath(id)
	for _, name := range []string{path, path + ".1", path + ".2"} {
		if err := os.WriteFile(name, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	openErr := errors.New("access denied")
	open := func(name string) (*os.File, error) {
		if name == path+".1" {
			return nil, openErr
		}
		return OpenLog(name)
	}
	files := openLogSnapshot(path, open)
	defer CloseLogSnapshot(files)
	if len(files) != 3 {
		t.Fatalf("got %d entries, want 3", len(files))
	}
	if files[0].File == nil || files[2].File == nil {
		t.Fatalf("readable files must stay open: %+v", files)
	}
	if files[1].File != nil || !errors.Is(files[1].Err, openErr) {
		t.Fatalf("unopenable file = %+v, want error %v", files[1], openErr)
	}
}
