package process

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// atomicReplaceMaxAttempts bounds how many times AtomicReplace retries a
// rename that fails with a transient Windows access/sharing violation
// (e.g. a reader briefly holding the destination open, or antivirus
// scanning). POSIX renames are atomic and never hit this path.
const atomicReplaceMaxAttempts = 25

// atomicReplaceRetryDelay is the pause between retried rename attempts.
const atomicReplaceRetryDelay = 20 * time.Millisecond

// AtomicReplace writes data to path atomically: it writes to a temporary
// file in the same directory, then renames the temporary file onto path.
//
// The destination is never removed before the rename, so a failed replace
// (including one that exhausts its retries) always leaves any previously
// complete file at path intact. On Windows, a rename onto an existing file
// can fail transiently with an access or sharing violation (for example a
// reader with a brief open handle, or antivirus scanning); such failures
// are retried with a bounded backoff. The temporary file is cleaned up on
// any failure.
func AtomicReplace(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath) //nolint:gosec // path comes from os.CreateTemp in the caller-selected directory
		}
	}()

	if err := writeFull(tmp, data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	// Best-effort permission fixup; CreateTemp always creates with 0o600,
	// which matches most callers, but honor an explicit perm if different.
	if perm != 0 {
		_ = tmp.Chmod(perm)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := renameWithRetry(tmpPath, path); err != nil {
		return err
	}
	cleanup = false // renamed away; nothing left to remove
	return nil
}

func writeFull(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

// renameWithRetry renames oldpath to newpath, retrying transient Windows
// access/sharing violations with a bounded backoff. On other platforms (or
// for non-retryable errors) it fails immediately after the first attempt.
func renameWithRetry(oldpath, newpath string) error {
	var err error
	for attempt := 1; attempt <= atomicReplaceMaxAttempts; attempt++ {
		err = os.Rename(oldpath, newpath) //nolint:gosec // both paths are internally constructed state/control paths
		if err == nil {
			return nil
		}
		if !isRetryableRenameErr(err) {
			return fmt.Errorf("rename %s -> %s: %w", oldpath, newpath, err)
		}
		if attempt < atomicReplaceMaxAttempts {
			time.Sleep(atomicReplaceRetryDelay)
		}
	}
	return fmt.Errorf("rename %s -> %s: gave up after %d attempts: %w", oldpath, newpath, atomicReplaceMaxAttempts, err)
}
