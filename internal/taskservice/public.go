package taskservice

import (
	"sort"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
)

// PublicTask is a front-end-agnostic, redacted projection of Task meant for
// exposure across a service boundary (HTTP, MCP): environment variable
// *values* are never included, only their key names, so a caller can see
// which variables a task overrides without learning secrets that may be
// stored in them. Command, working directory, and log path remain fully
// visible, since none of those are treated as sensitive on their own.
type PublicTask struct {
	ID             string
	Name           string
	Command        []string
	Cwd            string
	EnvKeys        []string // sorted; values are intentionally omitted
	Labels         []string
	Restart        string
	RestartDelay   time.Duration
	HealthCheck    string
	HealthInterval time.Duration
	AutoRm         bool
	CreatedAt      time.Time
	Status         state.TaskStatus
	LogPath        string
}

// Public builds the redacted PublicTask projection of t. Adapters that
// expose tasks across a service boundary (HTTP, MCP) should use this
// instead of serializing t.Meta directly, since Meta.EnvOverrides holds
// raw environment variable values that must never leave the process.
func (t Task) Public() PublicTask {
	pt := PublicTask{
		ID:      t.ID,
		Status:  t.Status,
		LogPath: t.LogPath,
	}
	if t.Meta == nil {
		return pt
	}
	pt.Name = t.Meta.Name
	pt.Command = t.Meta.Command
	pt.Cwd = t.Meta.Cwd
	pt.Labels = t.Meta.Labels
	pt.Restart = t.Meta.Restart
	pt.RestartDelay = t.Meta.RestartDelay
	pt.HealthCheck = t.Meta.HealthCheck
	pt.HealthInterval = t.Meta.HealthInterval
	pt.AutoRm = t.Meta.AutoRm
	pt.CreatedAt = t.Meta.CreatedAt
	if len(t.Meta.EnvOverrides) > 0 {
		keys := make([]string, 0, len(t.Meta.EnvOverrides))
		for k := range t.Meta.EnvOverrides {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pt.EnvKeys = keys
	}
	return pt
}
