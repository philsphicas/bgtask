package taskservice

import (
	"context"
	"fmt"

	"github.com/philsphicas/bgtask/internal/validation"
)

// Rename changes a task's name, enforcing name validation and store-wide
// uniqueness.
//
// Locking: the store-wide lock is acquired first (to make the uniqueness
// check atomic with the eventual write), then the per-task lock, then meta
// is re-read once more before writing -- guarding against a concurrent
// mutation (e.g. a concurrent SetLabels) landing between resolve and lock
// acquisition.
func (s *Service) Rename(ctx context.Context, req RenameRequest) (*OperationResult, error) {
	const op = "rename"
	if cerr := checkContext(op, req.Ref, ctx); cerr != nil {
		return nil, cerr
	}
	if err := validation.ValidateName(req.NewName); err != nil {
		return nil, InvalidArgument(op, req.Ref, "", err.Error())
	}

	global, err := s.Store.LockContext(ctx)
	if err != nil {
		return nil, wrapLockErr(op, req.Ref, "", ctx, err)
	}
	defer global.Unlock()

	id, _, err := s.resolve(op, req.Ref)
	if err != nil {
		return nil, err
	}

	// Bounded: this nested acquisition happens while the store-wide lock is
	// held, so a stuck per-task lock must not pin the global lock.
	taskLease, err := s.lockTaskBounded(ctx, id)
	if err != nil {
		return nil, wrapLockErr(op, req.Ref, id, ctx, err)
	}
	defer taskLease.Unlock()

	// Re-read after acquiring the task lock: another operation may have
	// mutated (or removed) this task between resolve and lock acquisition.
	meta, err := s.Store.ReadMeta(id)
	if err != nil {
		return nil, NotFound(op, req.Ref, "task no longer exists")
	}

	if meta.Name == req.NewName {
		return &OperationResult{NoOp: true, TaskID: id, Name: meta.Name, Labels: meta.Labels}, nil
	}

	if taken, err := s.Store.IsNameTaken(req.NewName); err != nil {
		return nil, Internal(op, req.Ref, id, err)
	} else if taken {
		return nil, Conflict(op, req.Ref, id, fmt.Sprintf("name %q is already in use", req.NewName))
	}

	if err := s.Store.Rename(id, req.NewName); err != nil {
		return nil, Internal(op, req.Ref, id, err)
	}

	return &OperationResult{Changed: true, TaskID: id, Name: req.NewName, Labels: meta.Labels}, nil
}

// SetLabels replaces the labels on a task, enforcing label validation.
//
// Locking: the per-task lock is held across a re-read of meta.json and the
// write, so a concurrent Rename or SetLabels on the same task can't race.
func (s *Service) SetLabels(ctx context.Context, req SetLabelsRequest) (*OperationResult, error) {
	const op = "set_labels"
	if cerr := checkContext(op, req.Ref, ctx); cerr != nil {
		return nil, cerr
	}
	if err := validation.ValidateLabels(req.Labels); err != nil {
		return nil, InvalidArgument(op, req.Ref, "", err.Error())
	}

	id, _, err := s.resolve(op, req.Ref)
	if err != nil {
		return nil, err
	}

	lease, err := s.Store.LockTaskContext(ctx, id)
	if err != nil {
		return nil, wrapLockErr(op, req.Ref, id, ctx, err)
	}
	defer lease.Unlock()

	meta, err := s.Store.ReadMeta(id)
	if err != nil {
		return nil, NotFound(op, req.Ref, "task no longer exists")
	}

	if err := s.Store.SetLabels(id, req.Labels); err != nil {
		return nil, Internal(op, req.Ref, id, err)
	}

	return &OperationResult{Changed: true, TaskID: id, Name: meta.Name, Labels: req.Labels}, nil
}
