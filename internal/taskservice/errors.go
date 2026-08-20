// Package taskservice provides the core business logic for managing
// bgtask background tasks, independent of any particular front end (CLI,
// HTTP, MCP). It owns name/label validation, task-directory-scoped process
// control, and the locking discipline required for safe concurrent access
// to the on-disk state.Store.
package taskservice

import (
	"context"
	"errors"
	"fmt"
)

// Code classifies a Service error so adapters (CLI, HTTP, MCP) can decide
// how to present or react to a failure without parsing message text.
type Code string

// Error codes returned by Service operations.
const (
	CodeInvalidArgument    Code = "invalid_argument"
	CodeNotFound           Code = "not_found"
	CodeConflict           Code = "conflict"
	CodeFailedPrecondition Code = "failed_precondition"
	CodeBusy               Code = "busy"
	CodeDeadlineExceeded   Code = "deadline_exceeded"
	CodeInternal           Code = "internal"
)

// Error is the typed error returned by every Service operation. It carries
// enough context for an adapter to build a good user-facing message (Op,
// Ref, TaskID) plus a machine-readable Code and Retryable hint, without
// exposing internal detail beyond the safe Message string. The original
// low-level cause, if any, is available via Cause/Unwrap for logging.
type Error struct {
	Code      Code
	Op        string // operation name, e.g. "run", "stop", "rename"
	Ref       string // the name/ID exactly as supplied by the caller
	TaskID    string // canonical resolved task ID, if known at the time of failure
	Message   string // safe, user-presentable message
	Retryable bool
	Cause     error
}

// Error implements the error interface with a safe, human-readable message.
func (e *Error) Error() string {
	switch {
	case e.Ref != "" && e.TaskID != "" && e.Ref != e.TaskID:
		return fmt.Sprintf("%s %s (%s): %s", e.Op, e.Ref, e.TaskID, e.Message)
	case e.Ref != "":
		return fmt.Sprintf("%s %s: %s", e.Op, e.Ref, e.Message)
	case e.TaskID != "":
		return fmt.Sprintf("%s %s: %s", e.Op, e.TaskID, e.Message)
	default:
		return fmt.Sprintf("%s: %s", e.Op, e.Message)
	}
}

// Unwrap exposes the wrapped cause, if any, for errors.Is/errors.As and
// diagnostic logging.
func (e *Error) Unwrap() error { return e.Cause }

// Is lets errors.Is(err, taskservice.ErrNotFound) (and the other Err*
// sentinels below) match any *Error sharing the same Code, without callers
// needing to compare message text.
func (e *Error) Is(target error) bool {
	var t *Error
	if errors.As(target, &t) {
		return e.Code == t.Code
	}
	return false
}

// Sentinel errors for use with errors.Is, one per Code. They carry no
// Op/Ref/Message of their own -- they only exist for code comparison via
// (*Error).Is.
var (
	ErrInvalidArgument    = &Error{Code: CodeInvalidArgument}
	ErrNotFound           = &Error{Code: CodeNotFound}
	ErrConflict           = &Error{Code: CodeConflict}
	ErrFailedPrecondition = &Error{Code: CodeFailedPrecondition}
	ErrBusy               = &Error{Code: CodeBusy}
	ErrDeadlineExceeded   = &Error{Code: CodeDeadlineExceeded}
	ErrInternal           = &Error{Code: CodeInternal}
)

func newError(code Code, op, ref, taskID, msg string, retryable bool, cause error) *Error {
	return &Error{Code: code, Op: op, Ref: ref, TaskID: taskID, Message: msg, Retryable: retryable, Cause: cause}
}

// InvalidArgument builds a CodeInvalidArgument error: the request itself is
// malformed (bad name, bad label, empty command, mutually exclusive flags).
func InvalidArgument(op, ref, taskID, msg string) *Error {
	return newError(CodeInvalidArgument, op, ref, taskID, msg, false, nil)
}

// NotFound builds a CodeNotFound error: no task matches the given ref.
func NotFound(op, ref, msg string) *Error {
	return newError(CodeNotFound, op, ref, "", msg, false, nil)
}

