package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var bgtaskBin string
var testConfigDir string
var coverDir string

// shellArgs returns args to run a shell snippet cross-platform.
func shellArgs(script string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", script}
	}
	return []string{"bash", "-c", script}
}

// longRunCmd returns a command that runs for a long time (for stop/pause tests).
func longRunCmd() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "ping -n 300 127.0.0.1 >nul"}
	}
	return []string{"sleep", "300"}
}

func TestMain(m *testing.M) {
	// Build the binary for testing. Use -cover so subprocess invocations
	// produce coverage data that can be merged with unit test coverage.
	tmpDir, err := os.MkdirTemp("", "bgtask-e2e")
	if err != nil {
		panic(err)
	}
	bgtaskBin = filepath.Join(tmpDir, "bgtask")
	if runtime.GOOS == "windows" {
		bgtaskBin += ".exe"
	}
	buildArgs := []string{"build", "-o", bgtaskBin}
	if os.Getenv("BGTASK_E2E_COVER") != "" {
		buildArgs = []string{"build", "-cover", "-o", bgtaskBin}
	}
	cmd := exec.Command("go", buildArgs...)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("build failed: " + string(out))
	}

	testConfigDir = filepath.Join(tmpDir, "config")
	coverDir = os.Getenv("GOCOVERDIR")

	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

func run(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(bgtaskBin, args...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+testConfigDir)
	if coverDir != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+coverDir)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bgtask %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

// tryRun runs bgtask and returns the output and whether it succeeded.
func tryRun(args ...string) (string, bool) {
	cmd := exec.Command(bgtaskBin, args...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+testConfigDir)
	if coverDir != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+coverDir)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// waitFor polls until check returns true, or fails the test after timeout.
func waitFor(t *testing.T, timeout time.Duration, desc string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", desc)
}

// waitForLog polls until the logs for a task contain the expected string.
func waitForLog(t *testing.T, name, content string) {
	t.Helper()
	waitFor(t, 5*time.Second, fmt.Sprintf("logs of %s to contain %q", name, content), func() bool {
		out, ok := tryRun("logs", name)
		return ok && strings.Contains(out, content)
	})
}

// waitForExit polls until a task has exited.
func waitForExit(t *testing.T, name string) {
	t.Helper()
	waitFor(t, 5*time.Second, fmt.Sprintf("task %s to exit", name), func() bool {
		out, ok := tryRun("status", name, "--json")
		return ok && strings.Contains(out, "exit_code")
	})
}

// waitForRunning polls until a task is running.
func waitForRunning(t *testing.T, name string) {
	t.Helper()
	waitFor(t, 10*time.Second, fmt.Sprintf("task %s to be running", name), func() bool {
		out, ok := tryRun("ls")
		return ok && strings.Contains(out, name) && strings.Contains(out, "running")
	})
}

func TestE2E_RunAndList(t *testing.T) {
	t.Parallel()
	out := run(t, "run", "--name", "e2e-echo", "--", "echo", "hello")
	if !strings.Contains(out, "Started: e2e-echo") {
		t.Fatalf("expected 'Started: e2e-echo', got: %s", out)
	}

	waitFor(t, 5*time.Second, "e2e-echo in ls", func() bool {
		out, ok := tryRun("ls")
		return ok && strings.Contains(out, "e2e-echo")
	})

	run(t, "rm", "e2e-echo")
}

func TestE2E_LogsAndStatus(t *testing.T) {
	t.Parallel()
	args := append([]string{"run", "--name", "e2e-logs", "--"}, shellArgs("echo stdout-line && echo stderr-line 1>&2")...)
	run(t, args...)

	waitForExit(t, "e2e-logs")

	out := run(t, "logs", "e2e-logs")
	if !strings.Contains(out, "stdout-line") {
		t.Errorf("expected stdout-line in logs, got: %s", out)
	}
	if !strings.Contains(out, "stderr-line") {
		t.Errorf("expected stderr-line in logs, got: %s", out)
	}

	out = run(t, "logs", "e2e-logs", "--stderr")
	if !strings.Contains(out, "stderr-line") {
		t.Errorf("expected stderr-line in stderr-only logs, got: %s", out)
	}
	if strings.Contains(out, "stdout-line") {
		t.Errorf("stdout-line should not appear in stderr-only logs")
	}

	out = run(t, "status", "e2e-logs")
	if !strings.Contains(out, "Name:       e2e-logs") {
		t.Errorf("expected name in status, got: %s", out)
	}

	run(t, "rm", "e2e-logs")
}

