package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// selectionErr builds the CallToolResult/ToolError pair for a malformed
// SelectionInput, shared by every lifecycle tool below.
func selectionErr(op string, err error) *ToolError {
	return toToolError(taskservice.InvalidArgument(op, "", "", err.Error()))
}

// StartInput is the input for bgtask_start.
type StartInput struct {
	Selection SelectionInput `json:"selection" jsonschema:"Which stopped task(s) to (re-)launch. Exactly one of selection.refs, selection.labels, or selection.all must be set."`
}

// StartOutput is the output for bgtask_start.
type StartOutput struct {
	Items []BatchItemInfo `json:"items" jsonschema:"Per-task outcome, one entry per targeted task."`
	Error *ToolError      `json:"error,omitempty" jsonschema:"Set only if the whole call failed (e.g. an explicit ref was not found); per-item failures are reported on the matching item instead."`
}

func (h *handlers) start(ctx context.Context, _ *mcp.CallToolRequest, in StartInput) (*mcp.CallToolResult, StartOutput, error) {
	sel, serr := parseSelection(in.Selection)
	if serr != nil {
		return &mcp.CallToolResult{IsError: true}, StartOutput{Error: selectionErr("start", serr)}, nil
	}
	result, err := h.svc.Start(ctx, taskservice.StartRequest{Selection: sel})
	items, failed, toolErr := batchOutcome(result, err)
	out := StartOutput{Items: items, Error: toolErr}
	if failed {
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	return nil, out, nil
}

// StopInput is the input for bgtask_stop.
type StopInput struct {
	Selection SelectionInput `json:"selection" jsonschema:"Which task(s) to stop. Exactly one of selection.refs, selection.labels, or selection.all must be set. A task that is already stopped is reported as a no-op, not an error."`
	Force     bool           `json:"force,omitempty" jsonschema:"Destructive: skip the graceful shutdown signal and SIGKILL the task's process(es) immediately."`
	Timeout   string         `json:"timeout,omitempty" jsonschema:"How long to wait for graceful shutdown before escalating to a forceful kill, as a Go duration string (e.g. \"10s\"). Empty uses the server's default. Ignored if force is true."`
}

// StopOutput is the output for bgtask_stop.
type StopOutput struct {
	Items []BatchItemInfo `json:"items" jsonschema:"Per-task outcome, one entry per targeted task."`
	Error *ToolError      `json:"error,omitempty" jsonschema:"Set only if the whole call failed; per-item failures are reported on the matching item instead."`
}

func (h *handlers) stop(ctx context.Context, _ *mcp.CallToolRequest, in StopInput) (*mcp.CallToolResult, StopOutput, error) {
	sel, serr := parseSelection(in.Selection)
	if serr != nil {
		return &mcp.CallToolResult{IsError: true}, StopOutput{Error: selectionErr("stop", serr)}, nil
	}
	timeout, terr := parseDuration(in.Timeout)
	if terr != nil {
		err := taskservice.InvalidArgument("stop", "", "", fmt.Sprintf("invalid timeout: %v", terr))
		return &mcp.CallToolResult{IsError: true}, StopOutput{Error: toToolError(err)}, nil
	}
	result, err := h.svc.Stop(ctx, taskservice.StopRequest{Selection: sel, Force: in.Force, Timeout: timeout})
	items, failed, toolErr := batchOutcome(result, err)
	out := StopOutput{Items: items, Error: toolErr}
	if failed {
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	return nil, out, nil
}

// RestartInput is the input for bgtask_restart.
type RestartInput struct {
	Selection SelectionInput `json:"selection" jsonschema:"Which running task(s) to restart. Exactly one of selection.refs, selection.labels, or selection.all must be set. A task that is not running fails with a failed_precondition error."`
	Force     bool           `json:"force,omitempty" jsonschema:"Destructive: force-kill the child process before restarting, instead of signaling a graceful restart."`
}

// RestartOutput is the output for bgtask_restart.
type RestartOutput struct {
	Items []BatchItemInfo `json:"items" jsonschema:"Per-task outcome, one entry per targeted task."`
	Error *ToolError      `json:"error,omitempty" jsonschema:"Set only if the whole call failed; per-item failures are reported on the matching item instead."`
}

func (h *handlers) restart(ctx context.Context, _ *mcp.CallToolRequest, in RestartInput) (*mcp.CallToolResult, RestartOutput, error) {
	sel, serr := parseSelection(in.Selection)
	if serr != nil {
		return &mcp.CallToolResult{IsError: true}, RestartOutput{Error: selectionErr("restart", serr)}, nil
	}
	result, err := h.svc.Restart(ctx, taskservice.RestartRequest{Selection: sel, Force: in.Force})
	items, failed, toolErr := batchOutcome(result, err)
	out := RestartOutput{Items: items, Error: toolErr}
	if failed {
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	return nil, out, nil
}

// RemoveInput is the input for bgtask_remove.
type RemoveInput struct {
	Selection SelectionInput `json:"selection" jsonschema:"Which task(s) to stop (if running) and permanently delete. Exactly one of selection.refs, selection.labels, or selection.all must be set."`
	Force     bool           `json:"force,omitempty" jsonschema:"Destructive: skip graceful shutdown and SIGKILL any running task(s) immediately before deleting their state."`
	Timeout   string         `json:"timeout,omitempty" jsonschema:"How long to wait for graceful shutdown before escalating to a forceful kill, as a Go duration string (e.g. \"10s\"). Empty uses the server's default. Ignored if force is true."`
}

// RemoveOutput is the output for bgtask_remove.
type RemoveOutput struct {
	Items []BatchItemInfo `json:"items" jsonschema:"Per-task outcome, one entry per targeted task."`
	Error *ToolError      `json:"error,omitempty" jsonschema:"Set only if the whole call failed; per-item failures are reported on the matching item instead."`
}

func (h *handlers) remove(ctx context.Context, _ *mcp.CallToolRequest, in RemoveInput) (*mcp.CallToolResult, RemoveOutput, error) {
	sel, serr := parseSelection(in.Selection)
	if serr != nil {
		return &mcp.CallToolResult{IsError: true}, RemoveOutput{Error: selectionErr("remove", serr)}, nil
	}
	timeout, terr := parseDuration(in.Timeout)
	if terr != nil {
		err := taskservice.InvalidArgument("remove", "", "", fmt.Sprintf("invalid timeout: %v", terr))
		return &mcp.CallToolResult{IsError: true}, RemoveOutput{Error: toToolError(err)}, nil
	}
	result, err := h.svc.Remove(ctx, taskservice.RemoveRequest{Selection: sel, Force: in.Force, Timeout: timeout})
	items, failed, toolErr := batchOutcome(result, err)
	out := RemoveOutput{Items: items, Error: toolErr}
	if failed {
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	return nil, out, nil
}

// CleanupInput is the input for bgtask_cleanup. It takes no parameters:
// cleanup always targets every non-running task, since the underlying
// taskservice.CleanupRequest has no label-filtering capability today.
type CleanupInput struct{}

// CleanupOutput is the output for bgtask_cleanup.
type CleanupOutput struct {
	Items []BatchItemInfo `json:"items" jsonschema:"Per-task outcome, one entry per non-running task considered."`
	Error *ToolError      `json:"error,omitempty" jsonschema:"Set only if the whole call failed; per-item failures are reported on the matching item instead."`
}

func (h *handlers) cleanup(ctx context.Context, _ *mcp.CallToolRequest, _ CleanupInput) (*mcp.CallToolResult, CleanupOutput, error) {
	result, err := h.svc.Cleanup(ctx, taskservice.CleanupRequest{})
	items, failed, toolErr := batchOutcome(result, err)
	out := CleanupOutput{Items: items, Error: toolErr}
	if failed {
		return &mcp.CallToolResult{IsError: true}, out, nil
	}
	return nil, out, nil
}
