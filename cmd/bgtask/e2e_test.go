package main_test

import (
	"bytes"
	"context"
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

// longRunCmd returns a command that runs for a long time (for stop/restart tests).
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
// desc can reference variables that are updated by check for diagnostic output.
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
// Uses --all to include lifecycle events from across restarts.
func waitForLog(t *testing.T, name, content string) {
	t.Helper()
	waitFor(t, 5*time.Second, fmt.Sprintf("logs of %s to contain %q", name, content), func() bool {
		out, ok := tryRun("logs", "--all", name)
		return ok && strings.Contains(out, content)
	})
}

// waitForExit polls until a task has exited or the supervisor has died.
func waitForExit(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastOut string
	var lastOk bool
	for time.Now().Before(deadline) {
		out, ok := tryRun("status", name, "--json")
		lastOut = out
		lastOk = ok
		if ok && (strings.Contains(out, `"state": "exited"`) || strings.Contains(out, `"state": "dead"`)) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for task %s to exit (last status ok=%v output=%s)", name, lastOk, lastOut)
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

	out = run(t, "logs", "--stdout", "e2e-logs")
	if !strings.Contains(out, "stdout-line") {
		t.Errorf("expected stdout-line in stdout-only logs, got: %s", out)
	}
	if strings.Contains(out, "stderr-line") {
		t.Errorf("stderr-line should not appear in stdout-only logs")
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

	// Test --tail.
	out = run(t, "logs", "--tail", "1", "e2e-logs")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 1 {
		t.Errorf("--tail 1 should show at most 1 line, got %d: %s", len(lines), out)
	}

	// Test --tail 0 shows nothing.
	out = run(t, "logs", "--tail", "0", "e2e-logs")
	if strings.TrimSpace(out) != "" {
		t.Errorf("--tail 0 should show no output, got: %s", out)
	}

	// Test --since with a large window (should show all).
	out = run(t, "logs", "--since", "1h", "e2e-logs")
	if !strings.Contains(out, "stdout-line") {
		t.Errorf("expected stdout-line in --since 1h logs, got: %s", out)
	}

	// Test --timestamps.
	out = run(t, "logs", "--timestamps", "e2e-logs")
	if !strings.Contains(out, "T") || !strings.Contains(out, "Z") {
		t.Errorf("expected ISO timestamps in output, got: %s", out)
	}

	run(t, "rm", "e2e-logs")
}

func TestE2E_LogsFollow(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("logs follow test uses POSIX shell constructs")
	}
	args := append([]string{"run", "--name", "e2e-follow", "--"},
		"sh", "-c", "for i in 1 2 3; do echo line-$i; sleep 1; done")
	run(t, args...)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logsCmd := exec.CommandContext(ctx, bgtaskBin, "logs", "-f", "e2e-follow")
	logsCmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+testConfigDir)
	if coverDir != "" {
		logsCmd.Env = append(logsCmd.Env, "GOCOVERDIR="+coverDir)
	}
	var logsBuf bytes.Buffer
	logsCmd.Stdout = &logsBuf
	logsCmd.Stderr = &logsBuf

	if err := logsCmd.Start(); err != nil {
		t.Fatalf("failed to start logs -f: %v", err)
	}

	waitForExit(t, "e2e-follow")
	// Give follow mode a moment to flush remaining output.
	time.Sleep(500 * time.Millisecond)

	// Kill the follow process (it blocks until cancelled).
	logsCmd.Process.Kill()
	logsCmd.Wait() //nolint:errcheck

	out := logsBuf.String()
	for _, want := range []string{"line-1", "line-2", "line-3"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in follow output, got: %s", want, out)
		}
	}

	run(t, "rm", "e2e-follow")
}

