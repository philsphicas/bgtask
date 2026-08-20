package server

import (
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/supervisor"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

// TaskJSON is the public, wire-format projection of a task. It is built
// from taskservice.PublicTask (never from state.Meta directly), so
// environment variable values can never leak into a response: only their
// key names are included. Durations are unambiguous strings
// (time.Duration.String, parseable back with time.ParseDuration) and
// timestamps are RFC3339.
type TaskJSON struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Command        []string   `json:"command"`
	Cwd            string     `json:"cwd"`
	EnvKeys        []string   `json:"env_keys,omitempty"`
	Labels         []string   `json:"labels,omitempty"`
	Restart        string     `json:"restart,omitempty"`
	RestartDelay   string     `json:"restart_delay,omitempty"`
	HealthCheck    string     `json:"health_check,omitempty"`
	HealthInterval string     `json:"health_interval,omitempty"`
	AutoRm         bool       `json:"auto_rm,omitempty"`
	CreatedAt      string     `json:"created_at,omitempty"`
	Status         StatusJSON `json:"status"`
	LogPath        string     `json:"log_path"`
}

// StatusJSON mirrors state.TaskStatus, but with timestamps rendered as
// RFC3339 strings instead of Go's default time.Time encoding.
type StatusJSON struct {
	State   string       `json:"state"`
	Running *RunningJSON `json:"running,omitempty"`
	Exited  *ExitedJSON  `json:"exited,omitempty"`
	Dead    *DeadJSON    `json:"dead,omitempty"`
}

// RunningJSON mirrors state.RunningInfo.
type RunningJSON struct {
	SupervisorPID int      `json:"supervisor_pid"`
	ChildPID      int      `json:"child_pid"`
	Ports         []uint32 `json:"ports,omitempty"`
	Since         string   `json:"since,omitempty"`
}

// ExitedJSON mirrors state.ExitedInfo.
type ExitedJSON struct {
	Code   int    `json:"code"`
	Signal string `json:"signal,omitempty"`
	At     string `json:"at,omitempty"`
}

// DeadJSON mirrors state.DeadInfo.
type DeadJSON struct {
	Message string `json:"message"`
}

