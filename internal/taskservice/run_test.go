package taskservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestRun_AutoNameAndLaunch(t *testing.T) {
	svc, env := newTestService(t)
	result := mustRun(t, svc, "", []string{"sleep", "100"})

	if result.Task.Meta.Name == "" {
		t.Fatal("expected an auto-generated name")
	}
	if result.PID == 0 {
		t.Fatal("expected a non-zero PID")
	}
	if env.launchCount() != 1 {
		t.Fatalf("launches = %d, want 1", env.launchCount())
	}
	if result.Task.Status.State != "running" {
		t.Fatalf("status.State = %q, want running", result.Task.Status.State)
	}
}

func TestRun_InvalidCommandIsInvalidArgument(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Run(context.Background(), taskservice.RunRequest{Name: "x"})
	if !taskservice.IsInvalidArgument(err) {
		t.Fatalf("expected CodeInvalidArgument, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestRun_InvalidNameIsInvalidArgument(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Run(context.Background(), taskservice.RunRequest{Name: "../evil", Command: []string{"true"}})
	if !taskservice.IsInvalidArgument(err) {
		t.Fatalf("expected CodeInvalidArgument for a path-separator name, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestRun_InvalidLabelIsInvalidArgument(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Run(context.Background(), taskservice.RunRequest{
		Name: "x", Command: []string{"true"}, Labels: []string{"123-bad"},
	})
	if !taskservice.IsInvalidArgument(err) {
		t.Fatalf("expected CodeInvalidArgument for an invalid label, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestRun_InvalidLifecycleConfigurationIsInvalidArgument(t *testing.T) {
	tests := []struct {
		name   string
		update func(*taskservice.RunRequest)
	}{
		{
			name: "restart policy",
			update: func(req *taskservice.RunRequest) {
				req.Restart = "unless-stopped"
			},
		},
		{
			name: "negative restart delay",
			update: func(req *taskservice.RunRequest) {
				req.RestartDelay = -time.Second
			},
		},
		{
			name: "negative health interval",
			update: func(req *taskservice.RunRequest) {
				req.HealthInterval = -time.Second
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			req := taskservice.RunRequest{Name: "invalid-config", Command: []string{"true"}}
			tt.update(&req)

			_, err := svc.Run(context.Background(), req)
			if !taskservice.IsInvalidArgument(err) {
				t.Fatalf("expected CodeInvalidArgument, got %v (code=%s)", err, taskservice.CodeOf(err))
			}
		})
	}
}

func TestRun_DuplicateNameDefaultIsConflict(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "dup", []string{"sleep", "100"})

	_, err := svc.Run(context.Background(), taskservice.RunRequest{
		Name: "dup", Command: []string{"sleep", "100"}, ReplaceExisting: false,
	})
	if !taskservice.IsConflict(err) {
		t.Fatalf("expected CodeConflict for a duplicate name, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestRun_DuplicateNameWithReplaceExistingReplaces(t *testing.T) {
	svc, env := newTestService(t)
	first := mustRun(t, svc, "dup", []string{"sleep", "100"})

	result, err := svc.Run(context.Background(), taskservice.RunRequest{
		Name: "dup", Command: []string{"sleep", "200"}, ReplaceExisting: true,
	})
	if err != nil {
		t.Fatalf("Run with ReplaceExisting: %v", err)
	}
	if !result.ReplacedExisting {
		t.Error("expected ReplacedExisting = true")
	}
	if result.Task.ID == first.Task.ID {
		t.Error("expected a new task ID after replacement")
	}
	if env.launchCount() != 2 {
		t.Fatalf("launches = %d, want 2 (original + replacement)", env.launchCount())
	}

	// The old task's ID should no longer resolve (it was removed).
	if _, _, err := svc.Store.Resolve(first.Task.ID); err == nil {
		t.Error("expected the replaced task's old ID to be gone")
	}
}

func TestRun_ImmediateExitIsReported(t *testing.T) {
	svc, _ := newTestService(t)
	result := mustRun(t, svc, "immediate", []string{"false"})

	// Simulate the supervisor writing exit.json almost instantly (e.g. a
	// bad command/typo). In production this races with Run's brief
	// StartupCheckDelay; here we write it directly and re-fetch via Get to
	// exercise the same status-resolution path deterministically.
	exit := &state.Exit{Code: 1, ExitedAt: time.Now()}
	if err := svc.Store.WriteExit(result.Task.ID, exit); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}

	got, err := svc.Get(context.Background(), result.Task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Task.Status.State != "exited" || got.Task.Status.Exited.Code != 1 {
		t.Fatalf("expected exited(1) status, got %+v", got.Task.Status)
	}
}
