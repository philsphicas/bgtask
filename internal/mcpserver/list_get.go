package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// ListInput is the input for bgtask_list.
type ListInput struct {
	States []string `json:"states,omitempty" jsonschema:"Only return these runtime states (OR match): running, exited, dead, unknown. Omit or leave empty for all states. Use [\"running\"] when the user asks what is currently running."`
	Labels []string `json:"labels,omitempty" jsonschema:"Only return tasks that have at least one of these labels (OR match). State and label filters combine with AND."`
	Limit  *int     `json:"limit,omitempty" jsonschema:"Maximum summaries to return, from 1 to 100. Defaults to 20."`
	Cursor string   `json:"cursor,omitempty" jsonschema:"Opaque continuation token from next_cursor. Pass it back unchanged with the same filters."`
}

// ListOutput is the output for bgtask_list.
type ListOutput struct {
	Tasks      []TaskSummary `json:"tasks" jsonschema:"Compact matching task summaries, newest first. Call bgtask_get for complete configuration."`
	Returned   int           `json:"returned" jsonschema:"Number of summaries in this page."`
	Total      int           `json:"total" jsonschema:"Number of tasks matching all filters before pagination."`
	NextCursor string        `json:"next_cursor,omitempty" jsonschema:"Opaque token for the next page. Omitted on the final page."`
	Error      *ToolError    `json:"error,omitempty" jsonschema:"Set only if the call failed."`
}

func (h *handlers) list(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOutput, error) {
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	if limit < 1 || limit > maxListLimit {
		err := toToolError(taskservice.InvalidArgument("list", "", "", fmt.Sprintf("limit must be between 1 and %d", maxListLimit)))
		return errorResult(err), ListOutput{Error: err}, nil
	}
	result, err := h.svc.List(ctx, taskservice.ListRequest{
		Labels: in.Labels, States: in.States, Limit: limit, Cursor: in.Cursor, NewestFirst: true,
	})
	if err != nil {
		toolErr := toToolError(err)
		return errorResult(toolErr), ListOutput{Error: toolErr}, nil
	}
	tasks := make([]TaskSummary, 0, len(result.Tasks))
	for _, t := range result.Tasks {
		tasks = append(tasks, toTaskSummary(t.Public()))
	}
	out := ListOutput{Tasks: tasks, Returned: len(tasks), Total: result.Total, NextCursor: result.NextCursor}
	return textResult(renderTaskList(out)), out, nil
}

func renderTaskList(out ListOutput) string {
	if len(out.Tasks) == 0 {
		return "No bgtasks match the requested filters."
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTATE\tPORTS\tLABELS\tCOMMAND")
	for _, task := range out.Tasks {
		ports := "-"
		if len(task.Ports) > 0 {
			values := make([]string, len(task.Ports))
			for i, port := range task.Ports {
				values[i] = fmt.Sprintf(":%d", port)
			}
			ports = strings.Join(values, ",")
		}
		labels := "-"
		if len(task.Labels) > 0 {
			labels = strings.Join(task.Labels, ",")
		}
		state := task.State
		if task.ExitCode != nil {
			state = fmt.Sprintf("exited(%d)", *task.ExitCode)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", task.Name, state, ports, labels, task.CommandPreview)
	}
	_ = w.Flush()
	fmt.Fprintf(&b, "%d of %d matching tasks returned.", out.Returned, out.Total)
	if out.NextCursor != "" {
		b.WriteString(" More are available; call bgtask_list again with next_cursor as cursor.")
	}
	return b.String()
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
		toolErr := toToolError(err)
		return errorResult(toolErr), GetOutput{Error: toolErr}, nil
	}
	ti := toTaskInfo(result.Task.Public())
	return textResult(fmt.Sprintf("%s (%s) is %s.", ti.Name, ti.ID, ti.Status.State)), GetOutput{Task: &ti}, nil
}
