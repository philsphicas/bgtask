package taskservice_test

import (
	"context"
	"strings"
	"testing"

	"github.com/philsphicas/bgtask/internal/supervisor"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestLogsReadsEscapedLargeEntry(t *testing.T) {
	svc, _ := newTestService(t)
	created := mustRun(t, svc, "large-entry", []string{"sleep", "100"})
	value := strings.Repeat("\x01", 200000)
	writeLogEntries(t, svc.Store.OutputPath(created.Task.ID), []supervisor.LogEntry{
		entry(0, "o", value),
	})
	result, err := svc.Logs(context.Background(), taskservice.LogsRequest{Ref: "large-entry", Tail: -1, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Data != value {
		t.Fatal("large JSON-escaped entry was not preserved")
	}
}
