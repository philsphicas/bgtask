package taskservice

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/validation"
)

// Run launches a new task. If a task with the same name already exists,
// req.ReplaceExisting decides whether it is stopped and replaced (true) or
// the call fails with a CodeConflict error (false, the default).
//
// Locking discipline (strictly global -> per-task, the same order Rename
// uses, so the two can never deadlock against each other):
//
//  1. the store-wide lock is taken first and held across the whole
//     name-uniqueness decision: the replaced task's stop/removal, the new
//     task's Create, and the new task's lock acquisition. No other
//     operation can observe (or create) the name in between.
//  2. the *old* task's lock is taken (bounded, see lockTaskBounded) before
//     anything destructive happens to it, and its meta is re-read under
//     that lock -- so a concurrent Stop/Rename/Remove of the same task can
//     neither be interleaved with, nor invalidated by, this replacement.
//  3. the *new* task's lock is taken before the global lock is released
//     and held through launch readiness, so a concurrent Start on the
//     brand-new task cannot spawn a duplicate supervisor.
func (s *Service) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	const op = "run"
	if cerr := checkContext(op, req.Name, ctx); cerr != nil {
		return nil, cerr
	}
	if len(req.Command) == 0 {
		return nil, InvalidArgument(op, req.Name, "", "command must not be empty")
	}

	name := req.Name
	if name == "" {
		name = state.AutoName(req.Command)
	}
	if err := validation.ValidateName(name); err != nil {
		return nil, InvalidArgument(op, name, "", err.Error())
	}
	if err := validation.ValidateLabels(req.Labels); err != nil {
		return nil, InvalidArgument(op, name, "", err.Error())
	}
	switch req.Restart {
	case "", "always", "on-failure":
	default:
		return nil, InvalidArgument(op, name, "", fmt.Sprintf("invalid restart policy %q (expected always or on-failure)", req.Restart))
	}
	if req.RestartDelay < 0 {
		return nil, InvalidArgument(op, name, "", "restart delay must not be negative")
	}
	if req.HealthInterval < 0 {
		return nil, InvalidArgument(op, name, "", "health interval must not be negative")
	}

	global, err := s.Store.LockContext(ctx)
	if err != nil {
		return nil, wrapLockErr(op, name, "", ctx, err)
	}
	globalHeld := true
	releaseGlobal := func() {
		if globalHeld {
			globalHeld = false
			global.Unlock()
		}
	}
	defer releaseGlobal()

	replacedExisting, rerr := s.replaceExisting(ctx, op, name, req.ReplaceExisting)
	if rerr != nil {
		return nil, rerr
	}

	meta := &state.Meta{
		ID:             state.GenerateID(),
		Name:           name,
		Command:        req.Command,
		Cwd:            req.Cwd,
		EnvOverrides:   req.EnvOverrides,
		Labels:         req.Labels,
		Restart:        req.Restart,
		RestartDelay:   req.RestartDelay,
		HealthCheck:    req.HealthCheck,
		HealthInterval: req.HealthInterval,
		AutoRm:         req.AutoRm,
		CreatedAt:      time.Now(),
	}
	if err := s.Store.Create(meta); err != nil {
		return nil, Internal(op, name, meta.ID, err)
	}

	// Take the new task's lock *before* dropping the store-wide lock: from
	// here until launch readiness is confirmed, no concurrent Start can
	// launch a second supervisor for this task.
	taskLease, err := s.lockTaskBounded(ctx, meta.ID)
	if err != nil {
		_ = s.Store.Remove(meta.ID)
		return nil, wrapLockErr(op, name, meta.ID, ctx, err)
	}
	defer taskLease.Unlock()

	// The name is now reserved by the new task directory; launching does
	// not need store-wide atomicity.
	releaseGlobal()

	pid, noBreakaway, err := s.Launcher.Launch(ctx, s.Store.Root, meta.ID)
	if err != nil {
		_ = s.Store.Remove(meta.ID)
		return nil, Internal(op, name, meta.ID, err)
	}

	outcome, aerr := s.awaitLaunch(ctx, op, name, meta.ID, pid)
	if aerr != nil {
		s.abandonLaunch(meta.ID, pid)
		return nil, aerr
	}

	exit := outcome.Exit
	if outcome.State == launchReady && s.StartupCheckDelay > 0 {
		// Settle briefly so an immediately-broken command (typo, missing
		// binary) is reported as an immediate exit rather than as a
		// running task that dies a moment later.
		time.Sleep(s.StartupCheckDelay)
		exit, _ = s.Store.ReadExit(meta.ID)
	}

	return &RunResult{
		Task:             s.toTask(meta.ID, meta),
		PID:              pid,
		NoBreakaway:      noBreakaway,
		ReplacedExisting: replacedExisting,
		ImmediateExit:    exit,
		AutoRemoved:      outcome.State == launchDisappeared,
	}, nil
}

// replaceExisting handles Run's duplicate-name path. It must be called with
// the store-wide lock held, and returns with it still held.
//
// The existing task is locked (bounded) and its meta re-read under that
// lock before anything destructive happens, so the decision to replace is
// made against state that cannot change underneath us. The stop and the
// state removal are bounded and run to completion even if ctx is canceled
// mid-stop -- abandoning a half-stopped supervisor would orphan it.
func (s *Service) replaceExisting(ctx context.Context, op, name string, allowed bool) (bool, error) {
	taken, err := s.Store.IsNameTaken(name)
	if err != nil {
		return false, Internal(op, name, "", err)
	}
	if !taken {
		return false, nil
	}
	if !allowed {
		return false, Conflict(op, name, "", fmt.Sprintf("name %q is already in use", name))
	}

	existingID, _, err := s.Store.Resolve(name)
	if err != nil {
		if errors.Is(err, state.ErrAmbiguousName) {
			return false, Conflict(op, name, "", err.Error())
		}
		if errors.Is(err, state.ErrTaskNotFound) {
			// Vanished between IsNameTaken and Resolve: nothing to replace.
			return false, nil
		}
		return false, Internal(op, name, "", err)
	}

	lease, err := s.lockTaskBounded(ctx, existingID)
	if err != nil {
		return false, wrapLockErr(op, name, existingID, ctx, err)
	}
	// Unlocking also deletes the per-task lock file, so a replaced-and-
	// removed task leaves no stale lock state behind.
	defer lease.Unlock()

	// Re-read under the task lock: the task may have been removed or
	// renamed away between Resolve and lock acquisition, in which case
	// there is nothing left to replace.
	meta, err := s.Store.ReadMeta(existingID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, Internal(op, name, existingID, err)
	}
	if meta.Name != name {
		return false, nil
	}

	if pid, alive := s.verifyAndGetPID(existingID); alive {
		s.gracefulStop(s.Store.TaskDir(existingID), existingID, pid, s.stopTimeout(0))
	} else {
		// The supervisor is gone; make sure it did not orphan its child.
		s.killChildIfVerified(existingID)
	}
	if err := s.Store.Remove(existingID); err != nil {
		return false, Internal(op, name, existingID, err)
	}
	return true, nil
}
