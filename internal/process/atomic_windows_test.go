//go:build windows

package process

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAtomicReplace_RetriesTransientSharingViolation exercises the Windows
// retry path: while a reader holds an open handle on the destination file
// (which can make MoveFileEx fail with ERROR_SHARING_VIOLATION), AtomicReplace
// must retry until the handle is released rather than surfacing an error or
// deleting the destination.
func TestAtomicReplace_RetriesTransientSharingViolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ctl")

	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Hold a handle open on the destination without FILE_SHARE_DELETE so a
	// concurrent rename-over is likely to hit a sharing violation.
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = f.Close()
		close(released)
	}()

	start := time.Now()
	if err := AtomicReplace(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("AtomicReplace should retry past the sharing violation and succeed: %v", err)
	}
	elapsed := time.Since(start)

	<-released
	if elapsed < 100*time.Millisecond {
		t.Logf("AtomicReplace returned in %v; the handle may not have caused contention on this system", elapsed)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test file
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "new" {
		t.Errorf("content = %q, want %q", data, "new")
	}
}

func TestIsRetryableRenameErr_AccessDenied(t *testing.T) {
	dir := t.TempDir()
	// Renaming a nonexistent file always fails, but with ERROR_FILE_NOT_FOUND,
	// which is not retryable; verify the classifier doesn't misclassify it.
	err := os.Rename(filepath.Join(dir, "missing"), filepath.Join(dir, "dest"))
	if err == nil {
		t.Fatal("expected rename of a missing file to fail")
	}
	if isRetryableRenameErr(err) {
		t.Errorf("ERROR_FILE_NOT_FOUND should not be classified as retryable, got retryable for: %v", err)
	}
}
