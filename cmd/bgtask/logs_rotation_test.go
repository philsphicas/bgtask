package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/philsphicas/bgtask/internal/state"
)

func TestNextLogFileTraversesRotations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.jsonl")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	current, err := state.OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if err := os.Rename(path, path+".2"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("middle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := nextLogFile(current, path)
	if err != nil || next == nil {
		t.Fatalf("next file: %v", err)
	}
	defer next.Close()
	info, _ := next.Stat()
	middle, _ := os.Stat(path + ".1")
	if !os.SameFile(info, middle) {
		t.Fatal("did not advance to the next oldest file")
	}
	last, err := nextLogFile(next, path)
	if err != nil || last == nil {
		t.Fatalf("current file: %v", err)
	}
	defer last.Close()
	info, _ = last.Stat()
	newest, _ := os.Stat(path)
	if !os.SameFile(info, newest) {
		t.Fatal("did not advance to the current file")
	}
	noNext, err := nextLogFile(last, path)
	if err != nil || noNext != nil {
		t.Fatalf("unexpected next file: %v, %v", noNext, err)
	}
}

func TestNextLogFileAfterOldFileIsDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.jsonl")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := state.OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("retained\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := nextLogFile(current, path)
	if err != nil || next == nil {
		t.Fatalf("next file: %v", err)
	}
	defer next.Close()
	info, err := next.Stat()
	if err != nil {
		t.Fatal(err)
	}
	oldest, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, oldest) {
		t.Fatal("did not resume at the oldest retained file")
	}
}
