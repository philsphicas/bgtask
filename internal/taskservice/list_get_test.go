package taskservice_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestList_FiltersByLabelOrSemantics(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "web", []string{"sleep", "100"})
	if _, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "web", Labels: []string{"frontend"}}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, svc, "api", []string{"sleep", "100"})
	if _, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "api", Labels: []string{"backend"}}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, svc, "db", []string{"sleep", "100"})

	result, err := svc.List(context.Background(), taskservice.ListRequest{Labels: []string{"frontend", "backend"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 matching tasks, got %d: %+v", len(result.Tasks), result.Tasks)
	}
}

func TestList_NoFilterReturnsAllSortedByID(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "z", []string{"sleep", "100"})
	mustRun(t, svc, "a", []string{"sleep", "100"})

	result, err := svc.List(context.Background(), taskservice.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}
	if result.Tasks[0].ID > result.Tasks[1].ID {
		t.Errorf("expected tasks sorted by ID, got %s then %s", result.Tasks[0].ID, result.Tasks[1].ID)
	}
}

func TestGet_UnknownRefIsNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Get(context.Background(), "does-not-exist")
	if !taskservice.IsNotFound(err) {
		t.Fatalf("expected CodeNotFound, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestGet_AmbiguousNameIsConflict(t *testing.T) {
	svc, _ := newTestService(t)
	// Force a name collision by writing meta.json directly (Run's own
	// uniqueness check would otherwise prevent this).
	for i := 0; i < 2; i++ {
		meta := &state.Meta{
			ID:        "manual-id-" + string(rune('a'+i)),
			Name:      "dup-name",
			Command:   []string{"sleep", "100"},
			Cwd:       ".",
			CreatedAt: time.Now(),
		}
		if err := svc.Store.Create(meta); err != nil {
			t.Fatal(err)
		}
	}

	_, err := svc.Get(context.Background(), "dup-name")
	if !taskservice.IsConflict(err) {
		t.Fatalf("expected CodeConflict for an ambiguous name, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestGet_ResolvesRunningStatus(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "running-task", []string{"sleep", "100"})

	got, err := svc.Get(context.Background(), "running-task")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Task.Status.State != "running" {
		t.Errorf("State = %q, want running", got.Task.Status.State)
	}
	if got.Task.Status.Running == nil || got.Task.Status.Running.SupervisorPID == 0 {
		t.Errorf("expected Running.SupervisorPID to be set, got %+v", got.Task.Status.Running)
	}
}

func TestGet_ResolvesDeadStatus(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "dead-task", []string{"sleep", "100"})
	markDead(env, created.PID)

	got, err := svc.Get(context.Background(), "dead-task")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Task.Status.State != "dead" {
		t.Errorf("State = %q, want dead", got.Task.Status.State)
	}
}

func TestGet_ResolvesUnknownStatus(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "unknown-task", []string{"sleep", "100"})

	// A task with no supervisor.pid at all (e.g. one whose supervisor
	// never got as far as recording its PID) resolves to "unknown", not
	// "dead" or "running".
	if err := os.Remove(filepath.Join(svc.Store.TaskDir(created.Task.ID), "supervisor.pid")); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(context.Background(), "unknown-task")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Task.Status.State != "unknown" {
		t.Errorf("State = %q, want unknown", got.Task.Status.State)
	}
}
