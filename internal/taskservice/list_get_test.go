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

func TestGet_StoreFailureIsInternal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := taskservice.New(&state.Store{Root: root})

	_, err := svc.Get(context.Background(), "missing")
	if taskservice.CodeOf(err) != taskservice.CodeInternal {
		t.Fatalf("expected CodeInternal for store failure, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

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

func TestList_FiltersStatesAndPaginatesNewestFirst(t *testing.T) {
	svc, _ := newTestService(t)
	for _, id := range []string{
		"20260101T000001-00000001",
		"20260101T000002-00000002",
		"20260101T000003-00000003",
	} {
		if err := svc.Store.Create(&state.Meta{ID: id, Name: id, Command: []string{"echo", id}, Cwd: ".", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := svc.List(context.Background(), taskservice.ListRequest{
		States: []string{"unknown"}, Limit: 2, NewestFirst: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 3 || len(first.Tasks) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, want total 3, 2 tasks, and cursor", first)
	}
	if first.Tasks[0].ID != "20260101T000003-00000003" {
		t.Fatalf("first task = %s, want newest ID", first.Tasks[0].ID)
	}

	second, err := svc.List(context.Background(), taskservice.ListRequest{
		States: []string{"unknown"}, Limit: 2, NewestFirst: true, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 3 || len(second.Tasks) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %+v, want total 3, 1 task, no cursor", second)
	}
	if second.Tasks[0].ID != "20260101T000001-00000001" {
		t.Fatalf("second-page task = %s, want oldest ID", second.Tasks[0].ID)
	}
}

func TestList_RejectsInvalidStateAndCursor(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.List(context.Background(), taskservice.ListRequest{States: []string{"RUNNING"}}); !taskservice.IsInvalidArgument(err) {
		t.Fatalf("invalid state error = %v, want invalid_argument", err)
	}
	if _, err := svc.List(context.Background(), taskservice.ListRequest{Cursor: "not-a-cursor"}); !taskservice.IsInvalidArgument(err) {
		t.Fatalf("invalid cursor error = %v, want invalid_argument", err)
	}
}

func TestList_OnlyScansPortsForReturnedPage(t *testing.T) {
	svc, env := newTestService(t)
	for _, name := range []string{"one", "two", "three"} {
		created := mustRun(t, svc, name, []string{"sleep", "100"})
		childPID := created.PID + 1000
		if err := svc.Store.WritePID(created.Task.ID, "child.pid", childPID); err != nil {
			t.Fatal(err)
		}
		env.ports[childPID] = []uint32{8080}
	}
	before := env.portCallCount()
	result, err := svc.List(context.Background(), taskservice.ListRequest{States: []string{"running"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(result.Tasks))
	}
	if got := env.portCallCount() - before; got != 1 {
		t.Fatalf("ListeningPorts calls = %d, want 1", got)
	}
}
