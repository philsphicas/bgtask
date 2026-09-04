package mcpserver

import (
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

const commandPreviewWidth = 120

// TaskSummary is the compact projection used by collection and mutation
// responses. Full configuration remains exclusive to TaskInfo/bgtask_get.
type TaskSummary struct {
	ID             string   `json:"id" jsonschema:"Canonical task ID."`
	Name           string   `json:"name" jsonschema:"Task name."`
	State          string   `json:"state" jsonschema:"One of running, exited, dead, or unknown."`
	Ports          []uint32 `json:"ports,omitempty" jsonschema:"Listening TCP ports, for a running task."`
	Labels         []string `json:"labels,omitempty" jsonschema:"Labels attached to the task."`
	CommandPreview string   `json:"command_preview" jsonschema:"Display-only command preview; call bgtask_get for the exact argv."`
	Since          string   `json:"since,omitempty" jsonschema:"Running since time, RFC3339."`
	ExitCode       *int     `json:"exit_code,omitempty" jsonschema:"Process exit code, present for exited tasks, including zero."`
	ExitedAt       string   `json:"exited_at,omitempty" jsonschema:"Exit time, RFC3339."`
}

// TaskInfo is the MCP wire-format projection of a task. Like
// server.TaskJSON, it is built from taskservice.PublicTask (never from
// state.Meta directly), so environment variable values can never leak
// into a tool result: only their key names are included. Durations are
// unambiguous strings (time.Duration.String, parseable back with
// time.ParseDuration) and timestamps are RFC3339.
type TaskInfo struct {
	ID             string     `json:"id" jsonschema:"Canonical task ID, assigned at creation and stable for the task's lifetime."`
	Name           string     `json:"name" jsonschema:"Task name, unique among all tasks. Renaming a task changes this value but keeps its ID."`
	Command        []string   `json:"command" jsonschema:"The command and its arguments, as an argv-style list."`
	Cwd            string     `json:"cwd" jsonschema:"The working directory the command runs in."`
	EnvKeys        []string   `json:"env_keys,omitempty" jsonschema:"Names of the environment variables overridden for this task. Values are never exposed."`
	Labels         []string   `json:"labels,omitempty" jsonschema:"Labels attached to the task, usable to target it (alongside others) in bulk operations."`
	Restart        string     `json:"restart,omitempty" jsonschema:"Restart policy: empty (never restart), \"always\", or \"on-failure\"."`
	RestartDelay   string     `json:"restart_delay,omitempty" jsonschema:"Fixed delay between restarts, as a Go duration string (e.g. \"5s\"). Empty means exponential backoff (1s-60s)."`
	HealthCheck    string     `json:"health_check,omitempty" jsonschema:"Health check command run periodically against the task, if configured."`
	HealthInterval string     `json:"health_interval,omitempty" jsonschema:"Interval between health check runs, as a Go duration string."`
	AutoRm         bool       `json:"auto_rm,omitempty" jsonschema:"True if the task's on-disk state is automatically removed once its command exits."`
	CreatedAt      string     `json:"created_at,omitempty" jsonschema:"Task creation time, RFC3339."`
	Status         StatusInfo `json:"status" jsonschema:"The task's current, freshly resolved runtime status."`
	LogPath        string     `json:"log_path" jsonschema:"Path to the task's current (newest) log file."`
}

// StatusInfo mirrors state.TaskStatus, but with timestamps rendered as
// RFC3339 strings instead of Go's default time.Time encoding.
type StatusInfo struct {
	State   string       `json:"state" jsonschema:"One of \"running\", \"exited\", \"dead\", or \"unknown\"."`
	Running *RunningInfo `json:"running,omitempty" jsonschema:"Present only when state is \"running\"."`
	Exited  *ExitedInfo  `json:"exited,omitempty" jsonschema:"Present only when state is \"exited\" (the command ran to completion, or was signaled)."`
	Dead    *DeadInfo    `json:"dead,omitempty" jsonschema:"Present only when state is \"dead\" (the supervisor process itself is gone without recording an exit)."`
}

// RunningInfo mirrors state.RunningInfo.
type RunningInfo struct {
	SupervisorPID int      `json:"supervisor_pid" jsonschema:"PID of the bgtask supervisor process managing this task."`
	ChildPID      int      `json:"child_pid" jsonschema:"PID of the supervised command itself."`
	Ports         []uint32 `json:"ports,omitempty" jsonschema:"TCP ports the child process appears to be listening on."`
	Since         string   `json:"since,omitempty" jsonschema:"When the child process started, RFC3339."`
}

// ExitedInfo mirrors state.ExitedInfo.
type ExitedInfo struct {
	Code   int    `json:"code" jsonschema:"Process exit code (0 for a clean exit)."`
	Signal string `json:"signal,omitempty" jsonschema:"Name of the signal that terminated the process, if any."`
	At     string `json:"at,omitempty" jsonschema:"When the process exited, RFC3339."`
}

// DeadInfo mirrors state.DeadInfo.
type DeadInfo struct {
	Message string `json:"message" jsonschema:"Human-readable explanation of why the task is considered dead."`
}

// BatchItemInfo is the per-task outcome within a bulk lifecycle operation
// (start, stop, restart, remove, cleanup), mirroring server.BatchItemJSON.
// Error is set only when this item failed; a best-effort batch (labels or
// all selection) never fails as a whole over one item's error.
type BatchItemInfo struct {
	Ref         string     `json:"ref" jsonschema:"The task reference exactly as processed."`
	TaskID      string     `json:"task_id,omitempty" jsonschema:"Canonical task ID, if resolved."`
	Name        string     `json:"name,omitempty" jsonschema:"Task name, if resolved."`
	State       string     `json:"state,omitempty" jsonschema:"Task state observed during the operation."`
	Changed     bool       `json:"changed" jsonschema:"True if the operation changed the task."`
	NoOp        bool       `json:"no_op,omitempty" jsonschema:"True if nothing needed to change."`
	Forced      bool       `json:"forced,omitempty" jsonschema:"True if a forceful process path was used."`
	PID         int        `json:"pid,omitempty" jsonschema:"New supervisor PID for launch operations."`
	NoBreakaway bool       `json:"no_breakaway,omitempty" jsonschema:"True if a launched supervisor could not detach from a Windows job object."`
	AutoRemoved bool       `json:"auto_removed,omitempty" jsonschema:"True if an auto-remove task already removed its state."`
	Error       *ToolError `json:"error,omitempty" jsonschema:"Set only when this item failed."`
}

type BatchCounts struct {
	Targeted int `json:"targeted"`
	Changed  int `json:"changed"`
	NoOp     int `json:"no_op"`
	Failed   int `json:"failed"`
}

// durationString renders d unambiguously for the wire: empty ("") for
// zero, otherwise time.Duration.String() (e.g. "1h30m0s"), which
// time.ParseDuration can parse back exactly.
func durationString(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}

// timeString renders t as RFC3339 (empty for the zero time).
func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// parseDuration parses an optional duration string: "" means zero,
// anything else must be a valid time.ParseDuration input.
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func toTaskInfo(pt taskservice.PublicTask) TaskInfo {
	return TaskInfo{
		ID:             pt.ID,
		Name:           pt.Name,
		Command:        pt.Command,
		Cwd:            pt.Cwd,
		EnvKeys:        pt.EnvKeys,
		Labels:         pt.Labels,
		Restart:        pt.Restart,
		RestartDelay:   durationString(pt.RestartDelay),
		HealthCheck:    pt.HealthCheck,
		HealthInterval: durationString(pt.HealthInterval),
		AutoRm:         pt.AutoRm,
		CreatedAt:      timeString(pt.CreatedAt),
		Status:         toStatusInfo(pt.Status),
		LogPath:        pt.LogPath,
	}
}

func toTaskSummary(pt taskservice.PublicTask) TaskSummary {
	summary := TaskSummary{
		ID:             pt.ID,
		Name:           pt.Name,
		State:          pt.Status.State,
		Labels:         pt.Labels,
		CommandPreview: truncateDisplay(strings.Join(pt.Command, " "), commandPreviewWidth),
	}
	if pt.Status.Running != nil {
		summary.Ports = pt.Status.Running.Ports
		if pt.Status.Running.Since != nil {
			summary.Since = timeString(*pt.Status.Running.Since)
		}
	}
	if pt.Status.Exited != nil {
		code := pt.Status.Exited.Code
		summary.ExitCode = &code
		summary.ExitedAt = timeString(pt.Status.Exited.At)
	}
	return summary
}

func truncateDisplay(value string, maxWidth int) string {
	if maxWidth <= 0 || runewidth.StringWidth(value) <= maxWidth {
		return value
	}
	return runewidth.Truncate(value, maxWidth, "…")
}

func toStatusInfo(ts state.TaskStatus) StatusInfo {
	out := StatusInfo{State: ts.State}
	if ts.Running != nil {
		out.Running = &RunningInfo{
			SupervisorPID: ts.Running.SupervisorPID,
			ChildPID:      ts.Running.ChildPID,
			Ports:         ts.Running.Ports,
		}
		if ts.Running.Since != nil {
			out.Running.Since = timeString(*ts.Running.Since)
		}
	}
	if ts.Exited != nil {
		out.Exited = &ExitedInfo{
			Code:   ts.Exited.Code,
			Signal: ts.Exited.Signal,
			At:     timeString(ts.Exited.At),
		}
	}
	if ts.Dead != nil {
		out.Dead = &DeadInfo{Message: ts.Dead.Message}
	}
	return out
}

func toBatchItemInfo(item taskservice.BatchItem) BatchItemInfo {
	out := BatchItemInfo{
		Ref:         item.Ref,
		TaskID:      item.TaskID,
		Changed:     item.Result.Changed,
		NoOp:        item.Result.NoOp,
		Forced:      item.Result.Forced,
		PID:         item.Result.PID,
		NoBreakaway: item.Result.NoBreakaway,
		AutoRemoved: item.Result.AutoRemoved,
	}
	if item.Task != nil {
		out.Name = item.Task.Meta.Name
		out.State = item.Task.Status.State
	}
	if item.Err != nil {
		out.Error = toToolError(item.Err)
	}
	return out
}

// batchOutcome converts a taskservice batch result into wire items and an
// optional top-level ToolError, mirroring server.api.respondBatch's
// three-way outcome:
//
//   - result == nil: the initial task snapshot could not be taken at all;
//     there is nothing batch-shaped to report, so err (which must be
//     non-nil) becomes the whole tool call's failure.
//   - result != nil && err != nil: an explicit refs selection failed
//     fast partway through; the partial progress made so far is still
//     returned as items, alongside the failure that stopped it.
//   - result != nil && err == nil: a best-effort (labels/all) selection,
//     or an explicit selection that fully succeeded. Per-item errors (if
//     any) are carried on the items themselves, not as a call failure.
func batchOutcome(result *taskservice.BatchResult, err error) (items []BatchItemInfo, counts BatchCounts, omitted int, failed bool, toolErr *ToolError) {
	if result == nil {
		return nil, counts, 0, true, toToolError(err)
	}
	items = make([]BatchItemInfo, 0, len(result.Items))
	for _, item := range result.Items {
		info := toBatchItemInfo(item)
		counts.Targeted++
		switch {
		case info.Error != nil:
			counts.Failed++
		case info.Changed:
			counts.Changed++
		case info.NoOp:
			counts.NoOp++
		}
		items = append(items, info)
	}
	if len(items) > maxBatchItems {
		sort.SliceStable(items, func(i, j int) bool {
			return batchItemPriority(items[i]) < batchItemPriority(items[j])
		})
		omitted = len(items) - maxBatchItems
		items = items[:maxBatchItems]
	}
	if err != nil {
		return items, counts, omitted, true, toToolError(err)
	}
	return items, counts, omitted, false, nil
}

func batchItemPriority(item BatchItemInfo) int {
	switch {
	case item.Error != nil:
		return 0
	case item.Changed || item.Forced:
		return 1
	default:
		return 2
	}
}
