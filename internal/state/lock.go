package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/philsphicas/bgtask/internal/process"
)

// leaseRecord is the JSON body of a lock file. It records enough information
// about the current holder to let a later waiter decide, without any
// out-of-band coordination, whether the lock is still legitimately held:
//
//   - PID/CreateTime identify the holder's OS process, so a lock left behind
//     by a crashed process can be told apart from one whose owner is still
//     alive (including surviving PID reuse by an unrelated process).
//   - Nonce is a random value chosen at acquisition time. Only the holder
//     that wrote a given nonce is allowed to remove the file for that
//     acquisition, so a stale-lock reap can never race with, and delete,
//     a different, later acquisition by a new owner.
//   - AcquiredAt/HeartbeatAt bound how long a lock can go unrenewed before
//     it becomes eligible for stale reclamation.
type leaseRecord struct {
	PID         int       `json:"pid"`
	CreateTime  int64     `json:"create_time,omitempty"`
	Nonce       string    `json:"nonce"`
	AcquiredAt  time.Time `json:"acquired_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
}

const (
	// lockRetries/lockInterval bound the fixed retry budget used by the
	// backward-compatible Lock() wrapper (50 * 100ms = 5s), matching the
	// timeout of the previous polling implementation.
	lockRetries  = 50
	lockInterval = 100 * time.Millisecond

	// leaseHeartbeatInterval controls how often a held lease refreshes its
	// heartbeat_at timestamp on disk.
	leaseHeartbeatInterval = 5 * time.Second

	// leaseStaleAfter is how long a lease can go without a heartbeat update
	// before it becomes *eligible* for reap. It is still only reaped once
	// the recorded owner process is also confirmed no longer alive.
	leaseStaleAfter = 30 * time.Second

	// leasePollInterval is how often acquireLease retries while waiting for
	// a contended lock.
	leasePollInterval = 100 * time.Millisecond

	// leaseRemoveAttempts/leaseRemoveInterval bound how long Unlock retries
	// deleting its own lock file (50 * 10ms = 500ms). On Windows a waiter
	// that is concurrently polling the same lock file holds a brief share on
	// it, which makes an unlucky delete fail with a sharing violation. A
	// dropped delete would orphan the lock file for the rest of the process
	// lifetime (its owner is still alive, so it can never be reaped as
	// stale), so removal is retried rather than attempted once.
	leaseRemoveAttempts = 50
	leaseRemoveInterval = 10 * time.Millisecond
)

// Lease represents an acquired, owned lock obtained via LockContext or
// LockTaskContext. Call Unlock to release it.
type Lease struct {
	path  string
	nonce string
	once  sync.Once

	stopHeartbeat chan struct{}
	heartbeatDone chan struct{}
}

// Unlock releases the lease. It is idempotent and safe to call more than
// once (including via defer combined with an earlier explicit call).
//
// Unlock only removes the lock file if it still contains this lease's
// nonce, so a lease that was already reclaimed as stale (and possibly
// reacquired by a new owner) can never delete another owner's lock.
func (l *Lease) Unlock() {
	l.once.Do(func() {
		if l.stopHeartbeat != nil {
			close(l.stopHeartbeat)
			<-l.heartbeatDone
		}
		removeLeaseIfOwned(l.path, l.nonce)
	})
}

// removeLeaseIfOwned removes the lease file at path only if its current
// on-disk content still matches nonce.
//
// Removal is retried for a bounded window because on Windows an unrelated
// waiter reading the same lock file can make a single os.Remove fail with a
// transient sharing violation. Ownership is re-checked before every attempt,
// so a lease that gets reaped and reacquired mid-retry is never deleted out
// from under its new owner.
func removeLeaseIfOwned(path, nonce string) {
	for attempt := 0; attempt < leaseRemoveAttempts; attempt++ {
		rec, ok := readLeaseRecord(path)
		switch {
		case ok && rec.Nonce != nonce:
			return // Reaped and reacquired by someone else; not ours to delete.
		case !ok:
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				return // Already gone.
			}
			// Unreadable right now (transient share, or a torn write): retry.
		default:
			if err := os.Remove(path); err == nil {
				return
			}
		}
		time.Sleep(leaseRemoveInterval)
	}
}

// readLeaseRecord reads and parses a lease file. ok is false if the file is
// missing or its content isn't a parseable leaseRecord (e.g. a torn write in
// progress), in which case callers must not treat it as owned by anyone.
func readLeaseRecord(path string) (rec leaseRecord, ok bool) {
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		return leaseRecord{}, false
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return leaseRecord{}, false
	}
	return rec, true
}

// acquireLease attempts to create the lease file at path, retrying until it
// succeeds or ctx is done. Creation uses O_CREATE|O_EXCL so a live lease is
// never silently overwritten; only a lease removed by Unlock or reaped by
// tryReapStaleLease frees up the path for a new O_EXCL create.
func acquireLease(ctx context.Context, path string) (*Lease, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	nonce, err := randomNonce()
	if err != nil {
		return nil, fmt.Errorf("generate lease nonce: %w", err)
	}

	for {
		acquired, err := createLeaseFile(path, nonce)
		if err != nil {
			return nil, err
		}
		if acquired {
			return startLease(path, nonce), nil
		}

		if tryReapStaleLease(path) {
			continue // Retry immediately; no need to wait out a poll tick.
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("acquire lock %s: %w", filepath.Base(path), ctx.Err())
		case <-time.After(leasePollInterval):
		}
	}
}

// createLeaseFile attempts one O_EXCL creation of the lease file. It reports
// acquired=false (with a nil error) when the file already exists, so the
// caller can decide whether to reap it as stale and retry.
func createLeaseFile(path, nonce string) (acquired bool, err error) {
	rec := leaseRecord{
		PID:         os.Getpid(),
		CreateTime:  process.CreateTime(os.Getpid()),
		Nonce:       nonce,
		AcquiredAt:  time.Now(),
		HeartbeatAt: time.Now(),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return false, fmt.Errorf("marshal lease: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is constructed internally
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("create lease: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return false, fmt.Errorf("write lease: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("write lease: %w", err)
	}
	return true, nil
}

// tryReapStaleLease removes path if it holds a parseable lease record whose
// heartbeat is stale AND whose recorded owner process is confirmed no
// longer alive. It re-reads the file immediately before removing and
// requires the bytes to be byte-identical to the earlier staleness-check
// read; if the content changed in between (a heartbeat renewal, or another
// waiter already reaped and a new owner recreated it), it aborts without
// removing anything. This closes the race where a stale holder's reap could
// otherwise delete a new owner's lock.
func tryReapStaleLease(path string) bool {
	first, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		return false
	}
	var rec leaseRecord
	if err := json.Unmarshal(first, &rec); err != nil {
		// An unparseable file may be a writer still between O_EXCL create and
		// its first write, so leave recent files alone. Once it has remained
		// unchanged beyond the stale interval it cannot represent a renewing
		// lease (and may be a crash remnant or a legacy empty lock file), so it
		// is safe to reclaim using the same two-read equality guard below.
		info, statErr := os.Stat(path)
		if statErr != nil || time.Since(info.ModTime()) < leaseStaleAfter {
			return false
		}
		second, readErr := os.ReadFile(path) //nolint:gosec // path is constructed internally
		if readErr != nil || !bytes.Equal(first, second) {
			return false
		}
		return os.Remove(path) == nil
	}
	if time.Since(rec.HeartbeatAt) < leaseStaleAfter {
		return false
	}
	if ownerAlive(rec.PID, rec.CreateTime) {
		return false
	}

	second, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil || !bytes.Equal(first, second) {
		return false
	}
	return os.Remove(path) == nil
}

// ownerAlive reports whether the process that acquired a lease is still the
// same live process: the PID must currently be alive, and (when a creation
// time was recorded) its creation time must still match, so PID reuse by an
// unrelated process is not mistaken for the original owner.
func ownerAlive(pid int, createTime int64) bool {
	if pid <= 0 {
		return false
	}
	if !process.IsAlive(pid) {
		return false
	}
	return process.VerifyPID(pid, createTime)
}

// randomNonce returns a random hex string used to identify one lease
// acquisition, distinguishing it from any other acquisition of the same
// lock path (past or future).
func randomNonce() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// startLease constructs a Lease for a freshly-created lock file and starts
// its background heartbeat.
func startLease(path, nonce string) *Lease {
	l := &Lease{
		path:          path,
		nonce:         nonce,
		stopHeartbeat: make(chan struct{}),
		heartbeatDone: make(chan struct{}),
	}
	go l.heartbeatLoop()
	return l
}

// heartbeatLoop periodically renews the lease's heartbeat_at timestamp so
// that other waiters do not consider it stale while it is legitimately
// still held.
func (l *Lease) heartbeatLoop() {
	defer close(l.heartbeatDone)
	ticker := time.NewTicker(leaseHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopHeartbeat:
			return
		case <-ticker.C:
			l.renew()
		}
	}
}

// renew refreshes heartbeat_at on disk, but only while this lease still owns
// the file (nonce match), so a lease that was already reaped never
// resurrects a stale lock file out from under a new owner.
func (l *Lease) renew() {
	rec, ok := readLeaseRecord(l.path)
	if !ok || rec.Nonce != l.nonce {
		return
	}
	rec.HeartbeatAt = time.Now()
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_ = process.AtomicReplace(l.path, data, 0o600)
}

// safeLockName sanitizes a task ID for use as a per-task lock file name.
// Task IDs are normally generator-produced (see GenerateID) and already
// filesystem-safe, but this defends against unexpected characters ending up
// in a lock file name.
func safeLockName(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "task"
	}
	return b.String()
}

// LockContext acquires the store-wide advisory lock, waiting until ctx is
// done if it is currently held elsewhere. Used for operations that require
// atomicity across the whole store (e.g. name uniqueness checks).
//
// The lock file records the owner's PID, process creation time, a random
// nonce, and acquisition/heartbeat timestamps, so a lock abandoned by a
// crashed process can be safely reclaimed while a live lock is never
// released or reaped by anyone but its own owner.
func (s *Store) LockContext(ctx context.Context) (*Lease, error) {
	return acquireLease(ctx, filepath.Join(s.Root, ".lock"))
}

// LockTaskContext acquires an advisory lock scoped to a single task, so
// operations on independent tasks never block on each other. Per-task lock
// files live under procs/.locks/, outside any task directory, so they never
// appear as a task ID from ListIDs.
func (s *Store) LockTaskContext(ctx context.Context, id string) (*Lease, error) {
	path := filepath.Join(s.Root, ".locks", safeLockName(id)+".lock")
	return acquireLease(ctx, path)
}

// Lock acquires the store-wide advisory lock with a fixed ~5s retry budget,
// matching the timeout of the previous polling-based implementation.
//
// It exists for backward compatibility with existing callers that use the
// unlock-func style; new code should prefer LockContext so callers can
// control cancellation/timeout explicitly.
func (s *Store) Lock() (unlock func(), err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(lockRetries)*lockInterval)
	defer cancel()
	lease, err := s.LockContext(ctx)
	if err != nil {
		return nil, err
	}
	return lease.Unlock, nil
}