func TestE2E_StopRunningTask(t *testing.T) {
	t.Parallel()
	args := append([]string{"run", "--name", "e2e-long", "--"}, longRunCmd()...)
	run(t, args...)
	waitForRunning(t, "e2e-long")

	run(t, "stop", "e2e-long")
	waitFor(t, 30*time.Second, "e2e-long stopped", func() bool {
		out, ok := tryRun("status", "e2e-long", "--json")
		return ok && (strings.Contains(out, `"state": "exited"`) || strings.Contains(out, `"state": "dead"`))
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
			// Verify structured status in ls --json.
			statusObj, ok := task["status"].(map[string]interface{})
			if !ok {
				t.Errorf("expected status to be an object, got: %T", task["status"])
			} else if statusObj["state"] != "exited" {
				t.Errorf("expected status.state to be 'exited', got: %v", statusObj["state"])
			}
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
	// Verify structured status in status --json.
	statusObj, ok := status["status"].(map[string]interface{})
	if !ok {
		t.Errorf("expected status to be an object, got: %T", status["status"])
	} else {
		if statusObj["state"] != "exited" {
			t.Errorf("expected status.state to be 'exited', got: %v", statusObj["state"])
		}
		exitedObj, _ := statusObj["exited"].(map[string]interface{})
		if exitedObj == nil {
			t.Errorf("expected status.exited to be an object")
		} else if _, ok := exitedObj["code"]; !ok {
			t.Errorf("expected status.exited.code to be present")
		}
	}

	run(t, "rm", "e2e-json")
}

func TestE2E_Labels(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args := append([]string{"run", "--name", "e2e-tagged", "--labels", "tunnel", "--"}, longArgs...)
	run(t, args...)
	args = append([]string{"run", "--name", "e2e-untagged", "--"}, longArgs...)
	run(t, args...)
	waitForRunning(t, "e2e-tagged")
	waitForRunning(t, "e2e-untagged")

	out := run(t, "ls", "--labels", "tunnel")
	if !strings.Contains(out, "e2e-tagged") {
		t.Errorf("expected tagged task in filtered ls, got: %s", out)
	}
	if strings.Contains(out, "e2e-untagged") {
		t.Errorf("untagged task should not appear in filtered ls")
	}

	run(t, "stop", "--labels", "tunnel")
	waitFor(t, 30*time.Second, "tagged task stopped", func() bool {
		out, ok := tryRun("status", "e2e-tagged", "--json")
		return ok && (strings.Contains(out, `"state": "exited"`) || strings.Contains(out, `"state": "dead"`))
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
	run(t, "run", "--name", "e2e-rmbasic", "--", "echo", "bye")
	waitForExit(t, "e2e-rmbasic")

	run(t, "rm", "e2e-rmbasic")

	out := run(t, "ls")
	if strings.Contains(out, "e2e-rmbasic") {
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

func TestE2E_RestartPolicy(t *testing.T) {
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
	waitFor(t, 30*time.Second, "restart task to exit", func() bool {
		out, ok := tryRun("status", "e2e-restart", "--json")
		return ok && (strings.Contains(out, `"state": "exited"`) || strings.Contains(out, `"state": "dead"`))
	})

	out := run(t, "logs", "--all", "e2e-restart")
	if !strings.Contains(out, "restarting") {
		t.Errorf("expected 'restarting' events in logs, got: %s", out)
	}

	out = run(t, "status", "e2e-restart", "--json")
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("invalid JSON from status: %v\n%s", err, out)
	}
	// Navigate the structured status: status.status.exited.code
	statusObj, _ := status["status"].(map[string]interface{})
	exitedObj, _ := statusObj["exited"].(map[string]interface{})
	if code, ok := exitedObj["code"].(float64); !ok || code != 0 {
		t.Errorf("expected exit code 0, got: %v", exitedObj["code"])
	}

	run(t, "rm", "e2e-restart")
}

func TestE2E_RestartCommand(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args := append([]string{"run", "--name", "e2e-rstcmd", "--"}, longArgs...)
	run(t, args...)
	waitForRunning(t, "e2e-rstcmd")

	run(t, "restart", "e2e-rstcmd")
	waitForLog(t, "e2e-rstcmd", "restarted")

	// Should still be running after restart.
	waitForRunning(t, "e2e-rstcmd")

	run(t, "stop", "e2e-rstcmd")
	run(t, "rm", "e2e-rstcmd")
}

func TestE2E_RestartByLabel(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args1 := append([]string{"run", "--name", "e2e-rsttag1", "--labels", "rstgrp", "--"}, longArgs...)
	args2 := append([]string{"run", "--name", "e2e-rsttag2", "--labels", "rstgrp", "--"}, longArgs...)
	run(t, args1...)
	run(t, args2...)
	waitForRunning(t, "e2e-rsttag1")
	waitForRunning(t, "e2e-rsttag2")

	run(t, "restart", "--labels", "rstgrp")
	waitForLog(t, "e2e-rsttag1", "restarted")
	waitForLog(t, "e2e-rsttag2", "restarted")

	// Both should still be running after restart.
	waitForRunning(t, "e2e-rsttag1")
	waitForRunning(t, "e2e-rsttag2")

	run(t, "stop", "e2e-rsttag1", "e2e-rsttag2")
	run(t, "rm", "e2e-rsttag1", "e2e-rsttag2")
}

func TestE2E_StartStoppedTask(t *testing.T) {
	t.Parallel()
	// Run a task that exits quickly.
	run(t, "run", "--name", "e2e-start", "--", "echo", "first-run")
	waitForExit(t, "e2e-start")

	// Re-start it.
	run(t, "start", "e2e-start")

	// The task runs "echo first-run" again via a new supervisor.
	// Wait until it appears twice in the logs to confirm the second run happened.
	waitFor(t, 5*time.Second, "e2e-start to re-run", func() bool {
		out, ok := tryRun("logs", "--all", "e2e-start")
		return ok && strings.Count(out, "first-run") >= 2
	})

	run(t, "rm", "e2e-start")
}

func TestE2E_StartByLabel(t *testing.T) {
	t.Parallel()
	run(t, "run", "--name", "e2e-starttag1", "--labels", "startgrp", "--", "echo", "run1")
	run(t, "run", "--name", "e2e-starttag2", "--labels", "startgrp", "--", "echo", "run2")
	waitForExit(t, "e2e-starttag1")
	waitForExit(t, "e2e-starttag2")

	// Start all stopped tasks with the label.
	run(t, "start", "--labels", "startgrp")

	// Both should run again.
	waitFor(t, 10*time.Second, "e2e-starttag1 to re-run", func() bool {
		out, ok := tryRun("logs", "--all", "e2e-starttag1")
		return ok && strings.Count(out, "run1") >= 2
	})
	waitFor(t, 10*time.Second, "e2e-starttag2 to re-run", func() bool {
		out, ok := tryRun("logs", "--all", "e2e-starttag2")
		return ok && strings.Count(out, "run2") >= 2
	})

	run(t, "rm", "e2e-starttag1", "e2e-starttag2")
}

func TestE2E_RmForce(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args := append([]string{"run", "--name", "e2e-force-rm", "--"}, longArgs...)
	run(t, args...)
	waitForRunning(t, "e2e-force-rm")

	run(t, "rm", "--force", "e2e-force-rm")

	out := run(t, "ls")
	if strings.Contains(out, "e2e-force-rm") {
		t.Errorf("force-removed task should not appear in ls, got: %s", out)
	}
}

func TestE2E_RmByLabel(t *testing.T) {
	t.Parallel()
	run(t, "run", "--name", "e2e-rmtag1", "--labels", "rmtest", "--", "echo", "bye1")
	run(t, "run", "--name", "e2e-rmtag2", "--labels", "rmtest", "--", "echo", "bye2")
	waitForExit(t, "e2e-rmtag1")
	waitForExit(t, "e2e-rmtag2")

	run(t, "rm", "--labels", "rmtest")

	out := run(t, "ls")
	if strings.Contains(out, "e2e-rmtag1") || strings.Contains(out, "e2e-rmtag2") {
		t.Errorf("rm --label should have removed both tasks, got: %s", out)
	}
}

func TestE2E_RmAll(t *testing.T) {
	// Not parallel: --all affects all tasks.
	localConfigDir := t.TempDir()
	localRun := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bgtaskBin, args...)
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+localConfigDir)
		if coverDir != "" {
			cmd.Env = append(cmd.Env, "GOCOVERDIR="+coverDir)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bgtask %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
		}
		return string(out)
	}

	localRun(t, "run", "--name", "e2e-rmall1", "--", "echo", "a")
	localRun(t, "run", "--name", "e2e-rmall2", "--", "echo", "b")
	time.Sleep(500 * time.Millisecond)

	localRun(t, "rm", "--all")

	out := localRun(t, "ls")
	if strings.Contains(out, "e2e-rmall") {
		t.Errorf("rm --all should have removed all tasks, got: %s", out)
	}
}

func TestE2E_StopByLabel(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	args1 := append([]string{"run", "--name", "e2e-sa1", "--labels", "stopall", "--"}, longArgs...)
	args2 := append([]string{"run", "--name", "e2e-sa2", "--labels", "stopall", "--"}, longArgs...)
	run(t, args1...)
	run(t, args2...)
	waitForRunning(t, "e2e-sa1")
	waitForRunning(t, "e2e-sa2")

	run(t, "stop", "--labels", "stopall")
	waitForExit(t, "e2e-sa1")
	waitForExit(t, "e2e-sa2")

	run(t, "rm", "e2e-sa1")
	run(t, "rm", "e2e-sa2")
}

func TestE2E_StopAll(t *testing.T) {
	// Not parallel: --all affects all tasks in the shared config dir.
	localConfigDir := t.TempDir()
	localRun := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bgtaskBin, args...)
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+localConfigDir)
		if coverDir != "" {
			cmd.Env = append(cmd.Env, "GOCOVERDIR="+coverDir)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bgtask %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
		}
		return string(out)
	}
	localTryRun := func(args ...string) (string, bool) {
		cmd := exec.Command(bgtaskBin, args...)
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+localConfigDir)
		if coverDir != "" {
			cmd.Env = append(cmd.Env, "GOCOVERDIR="+coverDir)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}

	longArgs := longRunCmd()
	args1 := append([]string{"run", "--name", "e2e-all1", "--"}, longArgs...)
	args2 := append([]string{"run", "--name", "e2e-all2", "--"}, longArgs...)
	localRun(t, args1...)
	localRun(t, args2...)

	waitFor(t, 10*time.Second, "both tasks running", func() bool {
		out, ok := localTryRun("ls")
		return ok && strings.Contains(out, "e2e-all1") && strings.Contains(out, "e2e-all2") && strings.Count(out, "running") >= 2
	})

	localRun(t, "stop", "--all")
	waitFor(t, 30*time.Second, "all tasks stopped", func() bool {
		out, ok := localTryRun("ls")
		if !ok {
			return false
		}
		return !strings.Contains(out, "running")
	})

	localRun(t, "rm", "e2e-all1")
	localRun(t, "rm", "e2e-all2")
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

	// Second run with same name should replace the existing task.
	run(t, append([]string{"run", "--name", "e2e-dup", "--"}, longArgs...)...)
	waitForRunning(t, "e2e-dup")

	// Should still be listed exactly once.
	out := run(t, "ls")
	if count := strings.Count(out, "e2e-dup"); count != 1 {
		t.Errorf("expected exactly 1 e2e-dup in ls, got %d: %s", count, out)
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

func TestE2E_NoTrunc(t *testing.T) {
	t.Parallel()

	// Launch a task with a deliberately long command.
	longArg := strings.Repeat("x", 200)
	run(t, "run", "--name", "e2e-notrunc", "--", "echo", longArg)
	waitForExit(t, "e2e-notrunc")

	// Helper to run ls with extra env vars.
	lsWithEnv := func(extraEnv []string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bgtaskBin, args...)
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+testConfigDir)
		if coverDir != "" {
			cmd.Env = append(cmd.Env, "GOCOVERDIR="+coverDir)
		}
		cmd.Env = append(cmd.Env, extraEnv...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bgtask %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
		}
		return string(out)
	}

	// With a narrow COLUMNS, the command should be truncated.
	narrow := lsWithEnv([]string{"COLUMNS=80"}, "ls")
	if strings.Contains(narrow, longArg) {
		t.Errorf("expected command to be truncated at COLUMNS=80, but full command appeared")
	}
	if !strings.Contains(narrow, "…") {
		t.Errorf("expected truncated output to contain '…'")
	}

	// With --no-trunc, the full command should appear regardless of COLUMNS.
	full := lsWithEnv([]string{"COLUMNS=80"}, "ls", "--no-trunc")
	if !strings.Contains(full, longArg) {
		t.Errorf("expected --no-trunc to show full command, got: %s", full)
	}

	run(t, "rm", "e2e-notrunc")
}

