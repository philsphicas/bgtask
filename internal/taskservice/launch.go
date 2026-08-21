package taskservice

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
)

// launchState classifies the terminal outcome of waiting for a freshly
// launched supervisor. Every value below means the launch *succeeded*; a
// launch that never produced any of them is reported as a typed error
// instead (see awaitLaunch).
type launchState int

const (
	// launchReady: the supervisor wrote its own PID to supervisor.pid and
	// that PID still refers to the process we launched.
	launchReady launchState = iota

	// launchExited: exit.json is already present, i.e. the command was
	// fast enough (or broken enough) to finish before we observed it
	// running. The launch itself still succeeded.
	launchExited

	// launchDisappeared: the task's state directory vanished while we were
	// waiting. This is the normal terminal outcome for an --rm (AutoRm)
	// task whose command finished immediately: the supervisor removes its
	// own state on exit. It is a success, not a missing task.
	launchDisappeared
)

// launchOutcome is the typed result of awaitLaunch.
type launchOutcome struct {
	State launchState

	// Exit is set when State == launchExited.
	Exit *state.Exit
}

// awaitLaunch waits, up to StartupReadyTimeout, for evidence that the
// supervisor launched with the given PID actually started. Callers must
// hold the task's lock across this wait so a concurrent Start/Run cannot
// launch a second supervisor for the same task in the readiness window.
//
// Evidence is accepted in this order, all of which mean "launched":
//
//   - exit.json exists: the command already ran to completion (a fast
//     command, or an immediately-broken one). Treating this as a readiness
//     failure would be wrong -- the supervisor did its job.
//   - the task directory is gone: an --rm task that already finished and
//     removed its own state (launchDisappeared, a typed terminal outcome).
//   - supervisor.pid contains the PID we launched, and that PID still has
//     the identity we launched (guarding against PID reuse).
//
// A supervisor that dies without leaving any of the above is an immediate
// launch failure (CodeInternal). Exhausting the bound, or the caller's
// context being canceled, yields CodeDeadlineExceeded.
func (s *Service) awaitLaunch(ctx context.Context, op, ref, id string, pid int) (launchOutcome, *Error) {
	interval := s.startupPollInterval()
	deadline := time.Now().Add(s.startupReadyTimeout())

	for {
		outcome, ok := s.checkLaunched(id, pid)
		if ok {
			return outcome, nil
		}

		// The supervisor is gone and left no evidence behind. Re-check once
		// more (it may have written exit.json between the two probes) and,
		// failing that, report an immediate launch failure.
		if pid > 0 && !s.Process.IsAlive(pid) {
			if outcome, ok := s.checkLaunched(id, pid); ok {
				return outcome, nil
			}
			return launchOutcome{}, newError(CodeInternal, op, ref, id,
				"supervisor exited before it could start the task", false, nil)
		}

		if err := ctx.Err(); err != nil {
			return launchOutcome{}, DeadlineExceededErr(op, ref, id, err)
		}
		if !time.Now().Before(deadline) {
			return launchOutcome{}, newError(CodeDeadlineExceeded, op, ref, id,
				"supervisor did not start within the startup timeout", true, nil)
		}

		select {
		case <-ctx.Done():
			return launchOutcome{}, DeadlineExceededErr(op, ref, id, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// checkLaunched performs one non-blocking probe for launch evidence. ok is
// false when nothing conclusive is on disk yet.
func (s *Service) checkLaunched(id string, pid int) (launchOutcome, bool) {
	if exit, err := s.Store.ReadExit(id); err == nil && exit != nil {
		return launchOutcome{State: launchExited, Exit: exit}, true
	}

	if _, err := s.Store.ReadMeta(id); errors.Is(err, fs.ErrNotExist) {
		return launchOutcome{State: launchDisappeared}, true
	}

	got, err := s.Store.ReadPID(id, "supervisor.pid")
	if err == nil && got == pid && pid > 0 && s.Process.VerifyPID(pid, s.Store.ReadCreateTime(id)) {
		return launchOutcome{State: launchReady}, true
	}

	return launchOutcome{}, false
}

// abandonLaunch cleans up after a launch that could not be confirmed: the
// supervisor (if it is somehow alive) is stopped, and the task state is
// removed, so a failed Run never leaves a half-created task behind.
//
// Like gracefulStop, this is destructive cleanup that takes no context: it
// must run to completion even when the originating request was canceled.
func (s *Service) abandonLaunch(id string, pid int) {
	if pid > 0 && s.Process.IsAlive(pid) {
		s.gracefulStop(s.Store.TaskDir(id), id, pid, s.stopTimeout(0))
	} else {
		s.killChildIfVerified(id)
	}
	_ = s.Store.Remove(id)
}
