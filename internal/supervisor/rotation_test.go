package supervisor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
)

func rotationWriter(t *testing.T, maxSize int64) (*jsonlWriter, *state.Store, string) {
	t.Helper()
	store := &state.Store{Root: t.TempDir()}
	id := "rotation-test"
	if err := os.MkdirAll(store.TaskDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(store.OutputPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	w := &jsonlWriter{f: f, store: store, id: id, maxSize: maxSize}
	t.Cleanup(w.close)
	return w, store, id
}

func TestWriterRotatesConcurrentStreamsAndRetainsBoundedFiles(t *testing.T) {
	const maxSize = 1024
	w, store, id := rotationWriter(t, maxSize)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(stream int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				w.WriteOutput("o", fmt.Sprintf("%d-%d\n", stream, j))
			}
		}(i)
	}
	wg.Wait()
	files := store.ListLogFiles(id)
	if len(files) != 1+state.MaxLogFiles {
		t.Fatalf("got %d files, want %d", len(files), 1+state.MaxLogFiles)
	}
	seen := map[string]bool{}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxSize {
			t.Fatalf("file %s size/error: %v / %v", path, info, err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var e LogEntry
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				t.Fatalf("invalid JSONL in %s: %v", path, err)
			}
			if seen[e.Data] {
				t.Fatalf("duplicate entry %q", e.Data)
			}
			seen[e.Data] = true
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) == 0 || len(seen) >= 400 {
		t.Fatalf("expected some, but not all, entries retained: %d", len(seen))
	}
}

func TestWriterRotatesLifecycleAndHealthEvents(t *testing.T) {
	w, store, id := rotationWriter(t, 256)
	w.WriteOutput("o", strings.Repeat("x", 140))
	w.WriteEvent("restarting", nil, nil, "")
	w.WriteHealthEvent("health_fail", "failed")
	files := store.ListLogFiles(id)
	if len(files) < 2 {
		t.Fatal("expected lifecycle and health events to trigger rotation")
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.Size() > 256 {
			t.Fatalf("size/error: %v / %v", info, err)
		}
	}
}

func TestWriterReportsAndRecoversFromRotationFailure(t *testing.T) {
	w, store, id := rotationWriter(t, 256)
	w.WriteOutput("o", strings.Repeat("x", 140))
	blocker := store.OutputPath(id) + ".3"
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "block"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.WriteOutput("o", strings.Repeat("y", 140))
	errPath := filepath.Join(store.TaskDir(id), "log-error")
	if _, err := os.Stat(errPath); err != nil {
		t.Fatalf("missing rotation diagnostic: %v", err)
	}
	if err := os.Remove(filepath.Join(blocker, "block")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	w.WriteOutput("e", "recovered\n")
	entries := readLogEntries(t, store.OutputPath(id))
	if len(entries) != 2 || entries[0].Data != "log_error" || entries[1].Data != "recovered\n" {
		t.Fatalf("unexpected entries after recovery: %+v", entries)
	}
	if _, err := os.Stat(errPath); err != nil {
		t.Fatalf("diagnostic must remain visible after recovery: %v", err)
	}
}

func TestWriterOmitsOversizedEntry(t *testing.T) {
	w, store, id := rotationWriter(t, 256)
	w.WriteHealthEvent("health_fail", strings.Repeat("x", 1024))
	entries := readLogEntries(t, store.OutputPath(id))
	if len(entries) != 1 || entries[0].Data != "log_entry_omitted" {
		t.Fatalf("expected omission notice: %+v", entries)
	}
	info, err := os.Stat(store.OutputPath(id))
	if err != nil || info.Size() > 256 {
		t.Fatalf("size/error: %v / %v", info, err)
	}
}

func TestLiveRotationHelper(t *testing.T) {
	if os.Getenv("BGTASK_ROTATION_HELPER") != "1" {
		return
	}
	line := strings.Repeat("x", 1023) + "\n"
	for i := 0; i < 11000; i++ {
		if _, err := fmt.Print(line); err != nil {
			os.Exit(2)
		}
	}
	fmt.Println("rotation-ready")
	for {
		if _, err := os.Stat(os.Getenv("BGTASK_ROTATION_RELEASE")); err == nil { // #nosec G703 -- test helper path supplied by the parent test.
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunRotatesWhileChildIsRunning(t *testing.T) {
	store := &state.Store{Root: t.TempDir()}
	release := filepath.Join(t.TempDir(), "release")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	meta := &state.Meta{
		ID: state.GenerateID(), Name: "live-rotation", Cwd: t.TempDir(),
		Command: []string{exe, "-test.run=^TestLiveRotationHelper$"},
		EnvOverrides: map[string]string{
			"BGTASK_ROTATION_HELPER": "1", "BGTASK_ROTATION_RELEASE": release,
		},
	}
	if err := store.Create(meta); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Run(&Config{StateDir: store.TaskDir(meta.ID), Meta: meta, Store: store}) }()
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0o600)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("supervisor did not finish")
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		if info, err := os.Stat(store.OutputPath(meta.ID) + ".1"); err == nil {
			if info.Size() > state.MaxLogSize {
				t.Fatalf("rotated file too large: %d", info.Size())
			}
			select {
			case err := <-done:
				t.Fatalf("supervisor exited before release: %v", err)
			default:
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rotation did not occur while child was running")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
