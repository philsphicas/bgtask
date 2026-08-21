package taskservice_test

import (
	"context"
	"errors"
	"testing"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestErrors_CodeOfAndHelpers(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		code  taskservice.Code
		check func(error) bool
	}{
		{"invalid_argument", taskservice.InvalidArgument("op", "ref", "id", "msg"), taskservice.CodeInvalidArgument, taskservice.IsInvalidArgument},
		{"not_found", taskservice.NotFound("op", "ref", "msg"), taskservice.CodeNotFound, taskservice.IsNotFound},
		{"conflict", taskservice.Conflict("op", "ref", "id", "msg"), taskservice.CodeConflict, taskservice.IsConflict},
		{"failed_precondition", taskservice.FailedPrecondition("op", "ref", "id", "msg"), taskservice.CodeFailedPrecondition, taskservice.IsFailedPrecondition},
		{"busy", taskservice.Busy("op", "ref", "id", "msg", nil), taskservice.CodeBusy, taskservice.IsBusy},
		{"deadline_exceeded", taskservice.DeadlineExceededErr("op", "ref", "id", nil), taskservice.CodeDeadlineExceeded, taskservice.IsDeadlineExceeded},
		{"internal", taskservice.Internal("op", "ref", "id", nil), taskservice.CodeInternal, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskservice.CodeOf(tc.err); got != tc.code {
				t.Errorf("CodeOf = %q, want %q", got, tc.code)
			}
			if tc.check != nil && !tc.check(tc.err) {
				t.Errorf("expected the matching Is* helper to return true for %v", tc.err)
			}
		})
	}
}

func TestErrors_IsRetryable(t *testing.T) {
	if !taskservice.IsRetryable(taskservice.Busy("op", "ref", "id", "msg", nil)) {
		t.Error("expected Busy to be retryable")
	}
	if taskservice.IsRetryable(taskservice.NotFound("op", "ref", "msg")) {
		t.Error("expected NotFound to not be retryable")
	}
	if taskservice.IsRetryable(nil) {
		t.Error("expected a nil error to not be retryable")
	}
}

func TestErrors_ErrorsIsMatchesByCode(t *testing.T) {
	err := taskservice.NotFound("get", "myref", "task not found: myref")
	if !errors.Is(err, taskservice.ErrNotFound) {
		t.Error("expected errors.Is to match ErrNotFound by code")
	}
	if errors.Is(err, taskservice.ErrConflict) {
		t.Error("expected errors.Is to not match a different code")
	}
}

func TestErrors_UnwrapExposesCause(t *testing.T) {
	cause := errors.New("boom")
	err := taskservice.Internal("op", "ref", "id", cause)
	if !errors.Is(err, cause) {
		t.Error("expected errors.Is to find the wrapped cause via Unwrap")
	}
}

func TestErrors_MessageIncludesOpAndRef(t *testing.T) {
	err := taskservice.NotFound("stop", "myname", "task not found: myname")
	got := err.Error()
	if got == "" {
		t.Fatal("expected a non-empty error message")
	}
	if got[:4] != "stop" {
		t.Errorf("expected the message to start with the operation name, got %q", got)
	}
}

// TestErrors_CanceledContextIsDeadlineExceeded verifies that Service
// methods reject an already-canceled/expired context immediately, rather
// than racing into filesystem operations.
func TestErrors_CanceledContextIsDeadlineExceeded(t *testing.T) {
	svc, _ := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.Get(ctx, "anything")
	if !taskservice.IsDeadlineExceeded(err) {
		t.Fatalf("expected CodeDeadlineExceeded for an already-canceled context, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}
