package taskservice

import (
	"context"
	"time"
)

// runBatch drives the common bulk-operation shape shared by Stop, Restart,
// Start, and Remove:
//
//   - an explicit Selection.Names list is processed one ref at a time, in
//     order, stopping at (and returning) the first error -- "fail-fast",
//     matching the CLI's historical behavior for e.g. `bgtask stop a b c`.
//   - a Labels/All selection is resolved to a sorted ID snapshot up front
//     (task-at-a-time, deterministic order) and processed best-effort: a
//     failure on one task never stops the others, and the overall call
//     returns a nil error (individual failures are reported per-item).
func (s *Service) runBatch(ctx context.Context, op string, sel Selection, do func(ctx context.Context, ref string) BatchItem) (*BatchResult, error) {
	if cerr := checkContext(op, "", ctx); cerr != nil {
		return nil, cerr
	}

	result := &BatchResult{}

	if sel.explicit() {
		for _, ref := range sel.Names {
			item := do(ctx, ref)
			result.Items = append(result.Items, item)
			if item.Err != nil {
				return result, item.Err
			}
		}
		return result, nil
	}

	ids, err := s.snapshotIDs(op, sel)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		item := do(ctx, id)
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// Stop stops one or more tasks. A task that is already stopped is reported
// as OperationResult.NoOp (not an error).
func (s *Service) Stop(ctx context.Context, req StopRequest) (*BatchResult, error) {
	const op = "stop"
	timeout := s.stopTimeout(req.Timeout)
	return s.runBatch(ctx, op, req.Selection, func(ctx context.Context, ref string) BatchItem {
		return s.stopByRef(ctx, ref, req.Force, timeout)
	})
}

func (s *Service) stopByRef(ctx context.Context, ref string, force bool, timeout time.Duration) BatchItem {
	const op = "stop"
	id, _, err := s.resolve(op, ref)
	if err != nil {
		return BatchItem{Ref: ref, Err: err}
	}

	lease, err := s.Store.LockTaskContext(ctx, id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Err: wrapLockErr(op, ref, id, ctx, err)}
	}
	defer lease.Unlock()

	meta, err := s.Store.ReadMeta(id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Err: NotFound(op, ref, "task no longer exists")}
	}
	task := s.toTask(id, meta)

	pid, alive := s.verifyAndGetPID(id)
	if !alive {
		return BatchItem{Ref: ref, TaskID: id, Task: &task, Result: OperationResult{NoOp: true}}
	}

	forced := false
	if force {
		_ = s.Process.SignalKill(pid)
		s.killChildIfVerified(id)
		forced = true
	} else {
		forced = s.gracefulStop(s.Store.TaskDir(id), id, pid, timeout)
	}

	task = s.toTask(id, meta)
	return BatchItem{Ref: ref, TaskID: id, Task: &task, Result: OperationResult{Changed: true, Forced: forced}}
}

// Restart restarts one or more running tasks. A task that is not running
// is reported as a CodeFailedPrecondition error (matching the CLI's
// historical behavior of treating "restart a stopped task" as a hard
// error, unlike Stop's "already stopped" no-op).
func (s *Service) Restart(ctx context.Context, req RestartRequest) (*BatchResult, error) {
	const op = "restart"
	return s.runBatch(ctx, op, req.Selection, func(ctx context.Context, ref string) BatchItem {
		return s.restartByRef(ctx, ref, req.Force)
	})
}

func (s *Service) restartByRef(ctx context.Context, ref string, force bool) BatchItem {
	const op = "restart"
	id, _, err := s.resolve(op, ref)
	if err != nil {
		return BatchItem{Ref: ref, Err: err}
	}

	lease, err := s.Store.LockTaskContext(ctx, id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Err: wrapLockErr(op, ref, id, ctx, err)}
	}
	defer lease.Unlock()

	meta, err := s.Store.ReadMeta(id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Err: NotFound(op, ref, "task no longer exists")}
	}
	task := s.toTask(id, meta)

	pid, alive := s.verifyAndGetPID(id)
	if !alive {
		return BatchItem{Ref: ref, TaskID: id, Task: &task, Err: FailedPrecondition(op, ref, id, "task is not running")}
	}

	if force {
		s.killChildIfVerified(id)
	}
	if err := s.Process.SignalRestart(s.Store.TaskDir(id), pid); err != nil {
		return BatchItem{Ref: ref, TaskID: id, Task: &task, Err: Internal(op, ref, id, err)}
	}
	// Give the supervisor a brief moment to act on the signal before
	// returning, matching the CLI's historical behavior.
	time.Sleep(200 * time.Millisecond)

	return BatchItem{Ref: ref, TaskID: id, Task: &task, Result: OperationResult{Changed: true, Forced: force}}
}

// Start (re-)launches one or more stopped tasks. A task that is already
// running is reported as a CodeFailedPrecondition error.
func (s *Service) Start(ctx context.Context, req StartRequest) (*BatchResult, error) {
	const op = "start"
	return s.runBatch(ctx, op, req.Selection, s.startByRef)
}

