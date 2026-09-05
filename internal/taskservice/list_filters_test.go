package taskservice_test

import (
	"context"
	"testing"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestList_CursorBindsNormalizedFilters(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	for _, name := range []string{"first", "second", "third"} {
		created := mustRun(t, svc, name, []string{"sleep", "100"})
		if _, err := svc.SetLabels(ctx, taskservice.SetLabelsRequest{Ref: created.Task.ID, Labels: []string{"dev", "api"}}); err != nil {
			t.Fatal(err)
		}
	}
	req := taskservice.ListRequest{Limit: 1, Labels: []string{"dev", "api"}, States: []string{"running", "exited"}}
	first, err := svc.List(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("expected continuation cursor")
	}
	req.Cursor = first.NextCursor
	for _, changed := range []taskservice.ListRequest{
		{Limit: 1, Cursor: req.Cursor, Labels: []string{"dev"}, States: req.States},
		{Limit: 1, Cursor: req.Cursor, Labels: req.Labels, States: []string{"running"}},
		{Limit: 1, Cursor: req.Cursor, Labels: req.Labels, States: req.States, NewestFirst: true},
	} {
		if _, err := svc.List(ctx, changed); !taskservice.IsInvalidArgument(err) {
			t.Fatalf("changed filters/order must reject cursor: %v", err)
		}
	}
	req.Labels = []string{"api", "dev", "api"}
	req.States = []string{"exited", "running", "running"}
	req.Limit = 2
	next, err := svc.List(ctx, req)
	if err != nil {
		t.Fatalf("equivalent filters should accept cursor: %v", err)
	}
	if next.Total != 3 || len(next.Tasks) != 2 || next.Tasks[0].ID == first.Tasks[0].ID || next.NextCursor != "" {
		t.Fatalf("unexpected continuation: %+v", next)
	}
}

func TestList_UnboundedFiltersOnlyScanReturnedPorts(t *testing.T) {
	svc, env := newTestService(t)
	created := mustRun(t, svc, "running", []string{"sleep", "100"})
	if err := svc.Store.WritePID(created.Task.ID, "child.pid", created.PID+1000); err != nil {
		t.Fatal(err)
	}
	before := env.portCallCount()
	result, err := svc.List(context.Background(), taskservice.ListRequest{States: []string{"exited"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 0 || env.portCallCount() != before {
		t.Fatal("filtered-out running task triggered port discovery")
	}
	result, err = svc.List(context.Background(), taskservice.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 1 || env.portCallCount()-before != 1 {
		t.Fatal("unfiltered legacy list must still discover ports")
	}
}
