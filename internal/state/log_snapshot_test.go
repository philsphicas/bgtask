package state

import (
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
	files, err := openLogFiles(path, open)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseLogFiles(files)
	if len(files) != 2 {
		t.Fatalf("got %d files, want two distinct files", len(files))
	}
	for i, want := range []string{"current", "older"} {
		data := make([]byte, len(want))
		if _, err := files[i].Read(data); err != nil || string(data) != want {
			t.Fatalf("file %d = %q, err = %v", i, data, err)
		}
	}
}
