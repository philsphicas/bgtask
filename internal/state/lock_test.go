package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// nonexistentPID is a PID above the default Linux pid_max that should never
// correspond to a live process on any reasonable system, matching the
// convention used in internal/process's own tests.
const nonexistentPID = 4194304

// writeStaleLease writes a lease record directly to path, bypassing
// acquireLease, so tests can construct a lock file that looks abandoned:
// its heartbeat is older than leaseStaleAfter and its owner PID is not
// alive.
func writeStaleLease(t *testing.T, path string, pid int) {
	t.Helper()
	rec := leaseRecord{
		PID:         pid,
		Nonce:       "stale-nonce",
		AcquiredAt:  time.Now().Add(-time.Hour),
		HeartbeatAt: time.Now().Add(-time.Hour),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLeaseRecordForTest(t *testing.T, path string) leaseRecord {
	t.Helper()
	rec, ok := readLeaseRecord(path)
	if !ok {
		t.Fatalf("lease file %s missing or unparseable", path)
	}
	return rec
}

func TestLockContext_RecordsOwnerFields(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	before := time.Now()
	lease, err := s.LockContext(context.Background())
	if err != nil {
		t.Fatalf("LockContext: %v", err)
	}
	defer lease.Unlock()

	rec := readLeaseRecordForTest(t, filepath.Join(dir, ".lock"))
	if rec.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", rec.PID, os.Getpid())
	}
	if rec.Nonce == "" {
		t.Error("expected a non-empty nonce")
	}
	if rec.AcquiredAt.Before(before) {
		t.Errorf("AcquiredAt = %v, want >= %v", rec.AcquiredAt, before)
	}
	if rec.HeartbeatAt.Before(before) {
		t.Errorf("HeartbeatAt = %v, want >= %v", rec.HeartbeatAt, before)
	}
}

func TestLockContext_UnlockRemovesLockFile(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	lease, err := s.LockContext(context.Background())
	if err != nil {
		t.Fatalf("LockContext: %v", err)
	}
	lockPath := filepath.Join(dir, ".lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should exist while held: %v", err)
	}

	lease.Unlock()
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock file should be removed after Unlock, stat err = %v", err)
	}
}

func TestLockContext_UnlockIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	lease, err := s.LockContext(context.Background())
	if err != nil {
		t.Fatalf("LockContext: %v", err)
	}
	lease.Unlock()
	lease.Unlock() // Must not panic or block.
}

// TestLease_UnlockOnlyRemovesMatchingNonce verifies the conditional-unlock
// invariant directly: if the on-disk lock file no longer matches the
// lease's nonce (e.g. it was reclaimed as stale and reacquired by a new
// owner), Unlock must leave that new owner's file alone.
func TestLease_UnlockOnlyRemovesMatchingNonce(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	lease, err := s.LockContext(context.Background())
	if err != nil {
		t.Fatalf("LockContext: %v", err)
	}
	lockPath := filepath.Join(dir, ".lock")

	// Simulate a new owner having reclaimed the path with a different nonce
	// (as if the original lease's process had crashed, this path was
	// reaped, and a new acquisition happened) while our stale Lease handle
	// is still around.
	newOwner := leaseRecord{
		PID:         os.Getpid(),
		Nonce:       "someone-elses-nonce",
		AcquiredAt:  time.Now(),
		HeartbeatAt: time.Now(),
	}
	data, err := json.Marshal(newOwner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	lease.Unlock()

	rec := readLeaseRecordForTest(t, lockPath)
	if rec.Nonce != "someone-elses-nonce" {
		t.Errorf("Unlock removed/altered a lock file it did not own; nonce = %q", rec.Nonce)
	}
}

func TestLockContext_ContextDeadlineExceeded(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	holder, err := s.LockContext(context.Background())
	if err != nil {
		t.Fatalf("LockContext (holder): %v", err)
	}
	defer holder.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err = s.LockContext(ctx)
	if err == nil {
		t.Fatal("expected LockContext to fail while the lock is held")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, context.DeadlineExceeded), got: %v", err)
	}
}

func TestLockContext_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	holder, err := s.LockContext(context.Background())
	if err != nil {
		t.Fatalf("LockContext (holder): %v", err)
	}
	defer holder.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err = s.LockContext(ctx)
	if err == nil {
		t.Fatal("expected LockContext to fail once its context is canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected errors.Is(err, context.Canceled), got: %v", err)
	}
}

func TestLockTaskContext_IndependentTasksDoNotBlock(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	leaseA, err := s.LockTaskContext(context.Background(), "task-a")
	if err != nil {
		t.Fatalf("LockTaskContext(task-a): %v", err)
	}
	defer leaseA.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	leaseB, err := s.LockTaskContext(ctx, "task-b")
	if err != nil {
		t.Fatalf("LockTaskContext(task-b) should not block on an unrelated task's lock: %v", err)
	}
	leaseB.Unlock()
}

func TestLockTaskContext_SameTaskContends(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	lease, err := s.LockTaskContext(context.Background(), "task-a")
	if err != nil {
		t.Fatalf("LockTaskContext: %v", err)
	}
	defer lease.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err = s.LockTaskContext(ctx, "task-a")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected a second lock on the same task ID to contend and time out, got: %v", err)
	}
}

