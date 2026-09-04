package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
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

func addLogsTool(server *mcp.Server, h *handlers) {
	inputSchema, err := jsonschema.For[LogsInput](nil)
	if err != nil {
		panic(fmt.Sprintf("derive bgtask_logs input schema: %v", err))
	}
	outputSchema, err := jsonschema.For[LogsOutput](nil)
	if err != nil {
		panic(fmt.Sprintf("derive bgtask_logs output schema: %v", err))
	}
	server.AddTool(&mcp.Tool{
		Name: "bgtask_logs",
		Description: "Read a text snapshot of task logs, bounded by both entry count (default 100, max 2000) and rendered bytes " +
			"(default 32768, max 131072). Filter with stream=all|stdout|stderr and since; set all to include prior runs. " +
			"Truncation is reported explicitly. This tool never follows or streams.",
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
		Annotations:  &mcp.ToolAnnotations{Title: "Tasks: Logs", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	}, h.logsRaw)
}

func (h *handlers) logsRaw(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := req.Params.Arguments
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	maxBytes := logMaxBytesFromArguments(raw)
	input, err := decodeLogsInput(raw)
	if err != nil {
		return structuredLogsError(taskservice.InvalidArgument("logs", "", "", fmt.Sprintf("invalid arguments: %v", err)), maxBytes), nil
	}
	if strings.TrimSpace(input.Ref) == "" {
		return structuredLogsError(taskservice.InvalidArgument("logs", "", "", "ref must not be empty"), maxBytes), nil
	}
	result, output, err := h.logs(ctx, req, input)
	if err != nil {
		return nil, err
	}
	return setLogsStructuredContent(result, output), nil
}

func decodeLogsInput(raw json.RawMessage) (LogsInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return LogsInput{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return LogsInput{}, fmt.Errorf("expected one JSON object")
	}
	allowed := map[string]bool{"ref": true, "tail": true, "since": true, "all": true, "stream": true, "max_bytes": true}
	for key := range fields {
		if !allowed[key] {
			return LogsInput{}, fmt.Errorf("unknown field %q", key)
		}
	}
	var input LogsInput
	var err error
	if input.Ref, err = stringField(fields, "ref"); err != nil {
		return LogsInput{}, err
	}
	if input.Since, err = stringField(fields, "since"); err != nil {
		return LogsInput{}, err
	}
	if input.Stream, err = stringField(fields, "stream"); err != nil {
		return LogsInput{}, err
	}
	if input.All, err = boolField(fields, "all"); err != nil {
		return LogsInput{}, err
	}
	if input.Tail, err = intPointerField(fields, "tail"); err != nil {
		return LogsInput{}, err
	}
	if input.MaxBytes, err = intPointerField(fields, "max_bytes"); err != nil {
		return LogsInput{}, err
	}
	return input, nil
}

func stringField(fields map[string]any, key string) (string, error) {
	value, ok := fields[key]
	if !ok {
		return "", nil
	}
	if value == nil {
		return "", fmt.Errorf("%s must be a string", key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}

func boolField(fields map[string]any, key string) (bool, error) {
	value, ok := fields[key]
	if !ok {
		return false, nil
	}
	if value == nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return flag, nil
}

func intPointerField(fields map[string]any, key string) (*int, error) {
	value, ok := fields[key]
	if !ok || value == nil {
		return nil, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, fmt.Errorf("%s must be an integer", key)
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed != math.Trunc(parsed) ||
		parsed < float64(math.MinInt) || parsed > float64(math.MaxInt) {
		return nil, fmt.Errorf("%s must be an integer", key)
	}
	result := int(parsed)
	return &result, nil
}

func logMaxBytesFromArguments(raw json.RawMessage) int {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return defaultLogMaxBytes
	}
	value, err := intPointerField(fields, "max_bytes")
	if err == nil && value != nil && *value >= 1 && *value <= maxLogMaxBytes {
		return *value
	}
	return defaultLogMaxBytes
}

func structuredLogsError(err error, maxBytes int) *mcp.CallToolResult {
	toolErr := toToolError(err)
	return setLogsStructuredContent(boundedLogErrorResult(toolErr, maxBytes), LogsOutput{Error: toolErr})
}

func setLogsStructuredContent(result *mcp.CallToolResult, output LogsOutput) *mcp.CallToolResult {
	raw, err := json.Marshal(output)
	if err != nil {
		panic(fmt.Sprintf("marshal bgtask_logs output: %v", err))
	}
	result.StructuredContent = json.RawMessage(raw)
	return result
}

func (h *handlers) logs(ctx context.Context, _ *mcp.CallToolRequest, in LogsInput) (*mcp.CallToolResult, LogsOutput, error) {
	maxBytes := defaultLogMaxBytes
	if in.MaxBytes != nil {
		maxBytes = *in.MaxBytes
		if maxBytes < 1 || maxBytes > maxLogMaxBytes {
			err := toToolError(taskservice.InvalidArgument("logs", in.Ref, "", fmt.Sprintf("max_bytes must be between 1 and %d", maxLogMaxBytes)))
			return errorResult(err), LogsOutput{Error: err}, nil
		}
	}
	tail := defaultLogTail
	if in.Tail != nil {
		tail = *in.Tail
		if tail < 0 || tail > maxLogTail {
			toolErr := toToolError(taskservice.InvalidArgument("logs", in.Ref, "", fmt.Sprintf("tail must be an integer between 0 and %d", maxLogTail)))
			return boundedLogErrorResult(toolErr, maxBytes), LogsOutput{Error: toolErr}, nil
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
		return boundedLogErrorResult(err, maxBytes), LogsOutput{Error: err}, nil
	}
	since, serr := parseDuration(in.Since)
	if serr != nil {
		err := toToolError(taskservice.InvalidArgument("logs", in.Ref, "", fmt.Sprintf("invalid since: %v", serr)))
		return boundedLogErrorResult(err, maxBytes), LogsOutput{Error: err}, nil
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
		return boundedLogErrorResult(toolErr, maxBytes), LogsOutput{Error: toolErr}, nil
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

func boundedLogErrorResult(err *ToolError, maxBytes int) *mcp.CallToolResult {
	text := "bgtask operation failed"
	if err != nil {
		text = fmt.Sprintf("%s: %s", err.Code, err.Message)
	}
	text, _ = truncateUTF8Bytes(text, maxBytes)
	result := textResult(text)
	result.IsError = true
	return result
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
		prefix := fmt.Sprintf("%s %s ", timeString(entries[i].Time), entries[i].Stream)
		data, truncated := truncateUTF8Bytes(data, maxLogEntryBytes-len(prefix))
		line := prefix + data
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
