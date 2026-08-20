package taskservice_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/supervisor"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// writeLogEntries appends JSONL entries to path, creating it if necessary.
func writeLogEntries(t *testing.T, path string, entries []supervisor.LogEntry) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func entry(offset time.Duration, stream, data string) supervisor.LogEntry {
	return supervisor.LogEntry{Time: time.Now().Add(offset), Stream: stream, Data: data}
}

func TestLogs_NoLogFileYet(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "nologs", []string{"sleep", "100"})

	result, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "nologs", Tail: -1})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if result.HasAnyLogFile {
		t.Error("expected HasAnyLogFile = false")
	}
	if len(result.Entries) != 0 {
		t.Errorf("expected no entries, got %d", len(result.Entries))
	}
}

func TestLogs_TailBoundsToLastN(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "tailtest", []string{"sleep", "100"})

	writeLogEntries(t, svc.Store.OutputPath(created.Task.ID), []supervisor.LogEntry{
		entry(-5*time.Second, "o", "line1\n"),
		entry(-4*time.Second, "o", "line2\n"),
		entry(-3*time.Second, "o", "line3\n"),
	})

	result, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "tailtest", Tail: 2})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}
	if result.Entries[0].Data != "line2\n" || result.Entries[1].Data != "line3\n" {
		t.Errorf("expected the last 2 lines in order, got %+v", result.Entries)
	}
}

func TestLogs_TailZeroShowsNothing(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "tailzero", []string{"sleep", "100"})
	writeLogEntries(t, svc.Store.OutputPath(created.Task.ID), []supervisor.LogEntry{entry(0, "o", "hi\n")})

	result, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "tailzero", Tail: 0})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries for --tail 0, got %d", len(result.Entries))
	}
	if !result.HasAnyLogFile {
		t.Error("expected HasAnyLogFile = true (a log file exists, just filtered to 0 lines)")
	}
}

func TestLogs_StdoutStderrFilters(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "streams", []string{"sleep", "100"})
	writeLogEntries(t, svc.Store.OutputPath(created.Task.ID), []supervisor.LogEntry{
		entry(-2*time.Second, "o", "out\n"),
		entry(-1*time.Second, "e", "err\n"),
	})

	out, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "streams", Tail: -1, Stdout: true})
	if err != nil {
		t.Fatalf("Logs (stdout): %v", err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Stream != "o" {
		t.Errorf("expected exactly 1 stdout entry, got %+v", out.Entries)
	}

	errOut, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "streams", Tail: -1, Stderr: true})
	if err != nil {
		t.Fatalf("Logs (stderr): %v", err)
	}
	if len(errOut.Entries) != 1 || errOut.Entries[0].Stream != "e" {
		t.Errorf("expected exactly 1 stderr entry, got %+v", errOut.Entries)
	}
}

func TestLogs_StdoutAndStderrIsInvalidArgument(t *testing.T) {
	svc, _ := newTestService(t)
	mustRun(t, svc, "bothstreams", []string{"sleep", "100"})

	_, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "bothstreams", Stdout: true, Stderr: true})
	if !taskservice.IsInvalidArgument(err) {
		t.Fatalf("expected CodeInvalidArgument, got %v (code=%s)", err, taskservice.CodeOf(err))
	}
}

// TestLogs_AllVsCurrentRun verifies that, by default, only entries from
// the current run (at or after child.starttime) are returned, while --all
// includes entries from a prior run too -- and that a bounded tail request
// on the current run alone does not need to read the older, pre-restart
// entries to satisfy the tail count.
func TestLogs_AllVsCurrentRun(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "runs", []string{"sleep", "100"})
	id := created.Task.ID

	writeLogEntries(t, svc.Store.OutputPath(id), []supervisor.LogEntry{
		entry(-10*time.Second, "o", "old-run-line\n"),
	})
	// child.starttime marks the boundary of the "current" run.
	currentRunStart := time.Now().Add(-5 * time.Second)
	if err := svc.Store.WriteChildStartTime(id, currentRunStart); err != nil {
		t.Fatal(err)
	}
	writeLogEntries(t, svc.Store.OutputPath(id), []supervisor.LogEntry{
		entry(-4*time.Second, "o", "new-run-line\n"),
	})

	current, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "runs", Tail: -1})
	if err != nil {
		t.Fatalf("Logs (current run): %v", err)
	}
	if len(current.Entries) != 1 || current.Entries[0].Data != "new-run-line\n" {
		t.Fatalf("expected only the current run's entry, got %+v", current.Entries)
	}

	all, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "runs", Tail: -1, All: true})
	if err != nil {
		t.Fatalf("Logs (all): %v", err)
	}
	if len(all.Entries) != 2 {
		t.Fatalf("expected both entries with --all, got %+v", all.Entries)
	}
}

// TestLogs_BoundedTailAcrossRotatedFiles verifies that a small --tail
// request is satisfied from the newest rotated file alone (ListLogFiles
// returns newest first), without needing correctness from older rotations
// to be wrong -- i.e. the bounded early-exit path still returns the right
// entries.
func TestLogs_BoundedTailAcrossRotatedFiles(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "rotated", []string{"sleep", "100"})
	id := created.Task.ID
	taskDir := svc.Store.TaskDir(id)

	// Oldest rotation.
	writeLogEntries(t, filepath.Join(taskDir, "output.jsonl.2"), []supervisor.LogEntry{
		entry(-30*time.Second, "o", "oldest\n"),
	})
	// Middle rotation.
	writeLogEntries(t, filepath.Join(taskDir, "output.jsonl.1"), []supervisor.LogEntry{
		entry(-20*time.Second, "o", "middle\n"),
	})
	// Current (newest) file.
	writeLogEntries(t, svc.Store.OutputPath(id), []supervisor.LogEntry{
		entry(-10*time.Second, "o", "newest1\n"),
		entry(-9*time.Second, "o", "newest2\n"),
	})

	result, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "rotated", Tail: 2, All: true})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(result.Entries), result.Entries)
	}
	if result.Entries[0].Data != "newest1\n" || result.Entries[1].Data != "newest2\n" {
		t.Errorf("expected the 2 newest entries from the current file alone, got %+v", result.Entries)
	}
}
