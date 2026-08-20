package mcpserver

import (
	"context"
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
	Task  *TaskInfo  `json:"task,omitempty" jsonschema:"The task, under its new name."`
	Error *ToolError `json:"error,omitempty" jsonschema:"Set only if the call failed (e.g. new_name is already in use)."`
}

func (h *handlers) rename(ctx context.Context, _ *mcp.CallToolRequest, in RenameInput) (*mcp.CallToolResult, RenameOutput, error) {
	if strings.TrimSpace(in.NewName) == "" {
		err := taskservice.InvalidArgument("rename", in.Ref, "", "new_name must not be empty")
		return &mcp.CallToolResult{IsError: true}, RenameOutput{Error: toToolError(err)}, nil
	}
	if _, err := h.svc.Rename(ctx, taskservice.RenameRequest{Ref: in.Ref, NewName: in.NewName}); err != nil {
		return &mcp.CallToolResult{IsError: true}, RenameOutput{Error: toToolError(err)}, nil
	}
	got, err := h.svc.Get(ctx, in.NewName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, RenameOutput{Error: toToolError(err)}, nil
	}
	ti := toTaskInfo(got.Task.Public())
	return nil, RenameOutput{Task: &ti}, nil
}

// SetLabelsInput is the input for bgtask_set_labels.
type SetLabelsInput struct {
	Ref    string   `json:"ref" jsonschema:"The task's name or ID."`
	Labels []string `json:"labels,omitempty" jsonschema:"Destructive: wholesale-replaces the task's labels with exactly this set. Omit or pass an empty array to clear all existing labels."`
}

// SetLabelsOutput is the output for bgtask_set_labels.
type SetLabelsOutput struct {
	Task  *TaskInfo  `json:"task,omitempty" jsonschema:"The task, with its updated labels."`
	Error *ToolError `json:"error,omitempty" jsonschema:"Set only if the call failed."`
}

func (h *handlers) setLabels(ctx context.Context, _ *mcp.CallToolRequest, in SetLabelsInput) (*mcp.CallToolResult, SetLabelsOutput, error) {
	if _, err := h.svc.SetLabels(ctx, taskservice.SetLabelsRequest{Ref: in.Ref, Labels: in.Labels}); err != nil {
		return &mcp.CallToolResult{IsError: true}, SetLabelsOutput{Error: toToolError(err)}, nil
	}
	got, err := h.svc.Get(ctx, in.Ref)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, SetLabelsOutput{Error: toToolError(err)}, nil
	}
	ti := toTaskInfo(got.Task.Public())
	return nil, SetLabelsOutput{Task: &ti}, nil
}
