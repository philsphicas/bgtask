package taskservice_test

import (
	"context"
	"testing"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestRename_Success(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "old-name", []string{"sleep", "100"})

	res, err := svc.Rename(context.Background(), taskservice.RenameRequest{Ref: "old-name", NewName: "new-name"})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if !res.Changed {
		t.Error("expected Changed = true")
	}

	got, err := svc.Get(context.Background(), created.Task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Task.Meta.Name != "new-name" {
		t.Errorf("Name = %q, want %q", got.Task.Meta.Name, "new-name")
	}

	// The old name must no longer resolve.
	if _, err := svc.Get(context.Background(), "old-name"); !taskservice.IsNotFound(err) {
		t.Errorf("expected old name to be NotFound, got %v", err)
	}
}

func TestRename_SameNameIsNoOp(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "same", []string{"sleep", "100"})

	res, err := svc.Rename(context.Background(), taskservice.RenameRequest{Ref: "same", NewName: "same"})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if !res.NoOp || res.Changed {
		t.Errorf("expected NoOp result, got %+v", res)
	}
}

func TestRename_ToTakenNameIsConflict(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "a", []string{"sleep", "100"})
	mustRun(t, svc, "b", []string{"sleep", "100"})

	_, err := svc.Rename(context.Background(), taskservice.RenameRequest{Ref: "a", NewName: "b"})
	if !taskservice.IsConflict(err) {
		t.Fatalf("expected CodeConflict, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestRename_InvalidNewNameIsInvalidArgument(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "a", []string{"sleep", "100"})

	_, err := svc.Rename(context.Background(), taskservice.RenameRequest{Ref: "a", NewName: "../etc"})
	if !taskservice.IsInvalidArgument(err) {
		t.Fatalf("expected CodeInvalidArgument, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestRename_UnknownRefIsNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Rename(context.Background(), taskservice.RenameRequest{Ref: "nope", NewName: "whatever"})
	if !taskservice.IsNotFound(err) {
		t.Fatalf("expected CodeNotFound, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestSetLabels_ReplacesAndClears(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "labeled", []string{"sleep", "100"})

	if _, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{
		Ref: "labeled", Labels: []string{"dev", "api"},
	}); err != nil {
		t.Fatalf("SetLabels: %v", err)
	}
	got, err := svc.Get(context.Background(), created.Task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Task.Meta.Labels) != 2 {
		t.Fatalf("Labels = %v, want 2 entries", got.Task.Meta.Labels)
	}

	// Clearing: an empty slice replaces the existing labels.
	if _, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "labeled", Labels: nil}); err != nil {
		t.Fatalf("SetLabels (clear): %v", err)
	}
	got, err = svc.Get(context.Background(), created.Task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Task.Meta.Labels) != 0 {
		t.Errorf("Labels after clear = %v, want none", got.Task.Meta.Labels)
	}
}

func TestSetLabels_InvalidLabelIsInvalidArgument(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "labeled", []string{"sleep", "100"})

	_, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "labeled", Labels: []string{"1bad"}})
	if !taskservice.IsInvalidArgument(err) {
		t.Fatalf("expected CodeInvalidArgument, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

func TestSetLabels_UnknownRefIsNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: "nope", Labels: []string{"dev"}})
	if !taskservice.IsNotFound(err) {
		t.Fatalf("expected CodeNotFound, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}
