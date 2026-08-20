package mcpserver

import (
	"errors"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

// ToolError is the machine-readable error shape carried in a failed tool
// call's structured output, mirroring server.ErrorEnvelope. Per the MCP
// spec, a domain failure (task not found, name conflict, busy lock, etc.)
// is reported as a successful protocol response with
// CallToolResult.IsError set, not as a JSON-RPC protocol error -- so an
// LLM caller can see what went wrong and self-correct. Putting the same
// code/message/ref/task_id/retryable fields taskservice already computes
// into structured content (in addition to the auto-generated text content)
// keeps that information machine-readable instead of collapsing it to
// prose.
type ToolError struct {
	Code      string `json:"code" jsonschema:"Machine-readable error code: invalid_argument, not_found, conflict, failed_precondition, busy, deadline_exceeded, or internal."`
	Message   string `json:"message" jsonschema:"Safe, human-readable description of the failure."`
	Ref       string `json:"ref,omitempty" jsonschema:"The task name or ID exactly as supplied by the caller, if applicable to this failure."`
	TaskID    string `json:"task_id,omitempty" jsonschema:"Canonical resolved task ID, if it was known at the time of failure."`
	Retryable bool   `json:"retryable" jsonschema:"True if retrying the same call may succeed (e.g. transient lock contention)."`
}

// toToolError normalizes any error into a *ToolError so every tool result
// uses the same shape, treating an error that isn't (or doesn't wrap) a
// *taskservice.Error as an internal error rather than leaking its raw
// message.
func toToolError(err error) *ToolError {
	var svcErr *taskservice.Error
	if !errors.As(err, &svcErr) {
		svcErr = taskservice.Internal("", "", "", err)
	}
	return &ToolError{
		Code:      string(svcErr.Code),
		Message:   svcErr.Message,
		Ref:       svcErr.Ref,
		TaskID:    svcErr.TaskID,
		Retryable: svcErr.Retryable,
	}
}
