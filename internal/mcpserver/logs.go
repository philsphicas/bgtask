package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/supervisor"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// defaultLogTail and maxLogTail bound the "tail" input: bounded by
// default, and capped so a single call can never demand an unbounded
// read. Mirrors server's GET /tasks/{ref}/logs; no follow/streaming is
// offered here either.
const (
	defaultLogTail     = 100
	maxLogTail         = 2000
	defaultLogMaxBytes = 32 * 1024
	maxLogMaxBytes     = 128 * 1024
	maxLogEntryBytes   = 4 * 1024
)

// LogsInput is the input for bgtask_logs.
type LogsInput struct {
	Ref      string `json:"ref" jsonschema:"The task's name or ID."`
	Tail     *int   `json:"tail,omitempty" jsonschema:"Maximum number of most-recent matching entries to inspect, between 0 and 2000. Defaults to 100."`
	Since    string `json:"since,omitempty" jsonschema:"Only include entries newer than this duration ago (e.g. \"10m\", \"1h30m\")."`
	All      bool   `json:"all,omitempty" jsonschema:"Include entries from previous runs, not just the current run."`
	Stream   string `json:"stream,omitempty" jsonschema:"Entry stream: all (default), stdout, or stderr."`
	MaxBytes *int   `json:"max_bytes,omitempty" jsonschema:"Maximum rendered log bytes, from 1 to 131072. Defaults to 32768. Individual entries are capped at 4096 bytes."`
}

// LogsOutput is the output for bgtask_logs.
type LogsOutput struct {
	Returned         int        `json:"returned" jsonschema:"Number of rendered log entries."`
	OmittedOlder     int        `json:"omitted_older,omitempty" jsonschema:"Matching older entries omitted by the byte budget."`
	ByteLimitReached bool       `json:"byte_limit_reached,omitempty" jsonschema:"True when max_bytes omitted older entries."`
	EntryTruncated   bool       `json:"entry_truncated,omitempty" jsonschema:"True when at least one individual entry exceeded 4096 bytes."`
	HasAnyLogFile    bool       `json:"has_any_log_file" jsonschema:"True if the task has ever produced a log file."`
	Error            *ToolError `json:"error,omitempty" jsonschema:"Set only if the call failed."`
}

func (h *handlers) logs(ctx context.Context, _ *mcp.CallToolRequest, in LogsInput) (*mcp.CallToolResult, LogsOutput, error) {
	tail := defaultLogTail
	if in.Tail != nil {
		tail = *in.Tail
		if tail < 0 || tail > maxLogTail {
			toolErr := toToolError(taskservice.InvalidArgument("logs", in.Ref, "", fmt.Sprintf("tail must be an integer between 0 and %d", maxLogTail)))
			return errorResult(toolErr), LogsOutput{Error: toolErr}, nil
		}
	}
	maxBytes := defaultLogMaxBytes
	if in.MaxBytes != nil {
		maxBytes = *in.MaxBytes
		if maxBytes < 1 || maxBytes > maxLogMaxBytes {
			err := toToolError(taskservice.InvalidArgument("logs", in.Ref, "", fmt.Sprintf("max_bytes must be between 1 and %d", maxLogMaxBytes)))
			return errorResult(err), LogsOutput{Error: err}, nil
		}
	}
	stdout, stderr := false, false
	switch in.Stream {
	case "", "all":
	case "stdout":
		stdout = true
	case "stderr":
		stderr = true
	default:
		err := toToolError(taskservice.InvalidArgument("logs", in.Ref, "", "stream must be all, stdout, or stderr"))
		return errorResult(err), LogsOutput{Error: err}, nil
	}
	since, serr := parseDuration(in.Since)
	if serr != nil {
		err := toToolError(taskservice.InvalidArgument("logs", in.Ref, "", fmt.Sprintf("invalid since: %v", serr)))
		return errorResult(err), LogsOutput{Error: err}, nil
	}

	result, err := h.svc.Logs(ctx, taskservice.LogsRequest{
		Ref:    in.Ref,
		Tail:   tail,
		Since:  since,
		All:    in.All,
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		toolErr := toToolError(err)
		return errorResult(toolErr), LogsOutput{Error: toolErr}, nil
	}
	text, returned, omitted, entryTruncated := renderLogs(result.Entries, maxBytes)
	out := LogsOutput{
		Returned:         returned,
		OmittedOlder:     omitted,
		ByteLimitReached: omitted > 0,
		EntryTruncated:   entryTruncated,
		HasAnyLogFile:    result.HasAnyLogFile,
	}
	if text == "" {
		if omitted > 0 {
			text = fmt.Sprintf("%d matching log entries exceeded max_bytes; increase max_bytes to read them.", omitted)
		} else {
			text = "No matching log entries."
		}
		text, _ = truncateUTF8Bytes(text, maxBytes)
	}
	return textResult(text), out, nil
}

func renderLogs(entries []supervisor.LogEntry, maxBytes int) (string, int, int, bool) {
	lines := make([]string, 0, len(entries))
	total := 0
	entryTruncated := false
	for i := len(entries) - 1; i >= 0; i-- {
		data := strings.TrimSuffix(entries[i].Data, "\n")
		if entries[i].Code != nil {
			data += fmt.Sprintf(" (code=%d)", *entries[i].Code)
		}
		if entries[i].Attempt != nil {
			data += fmt.Sprintf(" attempt=%d", *entries[i].Attempt)
		}
		if entries[i].Delay != "" {
			data += fmt.Sprintf(" delay=%s", entries[i].Delay)
		}
		if entries[i].Message != "" {
			data += " " + entries[i].Message
		}
		data, truncated := truncateUTF8Bytes(data, maxLogEntryBytes)
		line := fmt.Sprintf("%s %s %s", timeString(entries[i].Time), entries[i].Stream, data)
		lineBytes := len(line)
		if len(lines) > 0 {
			lineBytes++ // strings.Join adds one separator between adjacent lines.
		}
		if total+lineBytes > maxBytes {
			break
		}
		entryTruncated = entryTruncated || truncated
		lines = append(lines, line)
		total += lineBytes
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return strings.Join(lines, "\n"), len(lines), len(entries) - len(lines), entryTruncated
}

func truncateUTF8Bytes(value string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", value != ""
	}
	if len(value) <= maxBytes {
		return value, false
	}
	suffix := "…"
	if maxBytes < len(suffix) {
		return strings.Repeat(".", maxBytes), true
	}
	cut := maxBytes - len(suffix)
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + suffix, true
}
