package mcpserver

import (
	"context"
	"encoding/json"
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
	_, _ = fmt.Fprintln(w, "ID\tNAME\tSTATE\tPORTS\tLABELS\tCOMMAND")
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
			if task.LabelsTruncated {
				labels += ",…"
			}
		}
		state := task.State
		if task.ExitCode != nil {
			state = fmt.Sprintf("exited(%d)", *task.ExitCode)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", task.ID, task.Name, state, ports, labels, task.CommandPreview)
	}
	_ = w.Flush()
	fmt.Fprintf(&b, "%d of %d matching tasks returned.", out.Returned, out.Total)
	if out.NextCursor != "" {
		fmt.Fprintf(&b, " More are available; call bgtask_list again with cursor=%q.", out.NextCursor)
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
	return textResult(renderTaskInfo(ti)), GetOutput{Task: &ti}, nil
}

func renderTaskInfo(task TaskInfo) string {
	command, _ := json.Marshal(task.Command)
	var b strings.Builder
	fmt.Fprintf(&b, "Name: %q\nID: %s\nState: %s\nCommand: %s\nCwd: %q", task.Name, task.ID, task.Status.State, command, task.Cwd)
	if len(task.EnvKeys) > 0 {
		envKeys, _ := json.Marshal(task.EnvKeys)
		fmt.Fprintf(&b, "\nEnv keys: %s", envKeys)
	}
	if len(task.Labels) > 0 {
		labels, _ := json.Marshal(task.Labels)
		fmt.Fprintf(&b, "\nLabels: %s", labels)
	}
	if task.Restart != "" {
		fmt.Fprintf(&b, "\nRestart: %s", task.Restart)
		if task.RestartDelay != "" {
			fmt.Fprintf(&b, " after %s", task.RestartDelay)
		}
	}
	if task.HealthCheck != "" {
		fmt.Fprintf(&b, "\nHealth check: %q every %s", task.HealthCheck, task.HealthInterval)
	}
	if task.CreatedAt != "" {
		fmt.Fprintf(&b, "\nCreated: %s", task.CreatedAt)
	}
	if task.Status.Running != nil {
		fmt.Fprintf(&b, "\nSupervisor PID: %d\nChild PID: %d", task.Status.Running.SupervisorPID, task.Status.Running.ChildPID)
		if len(task.Status.Running.Ports) > 0 {
			fmt.Fprintf(&b, "\nPorts: %v", task.Status.Running.Ports)
		}
		if task.Status.Running.Since != "" {
			fmt.Fprintf(&b, "\nRunning since: %s", task.Status.Running.Since)
		}
	}
	if task.Status.Exited != nil {
		fmt.Fprintf(&b, "\nExit code: %d", task.Status.Exited.Code)
		if task.Status.Exited.Signal != "" {
			fmt.Fprintf(&b, "\nSignal: %s", task.Status.Exited.Signal)
		}
		if task.Status.Exited.At != "" {
			fmt.Fprintf(&b, "\nExited at: %s", task.Status.Exited.At)
		}
	}
	if task.Status.Dead != nil {
		fmt.Fprintf(&b, "\nDead: %q", task.Status.Dead.Message)
	}
	if task.AutoRm {
		b.WriteString("\nAuto-remove: true")
	}
	fmt.Fprintf(&b, "\nLog path: %q", task.LogPath)
	return b.String()
}