func TestE2E_StopRunningTask(t *testing.T) {
	t.Parallel()
	args := append([]string{"run", "--name", "e2e-long", "--"}, longRunCmd()...)
	run(t, args...)
	waitForRunning(t, "e2e-long")

	run(t, "stop", "e2e-long")
	waitFor(t, 15*time.Second, "e2e-long stopped", func() bool {
		out, ok := tryRun("status", "e2e-long", "--json")
		return ok && !strings.Contains(out, `"supervisor_alive":true`)
	})

	run(t, "rm", "e2e-long")
}

func TestE2E_Rename(t *testing.T) {
	t.Parallel()
	run(t, "run", "--name", "e2e-old", "--", "echo", "hi")
	waitForExit(t, "e2e-old")

	run(t, "rename", "e2e-old", "e2e-new")

	out := run(t, "ls")
	if !strings.Contains(out, "e2e-new") {
		t.Errorf("expected renamed task in ls, got: %s", out)
	}
	if strings.Contains(out, "e2e-old") {
		t.Errorf("old name should not appear in ls")
	}

	run(t, "rm", "e2e-new")
}

func TestE2E_JSONOutput(t *testing.T) {
	t.Parallel()
	run(t, "run", "--name", "e2e-json", "--", "echo", "test")
	waitForExit(t, "e2e-json")

	out := run(t, "ls", "--json")
	var tasks []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("invalid JSON from ls --json: %v\n%s", err, out)
	}
	found := false
	for _, task := range tasks {
		if task["name"] == "e2e-json" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected e2e-json in JSON output, got: %s", out)
	}

	out = run(t, "status", "e2e-json", "--json")
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("invalid JSON from status --json: %v\n%s", err, out)
	}
	if status["name"] != "e2e-json" {
		t.Errorf("expected name in status JSON, got: %v", status["name"])
	}

	run(t, "rm", "e2e-json")
}

func TestE2E_Tags(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args := append([]string{"run", "--name", "e2e-tagged", "--tag", "tunnel", "--"}, longArgs...)
	run(t, args...)
	args = append([]string{"run", "--name", "e2e-untagged", "--"}, longArgs...)
	run(t, args...)
	waitForRunning(t, "e2e-tagged")
	waitForRunning(t, "e2e-untagged")

	out := run(t, "ls", "--tag", "tunnel")
	if !strings.Contains(out, "e2e-tagged") {
		t.Errorf("expected tagged task in filtered ls, got: %s", out)
	}
	if strings.Contains(out, "e2e-untagged") {
		t.Errorf("untagged task should not appear in filtered ls")
	}

	run(t, "stop", "--tag", "tunnel")
	waitFor(t, 15*time.Second, "tagged task stopped", func() bool {
		out, ok := tryRun("status", "e2e-tagged", "--json")
		return ok && !strings.Contains(out, `"supervisor_alive":true`)
	})

	out = run(t, "ls")
	// e2e-untagged should still be running.
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "e2e-untagged") && !strings.Contains(line, "running") {
			t.Errorf("untagged task should still be running: %s", line)
		}
	}

	run(t, "stop", "e2e-untagged")
	run(t, "rm", "e2e-tagged")
	run(t, "rm", "e2e-untagged")
}

func TestE2E_Dir(t *testing.T) {
	t.Parallel()
	tmpDir := os.TempDir()
	pwdArgs := shellArgs("cd")
	if runtime.GOOS != "windows" {
		pwdArgs = []string{"pwd"}
	}
	args := append([]string{"run", "--name", "e2e-dir", "--dir", tmpDir, "--"}, pwdArgs...)
	run(t, args...)
	waitForExit(t, "e2e-dir")

	out := run(t, "logs", "e2e-dir")
	if !strings.Contains(out, filepath.Clean(tmpDir)) {
		t.Errorf("expected %s in logs, got: %s", tmpDir, out)
	}

	run(t, "rm", "e2e-dir")
}

func TestE2E_Rm(t *testing.T) {
	t.Parallel()
	run(t, "run", "--name", "e2e-rm", "--", "echo", "bye")
	waitForExit(t, "e2e-rm")

	run(t, "rm", "e2e-rm")

	out := run(t, "ls")
	if strings.Contains(out, "e2e-rm") {
		t.Errorf("removed task should not appear in ls, got: %s", out)
	}
}

