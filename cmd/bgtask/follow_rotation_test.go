package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/supervisor"
)

func TestFollowDrainsWritesBeforeSwitching(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.jsonl")
	exitPath := filepath.Join(dir, "exit.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeEntry := func(path, text string) {
		t.Helper()
		data, err := json.Marshal(supervisor.LogEntry{Data: text, Stream: "o"})
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// The handle is opened before the entry below is written, as the service
	// does when it reads a snapshot, so the entry must not be skipped.
	f, err := state.OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	writeEntry(path, "gap")
	first := true
	findNext := func(f *os.File, name string) (*os.File, error) {
		if first {
			first = false
			writeEntry(path, "old")
			if err := os.Rename(path, path+".1"); err != nil {
				t.Fatal(err)
			}
			writeEntry(path, "new")
			if err := os.WriteFile(exitPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nextLogFile(f, name)
	}
	var got []string
	err = followLogs(f, path, exitPath, time.Millisecond, findNext, func(e supervisor.LogEntry) {
		got = append(got, e.Data)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"gap", "old", "new"}) {
		t.Fatalf("got %v, want [gap old new]", got)
	}
}