// LogEntryJSON mirrors supervisor.LogEntry, with its timestamp rendered as
// an RFC3339 string.
type LogEntryJSON struct {
	Time    string `json:"time"`
	Stream  string `json:"stream"`
	Data    string `json:"data"`
	Code    *int   `json:"code,omitempty"`
	Attempt *int   `json:"attempt,omitempty"`
	Delay   string `json:"delay,omitempty"`
	Message string `json:"message,omitempty"`
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

func toTaskJSON(pt taskservice.PublicTask) TaskJSON {
	return TaskJSON{
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
		Status:         toStatusJSON(pt.Status),
		LogPath:        pt.LogPath,
	}
}

func toStatusJSON(ts state.TaskStatus) StatusJSON {
	out := StatusJSON{State: ts.State}
	if ts.Running != nil {
		out.Running = &RunningJSON{
			SupervisorPID: ts.Running.SupervisorPID,
			ChildPID:      ts.Running.ChildPID,
			Ports:         ts.Running.Ports,
		}
		if ts.Running.Since != nil {
			out.Running.Since = timeString(*ts.Running.Since)
		}
	}
	if ts.Exited != nil {
		out.Exited = &ExitedJSON{
			Code:   ts.Exited.Code,
			Signal: ts.Exited.Signal,
			At:     timeString(ts.Exited.At),
		}
	}
	if ts.Dead != nil {
		out.Dead = &DeadJSON{Message: ts.Dead.Message}
	}
	return out
}

func toLogEntryJSON(e supervisor.LogEntry) LogEntryJSON {
	return LogEntryJSON{
		Time:    timeString(e.Time),
		Stream:  e.Stream,
		Data:    e.Data,
		Code:    e.Code,
		Attempt: e.Attempt,
		Delay:   e.Delay,
		Message: e.Message,
	}
}

// BatchItemJSON is the per-task outcome within a batch (bulk-action) or
// single-ref mutating response. Error is set only when this item failed
// (best-effort batches report per-item errors without failing the whole
// request; see BatchResponseJSON/BatchErrorResponseJSON).
type BatchItemJSON struct {
	Ref         string         `json:"ref"`
	TaskID      string         `json:"task_id,omitempty"`
	Task        *TaskJSON      `json:"task,omitempty"`
	Changed     bool           `json:"changed"`
	NoOp        bool           `json:"no_op,omitempty"`
	Forced      bool           `json:"forced,omitempty"`
	PID         int            `json:"pid,omitempty"`
	NoBreakaway bool           `json:"no_breakaway,omitempty"`
	AutoRemoved bool           `json:"auto_removed,omitempty"`
	Error       *ErrorEnvelope `json:"error,omitempty"`
}

// BatchResponseJSON is the 200 OK body for a best-effort (labels/all
// selection, or single-ref) bulk operation: every item is reported, and a
// per-item error never fails the overall request.
type BatchResponseJSON struct {
	Items []BatchItemJSON `json:"items"`
}

func toBatchItemJSON(requestID string, item taskservice.BatchItem) BatchItemJSON {
	out := BatchItemJSON{
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
		tj := toTaskJSON(item.Task.Public())
		out.Task = &tj
	}
	if item.Err != nil {
		env := errorEnvelopeFor(item.Err, requestID)
		out.Error = &env
	}
	return out
}

// ListTasksResponseJSON is the body of GET /api/v1/tasks.
type ListTasksResponseJSON struct {
	Tasks []TaskJSON `json:"tasks"`
}

// LogsResponseJSON is the body of GET /api/v1/tasks/{ref}/logs.
type LogsResponseJSON struct {
	Entries       []LogEntryJSON `json:"entries"`
	HasAnyLogFile bool           `json:"has_any_log_file"`
}

// RunRequestJSON is the body of POST /api/v1/tasks. Durations are strings
// parsed with time.ParseDuration; an empty/omitted duration means zero.
// ReplaceExisting defaults to false, unlike the CLI's historical
// always-replace behavior: a REST client must opt in explicitly.
type RunRequestJSON struct {
	Name            string            `json:"name,omitempty"`
	Command         []string          `json:"command"`
	Cwd             string            `json:"cwd,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Labels          []string          `json:"labels,omitempty"`
	Restart         string            `json:"restart,omitempty"`
	RestartDelay    string            `json:"restart_delay,omitempty"`
	HealthCheck     string            `json:"health_check,omitempty"`
	HealthInterval  string            `json:"health_interval,omitempty"`
	AutoRm          bool              `json:"auto_rm,omitempty"`
	ReplaceExisting bool              `json:"replace_existing,omitempty"`
}

// RunResponseJSON is the body of a successful POST /api/v1/tasks.
type RunResponseJSON struct {
	Task             TaskJSON    `json:"task"`
	PID              int         `json:"pid"`
	NoBreakaway      bool        `json:"no_breakaway,omitempty"`
	ReplacedExisting bool        `json:"replaced_existing,omitempty"`
	AutoRemoved      bool        `json:"auto_removed,omitempty"`
	ImmediateExit    *ExitedJSON `json:"immediate_exit,omitempty"`
}

// StopBodyJSON is the optional body of POST /api/v1/tasks/{ref}/stop.
type StopBodyJSON struct {
	Force   bool   `json:"force,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

// RestartBodyJSON is the optional body of POST /api/v1/tasks/{ref}/restart.
type RestartBodyJSON struct {
	Force bool `json:"force,omitempty"`
}

// RenameBodyJSON is the required body of POST /api/v1/tasks/{ref}/rename.
type RenameBodyJSON struct {
	NewName string `json:"new_name"`
}

// LabelsBodyJSON is the required body of PUT /api/v1/tasks/{ref}/labels.
// Labels wholesale-replaces the task's labels; an absent/empty array
// clears them.
type LabelsBodyJSON struct {
	Labels []string `json:"labels"`
}

// BulkActionBodyJSON is the body of POST /api/v1/actions/{action}.
// Selection is required for every action except "cleanup", which targets
// every non-running task and ignores Selection/Force/Timeout entirely.
type BulkActionBodyJSON struct {
	Selection SelectionJSON `json:"selection,omitempty"`
	Force     bool          `json:"force,omitempty"`
	Timeout   string        `json:"timeout,omitempty"`
}