func TestLockTaskContext_DoesNotBlockGlobalLock(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	taskLease, err := s.LockTaskContext(context.Background(), "task-a")
	if err != nil {
		t.Fatalf("LockTaskContext: %v", err)
	}
	defer taskLease.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	globalLease, err := s.LockContext(ctx)
	if err != nil {
		t.Fatalf("global LockContext should not block on a per-task lock: %v", err)
	}
	globalLease.Unlock()
}

func TestLockTaskContext_UnderLocksDir(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	lease, err := s.LockTaskContext(context.Background(), "20240101T000000-abcd1234")
	if err != nil {
		t.Fatalf("LockTaskContext: %v", err)
	}
	defer lease.Unlock()

	locksDir := filepath.Join(dir, ".locks")
	entries, err := os.ReadDir(locksDir)
	if err != nil {
		t.Fatalf("ReadDir(.locks): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one lock file under .locks, got %v", entries)
	}
}

func TestSafeLockName_SanitizesUnsafeCharacters(t *testing.T) {
	cases := map[string]string{ //nolint:gosec // task IDs and paths, not credentials
		"20240101T000000-abcd1234": "20240101T000000-abcd1234",
		"":                         "task",
		"../../etc/passwd":         ".._.._etc_passwd",
		"a/b\\c":                   "a_b_c",
	}
	for in, want := range cases {
		if got := safeLockName(in); got != want {
			t.Errorf("safeLockName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTryReapStaleLease_LeavesFreshLeaseAlone(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	lease, err := s.LockContext(context.Background())
	if err != nil {
		t.Fatalf("LockContext: %v", err)
	}
	defer lease.Unlock()

	lockPath := filepath.Join(dir, ".lock")
	if tryReapStaleLease(lockPath) {
		t.Error("a freshly-acquired, live lease must never be reaped")
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file should still exist: %v", err)
	}
}

func TestTryReapStaleLease_LeavesRecentlyHeldLeaseAlone(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")

	// Heartbeat is recent (well under leaseStaleAfter) even though the
	// recorded PID is not alive; a lease that isn't stale yet must not be
	// reclaimed regardless of owner liveness.
	rec := leaseRecord{
		PID:         nonexistentPID,
		Nonce:       "recent",
		AcquiredAt:  time.Now(),
		HeartbeatAt: time.Now(),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if tryReapStaleLease(lockPath) {
		t.Error("a lease with a recent heartbeat must not be reaped, even if its PID is dead")
	}
}

func TestTryReapStaleLease_ReapsAbandonedLease(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")
	writeStaleLease(t, lockPath, nonexistentPID)

	if !tryReapStaleLease(lockPath) {
		t.Fatal("expected a stale lease with a dead owner to be reaped")
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock file should be removed after reap, stat err = %v", err)
	}
}

func TestTryReapStaleLease_DoesNotReapLiveOwner(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")

	// Stale heartbeat, but PID is our own (definitely alive) process: must
	// not be reaped.
	rec := leaseRecord{
		PID:         os.Getpid(),
		Nonce:       "still-alive",
		AcquiredAt:  time.Now().Add(-time.Hour),
		HeartbeatAt: time.Now().Add(-time.Hour),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if tryReapStaleLease(lockPath) {
		t.Error("must not reap a lease whose owner PID is still alive")
	}
}

func TestTryReapStaleLease_SkipsRecentUnparseableContent(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")
	if err := os.WriteFile(lockPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if tryReapStaleLease(lockPath) {
		t.Error("unparseable lock content must not be reaped (could be a torn write in progress)")
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file should be left alone: %v", err)
	}
}

func TestTryReapStaleLease_ReapsStaleUnparseableContent(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-leaseStaleAfter - time.Second)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	if !tryReapStaleLease(lockPath) {
		t.Fatal("expected stale unparseable lock content to be reaped")
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock file should be removed after reap, stat err = %v", err)
	}
}

// TestLockContext_ConcurrentContendersExactlyOneWinsAtATime stresses the
// acquire/heartbeat/unlock path with several goroutines competing for the
// same per-task lock, verifying mutual exclusion end to end (no two
// goroutines observe the lock as held simultaneously).
func TestLockContext_ConcurrentContendersExactlyOneWinsAtATime(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	const contenders = 6
	var mu sync.Mutex
	held := false
	var violations int

	var wg sync.WaitGroup
	wg.Add(contenders)
	for i := 0; i < contenders; i++ {
		go func() {
			defer wg.Done()
			// A generous per-contender budget: correctness (no mutual
			// exclusion violations), not tight timing, is what this test
			// checks, and CI machines may run this alongside other package
			// test binaries with unpredictable scheduling delays.
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			lease, err := s.LockTaskContext(ctx, "shared-task")
			if err != nil {
				t.Errorf("LockTaskContext: %v", err)
				return
			}

			mu.Lock()
			if held {
				violations++
			}
			held = true
			mu.Unlock()

			time.Sleep(10 * time.Millisecond)

			mu.Lock()
			held = false
			mu.Unlock()

			lease.Unlock()
		}()
	}
	wg.Wait()

	if violations != 0 {
		t.Errorf("observed %d mutual-exclusion violations", violations)
	}
}
