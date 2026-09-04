package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// RenameInput is the input for bgtask_rename.
type RenameInput struct {
	Ref     string `json:"ref" jsonschema:"The task's current name or ID."`
	NewName string `json:"new_name" jsonschema:"The task's new name. Must be unique among all tasks."`
}

// RenameOutput is the output for bgtask_rename.
type RenameOutput struct {
	TaskID  string     `json:"task_id,omitempty" jsonschema:"Canonical task ID."`
	Name    string     `json:"name,omitempty" jsonschema:"Resulting task name."`
	Changed bool       `json:"changed" jsonschema:"True if the name changed."`
	NoOp    bool       `json:"no_op,omitempty" jsonschema:"True if the task already had this name."`
	Error   *ToolError `json:"error,omitempty" jsonschema:"Set only if the call failed."`
}

func (h *handlers) rename(ctx context.Context, _ *mcp.CallToolRequest, in RenameInput) (*mcp.CallToolResult, RenameOutput, error) {
	if strings.TrimSpace(in.NewName) == "" {
		err := taskservice.InvalidArgument("rename", in.Ref, "", "new_name must not be empty")
		toolErr := toToolError(err)
		return errorResult(toolErr), RenameOutput{Error: toolErr}, nil
	}
	result, err := h.svc.Rename(ctx, taskservice.RenameRequest{Ref: in.Ref, NewName: in.NewName})
	if err != nil {
		toolErr := toToolError(err)
		return errorResult(toolErr), RenameOutput{Error: toolErr}, nil
	}
	out := RenameOutput{TaskID: result.TaskID, Name: result.Name, Changed: result.Changed, NoOp: result.NoOp}
	return textResult(fmt.Sprintf("Task %s is named %s.", result.TaskID, result.Name)), out, nil
}

// SetLabelsInput is the input for bgtask_set_labels.
type SetLabelsInput struct {
	Ref    string   `json:"ref" jsonschema:"The task's name or ID."`
	Labels []string `json:"labels,omitempty" jsonschema:"Destructive: wholesale-replaces the task's labels with exactly this set. Omit or pass an empty array to clear all existing labels."`
}

// SetLabelsOutput is the output for bgtask_set_labels.
type SetLabelsOutput struct {
	TaskID  string     `json:"task_id,omitempty" jsonschema:"Canonical task ID."`
	Name    string     `json:"name,omitempty" jsonschema:"Task name."`
	Labels  []string   `json:"labels" jsonschema:"Complete resulting label set."`
	Changed bool       `json:"changed" jsonschema:"True if labels were written."`
	Error   *ToolError `json:"error,omitempty" jsonschema:"Set only if the call failed."`
}

func (h *handlers) setLabels(ctx context.Context, _ *mcp.CallToolRequest, in SetLabelsInput) (*mcp.CallToolResult, SetLabelsOutput, error) {
	result, err := h.svc.SetLabels(ctx, taskservice.SetLabelsRequest{Ref: in.Ref, Labels: in.Labels})
	if err != nil {
		toolErr := toToolError(err)
		return errorResult(toolErr), SetLabelsOutput{Error: toolErr}, nil
	}
	out := SetLabelsOutput{TaskID: result.TaskID, Name: result.Name, Labels: result.Labels, Changed: result.Changed}
	return textResult(fmt.Sprintf("Set labels on %s (%s).", result.Name, result.TaskID)), out, nil
}