func TestE2E_VariadicStopRm(t *testing.T) {
	t.Parallel()
	longArgs := longRunCmd()
	run(t, append([]string{"run", "--name", "e2e-var1", "--"}, longArgs...)...)
	run(t, append([]string{"run", "--name", "e2e-var2", "--"}, longArgs...)...)
	waitForRunning(t, "e2e-var1")
	waitForRunning(t, "e2e-var2")

	// Stop both with a single command.
	run(t, "stop", "e2e-var1", "e2e-var2")
	waitForExit(t, "e2e-var1")
	waitForExit(t, "e2e-var2")

	// Rm both with a single command.
	run(t, "rm", "e2e-var1", "e2e-var2")

	out := run(t, "ls")
	if strings.Contains(out, "e2e-var1") || strings.Contains(out, "e2e-var2") {
		t.Errorf("variadic rm should have removed both tasks, got: %s", out)
	}
}

func TestE2E_LabelCommand(t *testing.T) {
	t.Parallel()
	run(t, "run", "--name", "e2e-label", "--", "echo", "hi")
	waitForExit(t, "e2e-label")

	run(t, "label", "e2e-label", "dev", "api")

	out := run(t, "ls", "--labels", "dev", "-w")
	if !strings.Contains(out, "e2e-label") {
		t.Errorf("labeled task should appear in filtered ls, got: %s", out)
	}

	// Clear labels
	run(t, "label", "e2e-label")
	out = run(t, "ls", "--labels", "dev", "-w")
	if strings.Contains(out, "e2e-label") {
		t.Errorf("cleared labels should not match filter, got: %s", out)
	}

	run(t, "rm", "e2e-label")
}

