package taskservice

import (
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/supervisor"
)

// Task is the canonical, front-end-agnostic view of a managed background
// task: its canonical ID, its persisted metadata, its resolved runtime
// status, and the path to its current log file.
type Task struct {
	ID      string
	Meta    *state.Meta
	Status  state.TaskStatus
	LogPath string
}

// Selection identifies which task(s) an operation targets. Exactly one of
// the three forms is expected to be meaningful per call:
//
//   - Names:  explicit names or IDs. Processed one at a time, in order,
//     stopping at the first error ("fail-fast"), matching the CLI's
//     historical behavior for `bgtask stop foo bar baz`-style invocations.
//   - Labels: OR-matched against each task's labels. Processed best-effort
//     (a failure on one task does not stop the others).
//   - All:    every task. Also processed best-effort.
type Selection struct {
	Names  []string
	Labels []string
	All    bool
}

// explicit reports whether this Selection names tasks directly, which
// selects fail-fast, one-at-a-time processing semantics.
func (s Selection) explicit() bool { return len(s.Names) > 0 }

// RunRequest describes a new task to launch.
type RunRequest struct {
	Name            string
	Command         []string
	Cwd             string
	EnvOverrides    map[string]string
	Labels          []string
	Restart         string
	RestartDelay    time.Duration
	HealthCheck     string
	HealthInterval  time.Duration
	AutoRm          bool
	ReplaceExisting bool // if a task with Name already exists, stop and replace it instead of failing
}

// RunResult is returned by Service.Run.
type RunResult struct {
	Task             Task
	PID              int
	NoBreakaway      bool        // true if the supervisor could not detach from a Windows job object
	ReplacedExisting bool        // true if an existing task with the same name was stopped and replaced
	ImmediateExit    *state.Exit // non-nil if the task had already exited by the time Run returned (e.g. bad command)

	// AutoRemoved is true if this was an --rm task whose command finished
	// (and whose supervisor therefore deleted the task's state directory)
	// before Run could confirm the launch. It is a successful terminal
	// outcome: the task ran, and nothing is left to inspect.
	AutoRemoved bool
}

// ListRequest filters the tasks returned by Service.List.
type ListRequest struct {
	Labels []string // OR semantics: a task matches if it has any of these labels
}

// ListResult is returned by Service.List. Tasks are sorted by ID (which,
// given state.GenerateID's timestamp prefix, is also chronological), so
// bulk consumers see a deterministic snapshot.
type ListResult struct {
	Tasks []Task
}

// GetResult is returned by Service.Get.
type GetResult struct {
	Task Task
}

// LogsRequest describes a bounded log read for a single task.
type LogsRequest struct {
	Ref    string        // task name or ID
	Tail   int           // -1 = unlimited, 0 = none, N = last N matching entries
	Since  time.Duration // 0 = no relative cutoff
	All    bool          // include entries from previous runs (default: current run only)
	Stdout bool          // only stdout entries
	Stderr bool          // only stderr entries
}

// LogsResult is returned by Service.Logs.
type LogsResult struct {
	Entries []supervisor.LogEntry

	// HasAnyLogFile is true if the task has ever produced a log file
	// (independent of whether any entries survived filtering).
	HasAnyLogFile bool

	// OutputPath is the path to the task's current (newest) log file,
	// useful for a caller implementing its own follow/tail-f loop.
	OutputPath string

	// ExitPath is the path to the task's exit.json, useful for a follow
	// loop to detect when the task has exited.
	ExitPath string
}

// OperationResult describes the outcome of a single-task mutating
// operation (rename, set-labels, or one item of a batch operation).
type OperationResult struct {
	// Changed is true if the operation performed a state change.
	Changed bool

	// NoOp is true if the task was already in the requested state and
	// nothing needed to change (e.g. stopping an already-stopped task).
	// NoOp operations are not errors.
	NoOp bool

	// Forced is true if a forceful path was taken: an explicit Force
	// request, or an internal escalation from graceful stop to SIGKILL
	// after the graceful timeout elapsed.
	Forced bool

	// PID is the new process ID, for operations that launch a process
	// (Run, Start).
	PID int

	// NoBreakaway is true if a launched supervisor could not detach from
	// a Windows job object (see RunResult.NoBreakaway).
	NoBreakaway bool

	// AutoRemoved is true if the launched task was an --rm task whose
	// command finished so quickly that the supervisor had already deleted
	// the task's state directory before the launch was confirmed. This is
	// a successful terminal outcome, not a missing task.
	AutoRemoved bool
}

// RenameRequest renames a task.
type RenameRequest struct {
	Ref     string // current name or ID
	NewName string
}

// SetLabelsRequest replaces the labels on a task.
type SetLabelsRequest struct {
	Ref    string
	Labels []string
}

// StopRequest stops one or more tasks.
type StopRequest struct {
	Selection Selection
	Force     bool
	Timeout   time.Duration // graceful shutdown timeout; <= 0 uses the service default
}

// RestartRequest restarts one or more running tasks.
type RestartRequest struct {
	Selection Selection
	Force     bool // force-kill the child before restarting
}

// StartRequest starts (re-launches) one or more stopped tasks.
type StartRequest struct {
	Selection Selection
}

// RemoveRequest stops (if needed) and deletes one or more tasks.
type RemoveRequest struct {
	Selection Selection
	Force     bool
	Timeout   time.Duration // graceful shutdown timeout; <= 0 uses the service default
}

// CleanupRequest removes state for all non-running tasks.
type CleanupRequest struct{}

// BatchItem is the outcome for a single task within a batch operation.
type BatchItem struct {
	Ref    string // the name/ID/task-ID as processed
	TaskID string // canonical resolved task ID, if resolution succeeded
	Task   *Task  // resolved task, if available (even on a FailedPrecondition error)
	Result OperationResult
	Err    error // typed *Error, nil on success
}

// BatchResult is returned by the bulk operations (Stop, Restart, Start,
// Remove, Cleanup). For an explicit Selection.Names request, Items holds
// only the processed prefix up to (and including) the first failure --
// processing stops there and the same error is also returned directly from
// the Service method. For a Labels/All request, Items holds one entry per
// matched task and processing never stops early; the Service method itself
// returns a nil error unless the initial task snapshot could not be taken.
type BatchResult struct {
	Items []BatchItem
}
