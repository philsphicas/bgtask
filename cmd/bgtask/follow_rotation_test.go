package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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
	err := followLogs(path, exitPath, time.Millisecond, findNext, func(e supervisor.LogEntry) {
		got = append(got, e.Data)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"old", "new"}) {
		t.Fatalf("got %v, want [old new]", got)
	}
}