// startByRef holds the per-task lock through launch readiness -- i.e.
// until the freshly launched supervisor has actually signaled that it
// started -- so two concurrent Start calls (or a concurrent Run replacing
// the same name) can never race into spawning duplicate supervisors for
// one task.
func (s *Service) startByRef(ctx context.Context, ref string) BatchItem {
	const op = "start"
	id, _, err := s.resolve(op, ref)
	if err != nil {
		return BatchItem{Ref: ref, Err: err}
	}

	lease, err := s.Store.LockTaskContext(ctx, id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Err: wrapLockErr(op, ref, id, ctx, err)}
	}
	defer lease.Unlock()

	meta, err := s.Store.ReadMeta(id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Err: NotFound(op, ref, "task no longer exists")}
	}
	task := s.toTask(id, meta)

	if _, alive := s.verifyAndGetPID(id); alive {
		return BatchItem{Ref: ref, TaskID: id, Task: &task, Err: FailedPrecondition(op, ref, id, "task is already running")}
	}

	// Kill any orphaned child left behind by a dead supervisor.
	s.killChildIfVerified(id)

	if err := s.Store.ClearExit(id); err != nil {
		return BatchItem{Ref: ref, TaskID: id, Task: &task, Err: Internal(op, ref, id, err)}
	}

	pid, noBreakaway, err := s.Launcher.Launch(ctx, s.Store.Root, id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Task: &task, Err: Internal(op, ref, id, err)}
	}

	// The lease is still held here: readiness is part of the operation, not
	// something the caller is left to poll for.
	outcome, aerr := s.awaitLaunch(ctx, op, ref, id, pid)
	if aerr != nil {
		return BatchItem{Ref: ref, TaskID: id, Task: &task, Err: aerr}
	}

	// Refresh the reported status now that the task is actually up (or has
	// already exited); an --rm task may have removed itself entirely, in
	// which case the pre-launch snapshot is the best we can report.
	if outcome.State != launchDisappeared {
		if fresh, ferr := s.Store.ReadMeta(id); ferr == nil {
			task = s.toTask(id, fresh)
		}
	}

	return BatchItem{
		Ref: ref, TaskID: id, Task: &task,
		Result: OperationResult{
			Changed:     true,
			PID:         pid,
			NoBreakaway: noBreakaway,
			AutoRemoved: outcome.State == launchDisappeared,
		},
	}
}

// Remove stops (if running) and deletes one or more tasks.
func (s *Service) Remove(ctx context.Context, req RemoveRequest) (*BatchResult, error) {
	const op = "remove"
	timeout := s.stopTimeout(req.Timeout)
	return s.runBatch(ctx, op, req.Selection, func(ctx context.Context, ref string) BatchItem {
		return s.removeByRef(ctx, ref, req.Force, timeout)
	})
}

func (s *Service) removeByRef(ctx context.Context, ref string, force bool, timeout time.Duration) BatchItem {
	const op = "remove"
	id, _, err := s.resolve(op, ref)
	if err != nil {
		return BatchItem{Ref: ref, Err: err}
	}

	lease, err := s.Store.LockTaskContext(ctx, id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Err: wrapLockErr(op, ref, id, ctx, err)}
	}
	defer lease.Unlock()

	meta, err := s.Store.ReadMeta(id)
	if err != nil {
		return BatchItem{Ref: ref, TaskID: id, Err: NotFound(op, ref, "task no longer exists")}
	}
	task := s.toTask(id, meta)

	pid, alive := s.verifyAndGetPID(id)
	forced := false
	if alive {
		if force {
			_ = s.Process.SignalKill(pid)
			s.killChildIfVerified(id)
			forced = true
		} else {
			forced = s.gracefulStop(s.Store.TaskDir(id), id, pid, timeout)
		}
	}

	if err := s.Store.Remove(id); err != nil {
		return BatchItem{Ref: ref, TaskID: id, Task: &task, Err: Internal(op, ref, id, err)}
	}

	return BatchItem{Ref: ref, TaskID: id, Task: &task, Result: OperationResult{Changed: true, Forced: forced}}
}

// Cleanup removes state for all non-running tasks. Running tasks are left
// alone (reported as OperationResult.NoOp, not an error). Cleanup always
// processes a best-effort, task-at-a-time snapshot of every task; it has
// no Selection since it inherently targets everything.
func (s *Service) Cleanup(ctx context.Context, _ CleanupRequest) (*BatchResult, error) {
	const op = "cleanup"
	return s.runBatch(ctx, op, Selection{All: true}, s.cleanupByID)
}

func (s *Service) cleanupByID(ctx context.Context, id string) BatchItem {
	const op = "cleanup"
	lease, err := s.Store.LockTaskContext(ctx, id)
	if err != nil {
		return BatchItem{Ref: id, TaskID: id, Err: wrapLockErr(op, id, id, ctx, err)}
	}
	defer lease.Unlock()

	meta, err := s.Store.ReadMeta(id)
	if err != nil {
		// Tolerated: disappeared between the snapshot and this task's turn.
		return BatchItem{Ref: id, TaskID: id, Result: OperationResult{NoOp: true}}
	}
	task := s.toTask(id, meta)

	if _, alive := s.verifyAndGetPID(id); alive {
		return BatchItem{Ref: id, TaskID: id, Task: &task, Result: OperationResult{NoOp: true}}
	}

	if err := s.Store.Remove(id); err != nil {
		return BatchItem{Ref: id, TaskID: id, Task: &task, Err: Internal(op, id, id, err)}
	}

	return BatchItem{Ref: id, TaskID: id, Task: &task, Result: OperationResult{Changed: true}}
}