// Conflict builds a CodeConflict error: the request collides with existing
// state (duplicate name, ambiguous name matching multiple tasks).
func Conflict(op, ref, taskID, msg string) *Error {
	return newError(CodeConflict, op, ref, taskID, msg, false, nil)
}

// FailedPrecondition builds a CodeFailedPrecondition error: the task exists
// but is not in the state the operation requires (e.g. restart on a
// stopped task, start on an already-running task).
func FailedPrecondition(op, ref, taskID, msg string) *Error {
	return newError(CodeFailedPrecondition, op, ref, taskID, msg, false, nil)
}

// Busy builds a CodeBusy error: a lock could not be acquired because
// another operation currently holds it. Busy errors are retryable.
func Busy(op, ref, taskID, msg string, cause error) *Error {
	return newError(CodeBusy, op, ref, taskID, msg, true, cause)
}

// DeadlineExceededErr builds a CodeDeadlineExceeded error: the caller's
// context was already done (canceled or past its deadline) before the
// operation could make progress.
func DeadlineExceededErr(op, ref, taskID string, cause error) *Error {
	return newError(CodeDeadlineExceeded, op, ref, taskID, "operation canceled or timed out", true, cause)
}

// Internal builds a CodeInternal error for unexpected failures (I/O errors,
// corrupted state) that are not the caller's fault.
func Internal(op, ref, taskID string, cause error) *Error {
	return newError(CodeInternal, op, ref, taskID, "internal error", false, cause)
}

// CodeOf returns the Code of err if it is (or wraps) a *Error, or the zero
// Code otherwise. Adapters can use this instead of type-asserting directly.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// IsNotFound reports whether err is a *Error with CodeNotFound.
func IsNotFound(err error) bool { return CodeOf(err) == CodeNotFound }

// IsConflict reports whether err is a *Error with CodeConflict.
func IsConflict(err error) bool { return CodeOf(err) == CodeConflict }

// IsFailedPrecondition reports whether err is a *Error with CodeFailedPrecondition.
func IsFailedPrecondition(err error) bool { return CodeOf(err) == CodeFailedPrecondition }

// IsInvalidArgument reports whether err is a *Error with CodeInvalidArgument.
func IsInvalidArgument(err error) bool { return CodeOf(err) == CodeInvalidArgument }

// IsBusy reports whether err is a *Error with CodeBusy.
func IsBusy(err error) bool { return CodeOf(err) == CodeBusy }

// IsDeadlineExceeded reports whether err is a *Error with CodeDeadlineExceeded.
func IsDeadlineExceeded(err error) bool { return CodeOf(err) == CodeDeadlineExceeded }

// IsRetryable reports whether err is a *Error marked retryable.
func IsRetryable(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Retryable
	}
	return false
}

// checkContext returns a CodeDeadlineExceeded *Error if ctx is already
// done, or nil otherwise. Service methods call this up front so a caller
// that passed an already-canceled/expired context gets a typed error
// immediately instead of racing into filesystem operations.
func checkContext(op, ref string, ctx context.Context) *Error {
	if err := ctx.Err(); err != nil {
		return DeadlineExceededErr(op, ref, "", err)
	}
	return nil
}

// wrapLockErr converts a lock-acquisition failure into the right typed
// error, rather than blanket-classifying every failure as contention:
//
//   - the caller's own context was canceled or hit its deadline while
//     waiting -> CodeDeadlineExceeded (the caller gave up, we don't know
//     that anyone else held the lock);
//   - an internally-bounded wait (see Service.lockTaskBounded) expired
//     while another live holder kept the lock -> CodeBusy, retryable;
//   - anything else (failure to create the lock directory, entropy failure
//     when generating a nonce, an I/O error writing the lock file) is a
//     genuine internal fault, not contention -> CodeInternal.
func wrapLockErr(op, ref, taskID string, ctx context.Context, err error) *Error {
	if ctx != nil && ctx.Err() != nil {
		return DeadlineExceededErr(op, ref, taskID, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Busy(op, ref, taskID, "task is locked by another operation; try again", err)
	}
	return Internal(op, ref, taskID, err)
}
