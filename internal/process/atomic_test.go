package process

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAtomicReplace_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")

	if err := AtomicReplace(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("AtomicReplace: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test file
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}
}

func TestAtomicReplace_ReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")

	if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := AtomicReplace(path, []byte("new content"), 0o600); err != nil {
		t.Fatalf("AtomicReplace: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test file
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "new content" {
		t.Errorf("content = %q, want %q", data, "new content")
	}
}

func TestAtomicReplace_NoTempFilesLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")

	if err := AtomicReplace(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("AtomicReplace: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "meta.json" {
		t.Errorf("expected only meta.json in dir, got %v", entries)
	}
}

// TestAtomicReplace_FailureLeavesDestinationIntact simulates a failing
// replace (destination path is actually a directory, so the rename cannot
// succeed) and verifies the original destination is left untouched and no
// temp file leaks.
func TestAtomicReplace_FailureLeavesDestinationIntact(t *testing.T) {
	dir := t.TempDir()
	// Make the destination path a directory so renaming a file onto it fails.
	path := filepath.Join(dir, "meta.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := AtomicReplace(path, []byte("new content"), 0o600); err == nil {
		t.Fatal("expected AtomicReplace to fail when destination is a directory")
	}

	// Destination should still be the original directory, untouched.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("destination should still exist: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("destination should still be a directory")
	}

	// No leftover temp files.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the original directory entry, got %v", entries)
	}
}

// TestAtomicReplace_ConcurrentReadsNeverSeePartialContent writes many
// distinct full-size payloads to the same path in a tight loop while a
// reader goroutine repeatedly reads the file. Every read must observe one
// of the complete written payloads (or ErrNotExist before the first write
// lands), never a torn/partial one.
func TestAtomicReplace_ConcurrentReadsNeverSeePartialContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	const iterations = 200
	payloadSize := 4096 // large enough that a naive write would risk tearing

	valid := make(map[string]bool, iterations)
	for i := 0; i < iterations; i++ {
		payload := bytes.Repeat([]byte{byte('a' + i%26)}, payloadSize)
		valid[string(payload)] = true
	}

	var stop int32
	var readErr atomic.Value // stores string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for atomic.LoadInt32(&stop) == 0 {
			data, err := os.ReadFile(path) //nolint:gosec // test file
			if err != nil {
				// On Windows, opening a file at the exact instant it is being
				// replaced can transiently surface a sharing violation even
				// though the replace itself is atomic; that's a benign race
				// in this reader, not evidence of torn content, so retry.
				time.Sleep(time.Millisecond)
				continue
			}
			if len(data) != payloadSize || !valid[string(data)] {
				n := len(data)
				if n > 32 {
					n = 32
				}
				readErr.Store("observed partial or unknown content: " + string(data[:n]))
				return
			}
			// Poll rather than busy-loop: this mirrors how callers in this
			// codebase read state (occasionally, not in a hot loop), and
			// avoids starving the writer's bounded rename retries under
			// Windows sharing-violation contention.
			time.Sleep(time.Millisecond)
		}
	}()

	for i := 0; i < iterations; i++ {
		payload := bytes.Repeat([]byte{byte('a' + i%26)}, payloadSize)
		if err := AtomicReplace(path, payload, 0o600); err != nil {
			t.Fatalf("AtomicReplace iteration %d: %v", i, err)
		}
	}

	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	if v := readErr.Load(); v != nil {
		t.Errorf("concurrent reader saw an error: %v", v)
	}
}

func TestRenameWithRetry_NonRetryableFailsFast(t *testing.T) {
	dir := t.TempDir()
	// A nonexistent source path always fails and is never retryable.
	start := time.Now()
	err := renameWithRetry(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "dest"))
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("expected fast failure for non-retryable error, took %v", elapsed)
	}
}
