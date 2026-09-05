package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

const maxBatchItems = 50

func selectionErr(op string, err error) *ToolError {
	return toToolError(taskservice.InvalidArgument(op, "", "", err.Error()))
}

type BatchOutput struct {
	Mode         string          `json:"mode,omitempty" jsonschema:"Selection mode: refs, labels, all, or cleanup."`
	StoppedEarly bool            `json:"stopped_early,omitempty" jsonschema:"True when explicit refs stopped at the first failure."`
	Counts       BatchCounts     `json:"counts" jsonschema:"Complete aggregate outcome counts."`
	Items        []BatchItemInfo `json:"items" jsonschema:"Prioritized per-task details, capped at 50; failures are included first."`
	ItemsOmitted int             `json:"items_omitted,omitempty" jsonschema:"Number of task details omitted from items."`
	Error        *ToolError      `json:"error,omitempty" jsonschema:"Whole-call or fail-fast error; item failures are also attached to items."`
}

type StartOutput = BatchOutput
type StopOutput = BatchOutput
type RestartOutput = BatchOutput
type RemoveOutput = BatchOutput
type CleanupOutput = BatchOutput

type StartInput struct {
	SelectionInput
}

type StopInput struct {
	SelectionInput
	Force   bool   `json:"force,omitempty" jsonschema:"Skip graceful shutdown and kill immediately."`
	Timeout string `json:"timeout,omitempty" jsonschema:"Graceful shutdown timeout as a Go duration; ignored when force is true."`
}

type RestartInput struct {
	SelectionInput
	Force bool `json:"force,omitempty" jsonschema:"Force-kill the child before restarting."`
}

type RemoveInput struct {
	SelectionInput
	Force   bool   `json:"force,omitempty" jsonschema:"Skip graceful shutdown and kill before removal."`
	Timeout string `json:"timeout,omitempty" jsonschema:"Graceful shutdown timeout as a Go duration; ignored when force is true."`
}

type CleanupInput struct{}

func (h *handlers) start(ctx context.Context, _ *mcp.CallToolRequest, in StartInput) (*mcp.CallToolResult, StartOutput, error) {
	sel, err := parseSelection(in.SelectionInput)
	if err != nil {
		toolErr := selectionErr("start", err)
		return errorResult(toolErr), BatchOutput{Error: toolErr}, nil
	}
	result, svcErr := h.svc.Start(ctx, taskservice.StartRequest{Selection: sel})
	return batchResponse(selectionMode(in.SelectionInput), result, svcErr)
}

func (h *handlers) stop(ctx context.Context, _ *mcp.CallToolRequest, in StopInput) (*mcp.CallToolResult, StopOutput, error) {
	sel, err := parseSelection(in.SelectionInput)
	if err != nil {
		toolErr := selectionErr("stop", err)
		return errorResult(toolErr), BatchOutput{Error: toolErr}, nil
	}
	timeout, err := parseDuration(in.Timeout)
	if err != nil {
		toolErr := toToolError(taskservice.InvalidArgument("stop", "", "", fmt.Sprintf("invalid timeout: %v", err)))
		return errorResult(toolErr), BatchOutput{Error: toolErr}, nil
	}
	result, svcErr := h.svc.Stop(ctx, taskservice.StopRequest{Selection: sel, Force: in.Force, Timeout: timeout})
	return batchResponse(selectionMode(in.SelectionInput), result, svcErr)
}

func (h *handlers) restart(ctx context.Context, _ *mcp.CallToolRequest, in RestartInput) (*mcp.CallToolResult, RestartOutput, error) {
	sel, err := parseSelection(in.SelectionInput)
	if err != nil {
		toolErr := selectionErr("restart", err)
		return errorResult(toolErr), BatchOutput{Error: toolErr}, nil
	}
	result, svcErr := h.svc.Restart(ctx, taskservice.RestartRequest{Selection: sel, Force: in.Force})
	return batchResponse(selectionMode(in.SelectionInput), result, svcErr)
}

func (h *handlers) remove(ctx context.Context, _ *mcp.CallToolRequest, in RemoveInput) (*mcp.CallToolResult, RemoveOutput, error) {
	sel, err := parseSelection(in.SelectionInput)
	if err != nil {
		toolErr := selectionErr("remove", err)
		return errorResult(toolErr), BatchOutput{Error: toolErr}, nil
	}
	timeout, err := parseDuration(in.Timeout)
	if err != nil {
		toolErr := toToolError(taskservice.InvalidArgument("remove", "", "", fmt.Sprintf("invalid timeout: %v", err)))
		return errorResult(toolErr), BatchOutput{Error: toolErr}, nil
	}
	result, svcErr := h.svc.Remove(ctx, taskservice.RemoveRequest{Selection: sel, Force: in.Force, Timeout: timeout})
	return batchResponse(selectionMode(in.SelectionInput), result, svcErr)
}

func (h *handlers) cleanup(ctx context.Context, _ *mcp.CallToolRequest, _ CleanupInput) (*mcp.CallToolResult, CleanupOutput, error) {
	result, err := h.svc.Cleanup(ctx, taskservice.CleanupRequest{})
	return batchResponse("cleanup", result, err)
}

func batchResponse(mode string, result *taskservice.BatchResult, err error) (*mcp.CallToolResult, BatchOutput, error) {
	items, counts, omitted, failed, toolErr := batchOutcome(result, err)
	out := BatchOutput{
		Mode:         mode,
		StoppedEarly: failed && mode == "refs" && result != nil,
		Counts:       counts,
		Items:        items,
		ItemsOmitted: omitted,
		Error:        toolErr,
	}
	text := renderBatch(out)
	if failed {
		res := textResult(text)
		res.IsError = true
		return res, out, nil
	}
	return textResult(text), out, nil
}

func renderBatch(out BatchOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s operation: %d targeted, %d changed, %d no-op, %d failed.",
		out.Mode, out.Counts.Targeted, out.Counts.Changed, out.Counts.NoOp, out.Counts.Failed)
	if out.StoppedEarly {
		b.WriteString(" Processing stopped at the first explicit-ref failure.")
	}
	if out.ItemsOmitted > 0 {
		fmt.Fprintf(&b, " %d routine item details omitted.", out.ItemsOmitted)
	}
	for _, item := range out.Items {
		if item.Error != nil {
			fmt.Fprintf(&b, "\n%s: %s: %s", item.Ref, item.Error.Code, item.Error.Message)
		}
	}
	if out.Error != nil && len(out.Items) == 0 {
		fmt.Fprintf(&b, "\n%s: %s", out.Error.Code, out.Error.Message)
	}
	return b.String()
}