// runExpectFail runs bgtask and expects it to fail. Returns combined output.
func runExpectFail(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(bgtaskBin, args...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+testConfigDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bgtask %s should have failed but succeeded:\n%s", strings.Join(args, " "), string(out))
	}
	return string(out)
}

func TestE2E_PauseResume(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args := append([]string{"run", "--name", "e2e-pause", "--"}, longArgs...)
	run(t, args...)
	waitForRunning(t, "e2e-pause")

	// Pause.
	run(t, "pause", "e2e-pause")
	waitForLog(t, "e2e-pause", "paused")

	// Resume.
	run(t, "resume", "e2e-pause")
	waitForLog(t, "e2e-pause", "resumed")

	// Should still be running.
	waitForRunning(t, "e2e-pause")

	run(t, "stop", "e2e-pause")
	run(t, "rm", "e2e-pause")
}

func TestE2E_Restart(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("restart test uses POSIX shell constructs")
	}
	// Command that fails a couple times then succeeds.
	counterFile := filepath.Join(t.TempDir(), "counter")
	os.WriteFile(counterFile, []byte("0"), 0o600)

	script := fmt.Sprintf(`n=$(cat %s); n=$((n+1)); echo $n > %s; [ $n -ge 3 ] && exit 0 || exit 1`, counterFile, counterFile)
	args := []string{"run", "--name", "e2e-restart", "--restart", "on-failure", "--", "sh", "-c", script}
	run(t, args...)

	// Wait for restarts to complete (3 attempts with backoff).
	waitFor(t, 15*time.Second, "restart task to exit", func() bool {
		out, ok := tryRun("status", "e2e-restart", "--json")
		return ok && strings.Contains(out, "exit_code")
	})

	out := run(t, "logs", "e2e-restart")
	if !strings.Contains(out, "restarting") {
		t.Errorf("expected 'restarting' events in logs, got: %s", out)
	}

	out = run(t, "status", "e2e-restart", "--json")
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("invalid JSON from status: %v\n%s", err, out)
	}
	if code, ok := status["exit_code"].(float64); !ok || code != 0 {
		t.Errorf("expected exit code 0, got: %v", status["exit_code"])
	}

	run(t, "rm", "e2e-restart")
}

func TestE2E_EphemeralRm(t *testing.T) {
	t.Parallel()
	args := []string{"run", "--name", "e2e-ephemeral", "--rm", "--", "echo", "ephemeral"}
	run(t, args...)
	waitFor(t, 5*time.Second, "ephemeral task removed", func() bool {
		out, ok := tryRun("ls")
		return ok && !strings.Contains(out, "e2e-ephemeral")
	})
}

func TestE2E_EnvOverride(t *testing.T) {
	t.Parallel()
	var script string
	if runtime.GOOS == "windows" {
		script = "echo %MY_TEST_KEY%"
	} else {
		script = "echo $MY_TEST_KEY"
	}
	args := append([]string{"run", "--name", "e2e-env", "--env", "MY_TEST_KEY=my_test_value", "--"},
		shellArgs(script)...)
	run(t, args...)
	waitForExit(t, "e2e-env")

	out := run(t, "logs", "e2e-env")
	if !strings.Contains(out, "my_test_value") {
		t.Errorf("expected env override value in logs, got: %s", out)
	}

	run(t, "rm", "e2e-env")
}

func TestE2E_ForceStop(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args := append([]string{"run", "--name", "e2e-force", "--"}, longArgs...)
	run(t, args...)
	waitForRunning(t, "e2e-force")

	run(t, "stop", "--force", "e2e-force")
	run(t, "rm", "e2e-force")
}

func TestE2E_DuplicateName(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args := append([]string{"run", "--name", "e2e-dup", "--"}, longArgs...)
	run(t, args...)
	waitForRunning(t, "e2e-dup")

	// Second run with same name should fail.
	out := runExpectFail(t, append([]string{"run", "--name", "e2e-dup", "--"}, longArgs...)...)
	if !strings.Contains(out, "already in use") {
		t.Errorf("expected 'already in use' error, got: %s", out)
	}

	run(t, "stop", "e2e-dup")
	run(t, "rm", "e2e-dup")
}

func TestE2E_RunNoCommand(t *testing.T) {
	t.Parallel()
	out := runExpectFail(t, "run", "--name", "e2e-nocmd", "--")
	if !strings.Contains(out, "provide a command") {
		t.Errorf("expected 'provide a command' error, got: %s", out)
	}
}
