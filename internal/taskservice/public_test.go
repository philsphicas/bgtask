package taskservice_test

import (
	"strings"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestTaskPublic_RedactsEnvValues(t *testing.T) {
	meta := &state.Meta{
		ID:      "20250101T000000-abcd1234",
		Name:    "myapp",
		Command: []string{"./server", "--port", "8080"},
		Cwd:     "/srv/myapp",
		EnvOverrides: map[string]string{
			"API_TOKEN": "super-secret",
			"PORT":      "8080",
		},
		Labels:         []string{"prod"},
		Restart:        "always",
		RestartDelay:   5 * time.Second,
		HealthCheck:    "curl -f http://localhost/health",
		HealthInterval: 30 * time.Second,
		AutoRm:         true,
		CreatedAt:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	task := taskservice.Task{
		ID:      meta.ID,
		Meta:    meta,
		Status:  state.TaskStatus{State: "running"},
		LogPath: "/state/myapp/output.log",
	}

	pub := task.Public()

	if pub.ID != meta.ID {
		t.Errorf("ID = %q, want %q", pub.ID, meta.ID)
	}
	if pub.Name != meta.Name {
		t.Errorf("Name = %q, want %q", pub.Name, meta.Name)
	}
	if strings.Join(pub.Command, " ") != strings.Join(meta.Command, " ") {
		t.Errorf("Command = %v, want %v (command must remain visible)", pub.Command, meta.Command)
	}
	if pub.Cwd != meta.Cwd {
		t.Errorf("Cwd = %q, want %q (cwd must remain visible)", pub.Cwd, meta.Cwd)
	}
	if pub.LogPath != task.LogPath {
		t.Errorf("LogPath = %q, want %q (log path must remain visible)", pub.LogPath, task.LogPath)
	}

	wantKeys := []string{"API_TOKEN", "PORT"}
	if len(pub.EnvKeys) != len(wantKeys) {
		t.Fatalf("EnvKeys = %v, want %v", pub.EnvKeys, wantKeys)
	}
	for i, k := range wantKeys {
		if pub.EnvKeys[i] != k {
			t.Errorf("EnvKeys[%d] = %q, want %q", i, pub.EnvKeys[i], k)
		}
	}

	// The redacted projection must never expose the raw values -- there is
	// no field on PublicTask that could hold them, but assert the type
	// doesn't accidentally carry the map through some other path.
	for _, v := range []string{"super-secret", "8080"} {
		if v == "8080" {
			// "8080" also appears in Command/EnvKeys legitimately (as a
			// visible arg and as a key name coincidentally matching the
			// PORT value's digits is not the case here); only check that
			// the literal secret value is absent.
			continue
		}
		if strings.Contains(pub.HealthCheck, v) || strings.Contains(pub.Cwd, v) {
			t.Errorf("secret value %q leaked into a visible field", v)
		}
	}
}

func TestTaskPublic_NilMeta(t *testing.T) {
	task := taskservice.Task{ID: "abc", Status: state.TaskStatus{State: "unknown"}, LogPath: "/x/output.log"}
	pub := task.Public()
	if pub.ID != "abc" {
		t.Errorf("ID = %q, want %q", pub.ID, "abc")
	}
	if pub.Name != "" || pub.Cwd != "" || len(pub.Command) != 0 || len(pub.EnvKeys) != 0 {
		t.Errorf("expected zero-value fields for nil Meta, got %+v", pub)
	}
}

func TestTaskPublic_NoEnvOverrides(t *testing.T) {
	task := taskservice.Task{
		ID:   "abc",
		Meta: &state.Meta{Name: "n", Command: []string{"x"}},
	}
	pub := task.Public()
	if pub.EnvKeys != nil {
		t.Errorf("EnvKeys = %v, want nil for a task with no env overrides", pub.EnvKeys)
	}
}
