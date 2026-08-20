package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// defaultLogTail and maxLogTail bound the "tail" input: bounded by
// default, and capped so a single call can never demand an unbounded
// read. Mirrors server's GET /tasks/{ref}/logs; no follow/streaming is
// offered here either.
const (
	defaultLogTail = 200
	maxLogTail     = 5000
)

// LogsInput is the input for bgtask_logs.
type LogsInput struct {
	Ref    string `json:"ref" jsonschema:"The task's name or ID."`
	Tail   *int   `json:"tail,omitempty" jsonschema:"Maximum number of most-recent matching log entries to return, between 0 and 5000. 0 means no entries (metadata only). Defaults to 200 if omitted. This call never follows/streams; it always returns a bounded snapshot."`
	Since  string `json:"since,omitempty" jsonschema:"Only include entries newer than this duration ago (e.g. \"10m\", \"1h30m\"), as a Go duration string. Omit for no relative cutoff."`
	All    bool   `json:"all,omitempty" jsonschema:"Include entries from previous runs of the task, not just the current run. Defaults to false (current run only)."`
	Stdout bool   `json:"stdout,omitempty" jsonschema:"Only include stdout entries. Mutually exclusive with stderr."`
	Stderr bool   `json:"stderr,omitempty" jsonschema:"Only include stderr entries. Mutually exclusive with stdout."`
}

// LogsOutput is the output for bgtask_logs.
type LogsOutput struct {
	Entries       []LogEntryInfo `json:"entries" jsonschema:"Matching log entries, oldest first."`
	HasAnyLogFile bool           `json:"has_any_log_file" jsonschema:"True if the task has ever produced a log file, independent of whether any entries survived filtering."`
	Error         *ToolError     `json:"error,omitempty" jsonschema:"Set only if the call failed."`
}

func (h *handlers) logs(ctx context.Context, _ *mcp.CallToolRequest, in LogsInput) (*mcp.CallToolResult, LogsOutput, error) {
	tail := defaultLogTail
	if in.Tail != nil {
		tail = *in.Tail
		if tail < 0 || tail > maxLogTail {
			err := taskservice.InvalidArgument("logs", in.Ref, "", fmt.Sprintf("tail must be an integer between 0 and %d", maxLogTail))
			return &mcp.CallToolResult{IsError: true}, LogsOutput{Error: toToolError(err)}, nil
		}
	}
	since, serr := parseDuration(in.Since)
	if serr != nil {
		err := taskservice.InvalidArgument("logs", in.Ref, "", fmt.Sprintf("invalid since: %v", serr))
		return &mcp.CallToolResult{IsError: true}, LogsOutput{Error: toToolError(err)}, nil
	}

	result, err := h.svc.Logs(ctx, taskservice.LogsRequest{
		Ref:    in.Ref,
		Tail:   tail,
		Since:  since,
		All:    in.All,
		Stdout: in.Stdout,
		Stderr: in.Stderr,
	})
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, LogsOutput{Error: toToolError(err)}, nil
	}

	entries := make([]LogEntryInfo, 0, len(result.Entries))
	for _, e := range result.Entries {
		entries = append(entries, toLogEntryInfo(e))
	}
	return nil, LogsOutput{Entries: entries, HasAnyLogFile: result.HasAnyLogFile}, nil
}
