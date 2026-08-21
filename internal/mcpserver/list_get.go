package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// ListInput is the input for bgtask_list.
type ListInput struct {
	Labels []string `json:"labels,omitempty" jsonschema:"Only return tasks that have at least one of these labels (OR match). Omit or leave empty to return every task."`
}

// ListOutput is the output for bgtask_list.
type ListOutput struct {
	Tasks []TaskInfo `json:"tasks" jsonschema:"Matching tasks, sorted by ID (chronological)."`
	Error *ToolError `json:"error,omitempty" jsonschema:"Set only if the call failed."`
}

func (h *handlers) list(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOutput, error) {
	result, err := h.svc.List(ctx, taskservice.ListRequest{Labels: in.Labels})
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, ListOutput{Error: toToolError(err)}, nil
	}
	tasks := make([]TaskInfo, 0, len(result.Tasks))
	for _, t := range result.Tasks {
		tasks = append(tasks, toTaskInfo(t.Public()))
	}
	return nil, ListOutput{Tasks: tasks}, nil
}

// GetInput is the input for bgtask_get.
type GetInput struct {
	Ref string `json:"ref" jsonschema:"The task's name or ID."`
}

// GetOutput is the output for bgtask_get.
type GetOutput struct {
	Task  *TaskInfo  `json:"task,omitempty" jsonschema:"The task, if found."`
	Error *ToolError `json:"error,omitempty" jsonschema:"Set only if the call failed (e.g. no task matches ref)."`
}

func (h *handlers) get(ctx context.Context, _ *mcp.CallToolRequest, in GetInput) (*mcp.CallToolResult, GetOutput, error) {
	result, err := h.svc.Get(ctx, in.Ref)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, GetOutput{Error: toToolError(err)}, nil
	}
	ti := toTaskInfo(result.Task.Public())
	return nil, GetOutput{Task: &ti}, nil
}