func TestE2E_LabelValidation(t *testing.T) {
	t.Parallel()

	// Invalid label on run should fail.
	out, ok := tryRun("run", "--name", "e2e-badlabel", "--labels", "123", "--", "echo", "hi")
	if ok {
		t.Fatalf("run with numeric label should fail, got: %s", out)
	}
	if !strings.Contains(out, "invalid label") {
		t.Errorf("expected 'invalid label' error, got: %s", out)
	}

	// Invalid label with spaces should fail.
	out, ok = tryRun("run", "--name", "e2e-badlabel", "--labels", "has space", "--", "echo", "hi")
	if ok {
		t.Fatalf("run with space in label should fail, got: %s", out)
	}

	// Valid label on run should succeed.
	run(t, "run", "--name", "e2e-validlabel", "--labels", "project:myapp", "--", "echo", "hi")
	waitForExit(t, "e2e-validlabel")

	// Invalid label on label command should fail.
	out, ok = tryRun("label", "e2e-validlabel", "good", "123bad")
	if ok {
		t.Fatalf("label with numeric-start should fail, got: %s", out)
	}

	run(t, "rm", "e2e-validlabel")
}

func TestE2E_PositionalNameRejected(t *testing.T) {
	t.Parallel()
	out, ok := tryRun("run", "e2e-posname", "--", "echo", "positional")
	if ok {
		t.Fatalf("positional name should be rejected, got: %s", out)
	}
	if !strings.Contains(out, "provide a command after --") {
		t.Errorf("expected error about --, got: %s", out)
	}
}
