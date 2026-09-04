package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// RunInput is the input for bgtask_run.
type RunInput struct {
	Name            string            `json:"name,omitempty" jsonschema:"Task name. If omitted, one is auto-generated from the command."`
	Command         []string          `json:"command" jsonschema:"The command and its arguments to launch, as an argv-style list (must not be empty)."`
	Cwd             string            `json:"cwd" jsonschema:"Required working directory for the command. Agents must pass an explicit absolute or server-valid path."`
	Env             map[string]string `json:"env,omitempty" jsonschema:"Environment variable overrides for the command, as name/value pairs. Only the names are ever reported back by other tools; values are never echoed."`
	Labels          []string          `json:"labels,omitempty" jsonschema:"Labels to attach to the new task, usable later to target it (alongside others) in bulk operations."`
	Restart         string            `json:"restart,omitempty" jsonschema:"Restart policy: omit for never, or \"always\", or \"on-failure\"."`
	RestartDelay    string            `json:"restart_delay,omitempty" jsonschema:"Fixed delay between restarts, as a Go duration string (e.g. \"5s\"). Omit for exponential backoff (1s-60s)."`
	HealthCheck     string            `json:"health_check,omitempty" jsonschema:"A command to run periodically against the task; its output is logged."`
	HealthInterval  string            `json:"health_interval,omitempty" jsonschema:"Interval between health check runs, as a Go duration string. Only meaningful with health_check set."`
	AutoRm          bool              `json:"auto_rm,omitempty" jsonschema:"If true, automatically remove the task's on-disk state once its command exits."`
	ReplaceExisting bool              `json:"replace_existing,omitempty" jsonschema:"Destructive: if true, and a task named name already exists, stop and permanently replace it instead of failing with a conflict error. Defaults to false -- unlike bgtask's CLI, a caller must opt in to replacement explicitly."`
}

// RunOutput is the output for bgtask_run.
type RunOutput struct {
	Task             *TaskSummary `json:"task,omitempty" jsonschema:"Compact summary of the newly launched task."`
	PID              int          `json:"pid,omitempty" jsonschema:"Process ID of the launched supervisor."`
	Cwd              string       `json:"cwd,omitempty" jsonschema:"Resolved working directory."`
	LogPath          string       `json:"log_path,omitempty" jsonschema:"Path to the current task log."`
	NoBreakaway      bool         `json:"no_breakaway,omitempty" jsonschema:"True if the supervisor could not detach from a Windows job object."`
	ReplacedExisting bool         `json:"replaced_existing,omitempty" jsonschema:"True if an existing task with the same name was stopped and replaced."`
	AutoRemoved      bool         `json:"auto_removed,omitempty" jsonschema:"True if this was an --rm task whose command finished so quickly its state was already removed before launch could be confirmed (a successful terminal outcome)."`
	ImmediateExit    *ExitedInfo  `json:"immediate_exit,omitempty" jsonschema:"Set if the task had already exited by the time the call returned (e.g. a bad command)."`
	Error            *ToolError   `json:"error,omitempty" jsonschema:"Set only if the call failed; no task was launched."`
}

func (h *handlers) run(ctx context.Context, _ *mcp.CallToolRequest, in RunInput) (*mcp.CallToolResult, RunOutput, error) {
	if len(in.Command) == 0 {
		err := taskservice.InvalidArgument("run", in.Name, "", "command must not be empty")
		toolErr := toToolError(err)
		return errorResult(toolErr), RunOutput{Error: toolErr}, nil
	}
	if strings.TrimSpace(in.Cwd) == "" {
		toolErr := toToolError(taskservice.InvalidArgument("run", in.Name, "", "cwd must not be empty"))
		return errorResult(toolErr), RunOutput{Error: toolErr}, nil
	}
	restartDelay, derr := parseDuration(in.RestartDelay)
	if derr != nil {
		err := taskservice.InvalidArgument("run", in.Name, "", fmt.Sprintf("invalid restart_delay: %v", derr))
		toolErr := toToolError(err)
		return errorResult(toolErr), RunOutput{Error: toolErr}, nil
	}
	healthInterval, ierr := parseDuration(in.HealthInterval)
	if ierr != nil {
		err := taskservice.InvalidArgument("run", in.Name, "", fmt.Sprintf("invalid health_interval: %v", ierr))
		toolErr := toToolError(err)
		return errorResult(toolErr), RunOutput{Error: toolErr}, nil
	}

	result, err := h.svc.Run(ctx, taskservice.RunRequest{
		Name:            in.Name,
		Command:         in.Command,
		Cwd:             in.Cwd,
		EnvOverrides:    in.Env,
		Labels:          in.Labels,
		Restart:         in.Restart,
		RestartDelay:    restartDelay,
		HealthCheck:     in.HealthCheck,
		HealthInterval:  healthInterval,
		AutoRm:          in.AutoRm,
		ReplaceExisting: in.ReplaceExisting,
	})
	if err != nil {
		toolErr := toToolError(err)
		return errorResult(toolErr), RunOutput{Error: toolErr}, nil
	}

	ti := toTaskSummary(result.Task.Public())
	out := RunOutput{
		Task:             &ti,
		PID:              result.PID,
		Cwd:              result.Task.Meta.Cwd,
		LogPath:          result.Task.LogPath,
		NoBreakaway:      result.NoBreakaway,
		ReplacedExisting: result.ReplacedExisting,
		AutoRemoved:      result.AutoRemoved,
	}
	if result.ImmediateExit != nil {
		out.ImmediateExit = &ExitedInfo{
			Code:   result.ImmediateExit.Code,
			Signal: result.ImmediateExit.Signal,
			At:     timeString(result.ImmediateExit.ExitedAt),
		}
	}
	return textResult(fmt.Sprintf("Started %s (%s), state %s.", ti.Name, ti.ID, ti.State)), out, nil
}
