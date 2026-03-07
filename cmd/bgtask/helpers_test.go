package main

import (
	"strings"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
)

func TestFormatDuration_Seconds(t *testing.T) {
	got := formatDuration(45 * time.Second)
	if got != "45s" {
		t.Errorf("formatDuration(45s) = %q, want %q", got, "45s")
	}
}

func TestFormatDuration_Zero(t *testing.T) {
	got := formatDuration(0)
	if got != "0s" {
		t.Errorf("formatDuration(0) = %q, want %q", got, "0s")
	}
}

func TestFormatDuration_Minutes(t *testing.T) {
	got := formatDuration(3*time.Minute + 15*time.Second)
	if got != "3m15s" {
		t.Errorf("formatDuration(3m15s) = %q, want %q", got, "3m15s")
	}
}

func TestFormatDuration_Hours(t *testing.T) {
	got := formatDuration(2*time.Hour + 30*time.Minute)
	if got != "2h30m" {
		t.Errorf("formatDuration(2h30m) = %q, want %q", got, "2h30m")
	}
}

func TestFormatDuration_Days(t *testing.T) {
	got := formatDuration(50 * time.Hour) // 2 days 2 hours
	if got != "2d2h" {
		t.Errorf("formatDuration(50h) = %q, want %q", got, "2d2h")
	}
}

func TestHasLabel_Found(t *testing.T) {
	if !hasLabel([]string{"tunnel", "prod"}, "tunnel") {
		t.Error("expected hasLabel to return true")
	}
}

func TestHasLabel_NotFound(t *testing.T) {
	if hasLabel([]string{"tunnel", "prod"}, "dev") {
		t.Error("expected hasLabel to return false")
	}
}

func TestHasLabel_EmptySlice(t *testing.T) {
	if hasLabel(nil, "tunnel") {
		t.Error("expected hasLabel to return false for nil slice")
	}
}

func TestFormatPorts_Empty(t *testing.T) {
	got := formatPorts(nil)
	if got != "-" {
		t.Errorf("formatPorts(nil) = %q, want %q", got, "-")
	}
}

func TestFormatPorts_Single(t *testing.T) {
	got := formatPorts([]uint32{8080})
	if got != ":8080" {
		t.Errorf("formatPorts([8080]) = %q, want %q", got, ":8080")
	}
}

func TestFormatPorts_Multiple(t *testing.T) {
	got := formatPorts([]uint32{8080, 9090})
	if got != ":8080,:9090" {
		t.Errorf("formatPorts([8080,9090]) = %q, want %q", got, ":8080,:9090")
	}
}

func TestFormatCommand(t *testing.T) {
	meta := &state.Meta{Command: []string{"ssh", "-D", "1080", "-N", "jumphost"}}
	got := formatCommand(meta)
	want := "ssh -D 1080 -N jumphost"
	if got != want {
		t.Errorf("formatCommand = %q, want %q", got, want)
	}
}

func TestTruncateCommand_Short(t *testing.T) {
	got := truncateCommand("echo hello", 80)
	if got != "echo hello" {
		t.Errorf("truncateCommand(short, 80) = %q, want %q", got, "echo hello")
	}
}

func TestTruncateCommand_ExactFit(t *testing.T) {
	cmd := "echo hello"
	got := truncateCommand(cmd, len(cmd))
	if got != cmd {
		t.Errorf("truncateCommand(exact, %d) = %q, want %q", len(cmd), got, cmd)
	}
}

func TestTruncateCommand_Truncated(t *testing.T) {
	cmd := "ssh -N -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/very/long/path"
	got := truncateCommand(cmd, 30)
	if len([]rune(got)) != 30 {
		t.Errorf("truncateCommand len = %d, want 30", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateCommand should end with …, got %q", got)
	}
	if got != "ssh -N -o StrictHostKeyChecki…" {
		t.Errorf("truncateCommand = %q, want %q", got, "ssh -N -o StrictHostKeyChecki…")
	}
}

func TestTruncateCommand_MinWidth(t *testing.T) {
	got := truncateCommand("long command string", 1)
	if got != "…" {
		t.Errorf("truncateCommand(s, 1) = %q, want %q", got, "…")
	}
}

func TestTruncateCommand_ZeroWidth(t *testing.T) {
	cmd := "echo hello"
	got := truncateCommand(cmd, 0)
	if got != cmd {
		t.Errorf("truncateCommand(s, 0) = %q, want original %q", got, cmd)
	}
}

func TestStyledAlive(t *testing.T) {
	if got := styledAlive(true); !strings.Contains(got, "running") {
		t.Errorf("styledAlive(true) = %q, want to contain 'running'", got)
	}
	if got := styledAlive(false); !strings.Contains(got, "dead") {
		t.Errorf("styledAlive(false) = %q, want to contain 'dead'", got)
	}
}

func TestStatusDisplayString(t *testing.T) {
	now := time.Now()
	twoMinAgo := now.Add(-2 * time.Minute)

	tests := []struct {
		name     string
		ts       state.TaskStatus
		contains string
	}{
		{"running with since", state.TaskStatus{State: "running", Running: &state.RunningInfo{Since: &twoMinAgo}}, "running (2m"},
		{"running no since", state.TaskStatus{State: "running"}, "running"},
		{"exited code 0", state.TaskStatus{State: "exited", Exited: &state.ExitedInfo{Code: 0, At: twoMinAgo}}, "exited(0) (2m"},
		{"exited code 1", state.TaskStatus{State: "exited", Exited: &state.ExitedInfo{Code: 1, At: twoMinAgo}}, "exited(1) (2m"},
		{"exited nil info", state.TaskStatus{State: "exited"}, "exited"},
		{"dead", state.TaskStatus{State: "dead"}, "dead"},
		{"unknown", state.TaskStatus{State: "unknown"}, "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := statusDisplayString(tc.ts)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("statusDisplayString() = %q, want to contain %q", got, tc.contains)
			}
		})
	}
}

func TestStatusState(t *testing.T) {
	tests := []struct {
		name string
		ts   state.TaskStatus
		want string
	}{
		{"running", state.TaskStatus{State: "running"}, "running"},
		{"exited 0", state.TaskStatus{State: "exited", Exited: &state.ExitedInfo{Code: 0}}, "exited(0)"},
		{"exited 1", state.TaskStatus{State: "exited", Exited: &state.ExitedInfo{Code: 1}}, "exited(1)"},
		{"exited nil", state.TaskStatus{State: "exited"}, "exited"},
		{"dead", state.TaskStatus{State: "dead"}, "dead"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusState(tc.ts); got != tc.want {
				t.Errorf("statusState() = %q, want %q", got, tc.want)
			}
		})
	}
}
