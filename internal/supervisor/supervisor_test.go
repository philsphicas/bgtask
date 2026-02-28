package supervisor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
)

// echoCmd returns a command that prints text to stdout.
func echoCmd(text string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo " + text}
	}
	return []string{"echo", text}
}

func TestRunChildExitsCleanly(t *testing.T) {
	dir := t.TempDir()
	store := &state.Store{Root: dir}

	meta := &state.Meta{
		ID:        state.GenerateID(),
		Name:      "test-clean",
		Command:   echoCmd("hello"),
		Cwd:       t.TempDir(),
		CreatedAt: time.Now(),
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cfg := &Config{
		StateDir: store.TaskDir(meta.ID),
		Meta:     meta,
		Store:    store,
	}

	if err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	exit, err := store.ReadExit(meta.ID)
	if err != nil {
		t.Fatalf("ReadExit: %v", err)
	}
	if exit == nil {
		t.Fatal("expected exit.json to be written")
	}
	if exit.Code != 0 {
		t.Errorf("exit code = %d, want 0", exit.Code)
	}

	entries := readLogEntries(t, store.OutputPath(meta.ID))
	foundHello := false
	for _, e := range entries {
		if e.Stream == "o" && strings.Contains(e.Data, "hello") {
			foundHello = true
		}
	}
	if !foundHello {
		t.Errorf("expected stdout entry containing 'hello' in log, got %v", entries)
	}

	foundExit := false
	for _, e := range entries {
		if e.Stream == "x" && e.Data == "exited" {
			foundExit = true
		}
	}
	if !foundExit {
		t.Errorf("expected lifecycle 'exited' event in log")
	}
}

func TestRunChildExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	store := &state.Store{Root: dir}

	// "exit 42" works in both sh and cmd.
	var cmd []string
	if runtime.GOOS == "windows" {
		cmd = []string{"cmd", "/c", "echo failing & exit /b 42"}
	} else {
		cmd = []string{"sh", "-c", "echo failing; exit 42"}
	}

	meta := &state.Meta{
		ID:        state.GenerateID(),
		Name:      "test-fail",
		Command:   cmd,
		Cwd:       t.TempDir(),
		CreatedAt: time.Now(),
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cfg := &Config{
		StateDir: store.TaskDir(meta.ID),
		Meta:     meta,
		Store:    store,
	}

	if err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	exit, err := store.ReadExit(meta.ID)
	if err != nil {
		t.Fatalf("ReadExit: %v", err)
	}
	if exit.Code != 42 {
		t.Errorf("exit code = %d, want 42", exit.Code)
	}
}

func TestRunStderrCapture(t *testing.T) {
	dir := t.TempDir()
	store := &state.Store{Root: dir}

	var cmd []string
	if runtime.GOOS == "windows" {
		cmd = []string{"cmd", "/c", "echo out & echo err 1>&2"}
	} else {
		cmd = []string{"sh", "-c", "echo out; echo err >&2"}
	}

	meta := &state.Meta{
		ID:        state.GenerateID(),
		Name:      "test-stderr",
		Command:   cmd,
		Cwd:       t.TempDir(),
		CreatedAt: time.Now(),
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cfg := &Config{
		StateDir: store.TaskDir(meta.ID),
		Meta:     meta,
		Store:    store,
	}

	if err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readLogEntries(t, store.OutputPath(meta.ID))
	var foundOut, foundErr bool
	for _, e := range entries {
		if e.Stream == "o" && strings.Contains(e.Data, "out") {
			foundOut = true
		}
		if e.Stream == "e" && strings.Contains(e.Data, "err") {
			foundErr = true
		}
	}
	if !foundOut {
		t.Error("expected stdout containing 'out' in log")
	}
	if !foundErr {
		t.Error("expected stderr containing 'err' in log")
	}
}

func TestRunRestart(t *testing.T) {
	dir := t.TempDir()
	store := &state.Store{Root: dir}

	// Use a Go helper approach: write a cross-platform counter script.
	// The script reads a counter file, increments it, exits 1 until count >= 3.
	counterFile := filepath.Join(t.TempDir(), "counter")
	os.WriteFile(counterFile, []byte("0"), 0o600)

	// Use the Go binary itself as the test helper -- but that's complex.
	// Instead, write a portable script approach using the shell.
	var cmd []string
	if runtime.GOOS == "windows" {
		// PowerShell is more reliable than cmd for this on Windows.
		// But to keep deps minimal, use a simpler approach: just fail 3 times.
		// We can't easily do counter files in cmd, so use a different strategy:
		// write a small .bat file.
		batFile := filepath.Join(t.TempDir(), "counter.bat")
		batContent := fmt.Sprintf(`@echo off
set /p n=<%s
echo attempt %%n%%
set /a n=%%n%%+1
>%s echo %%n%%
if %%n%% GEQ 3 (exit /b 0) else (exit /b 1)
`, counterFile, counterFile)
		os.WriteFile(batFile, []byte(batContent), 0o600)
		cmd = []string{"cmd", "/c", batFile}
	} else {
		cmd = []string{"sh", "-c",
			fmt.Sprintf(`n=$(cat %s); echo "attempt $n"; n=$((n+1)); echo $n > %s; [ $n -ge 3 ] && exit 0 || exit 1`, counterFile, counterFile),
		}
	}

	meta := &state.Meta{
		ID:      state.GenerateID(),
		Name:    "test-restart",
		Command: cmd,
		Cwd:     t.TempDir(),
		Restart: "on-failure", CreatedAt: time.Now(),
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cfg := &Config{
		StateDir:     store.TaskDir(meta.ID),
		Meta:         meta,
		Store:        store,
		Restart:      "on-failure",
		RestartDelay: 100 * time.Millisecond,
	}

	if err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	exit, err := store.ReadExit(meta.ID)
	if err != nil {
		t.Fatalf("ReadExit: %v", err)
	}
	if exit.Code != 0 {
		t.Errorf("exit code = %d, want 0", exit.Code)
	}

	entries := readLogEntries(t, store.OutputPath(meta.ID))
	restartCount := 0
	for _, e := range entries {
		if e.Stream == "x" && e.Data == "restarting" {
			restartCount++
		}
	}
	if restartCount < 2 {
		t.Errorf("expected at least 2 restart events, got %d", restartCount)
	}
}

func TestRunEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	store := &state.Store{Root: dir}

	var cmd []string
	if runtime.GOOS == "windows" {
		cmd = []string{"cmd", "/c", "echo %MY_TEST_VAR%"}
	} else {
		cmd = []string{"sh", "-c", "echo $MY_TEST_VAR"}
	}

	meta := &state.Meta{
		ID:           state.GenerateID(),
		Name:         "test-env",
		Command:      cmd,
		Cwd:          t.TempDir(),
		EnvOverrides: map[string]string{"MY_TEST_VAR": "hello_from_bgtask"},
		CreatedAt:    time.Now(),
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cfg := &Config{
		StateDir: store.TaskDir(meta.ID),
		Meta:     meta,
		Store:    store,
	}

	if err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := readLogEntries(t, store.OutputPath(meta.ID))
	found := false
	for _, e := range entries {
		if e.Stream == "o" && strings.Contains(e.Data, "hello_from_bgtask") {
			found = true
		}
	}
	if !found {
		t.Error("expected env override value in output")
	}
}

func TestComputeDelay_FixedDelay(t *testing.T) {
	cfg := &Config{RestartDelay: 5 * time.Second}
	for attempt := 1; attempt <= 10; attempt++ {
		d := cfg.computeDelay(attempt)
		if d != 5*time.Second {
			t.Errorf("attempt %d: got %v, want 5s", attempt, d)
		}
	}
}

func TestComputeDelay_ExponentialBackoff(t *testing.T) {
	cfg := &Config{RestartDelay: 0}
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 32 * time.Second},
		{7, 60 * time.Second}, // 64s capped to 60s
		{8, 60 * time.Second}, // still capped
		{100, 60 * time.Second},
	}
	for _, tt := range tests {
		d := cfg.computeDelay(tt.attempt)
		if d != tt.want {
			t.Errorf("attempt %d: got %v, want %v", tt.attempt, d, tt.want)
		}
	}
}

func TestExitCodeFrom_NilError(t *testing.T) {
	code := exitCodeFrom(nil)
	if code != 0 {
		t.Errorf("exitCodeFrom(nil) = %d, want 0", code)
	}
}

func TestExitCodeFrom_NonExitError(t *testing.T) {
	code := exitCodeFrom(fmt.Errorf("some error"))
	if code != -1 {
		t.Errorf("exitCodeFrom(non-ExitError) = %d, want -1", code)
	}
}

func TestBuildCmd_NoEnvOverrides(t *testing.T) {
	meta := &state.Meta{
		Command: []string{"echo", "hello"},
		Cwd:     t.TempDir(),
	}
	cmd := buildCmd(meta)
	if cmd.Path == "" {
		t.Error("expected non-empty path")
	}
	// When no env overrides, cmd.Env should be nil (inherits parent).
	if cmd.Env != nil {
		t.Errorf("expected nil Env, got %d entries", len(cmd.Env))
	}
	if cmd.Dir != meta.Cwd {
		t.Errorf("Dir = %q, want %q", cmd.Dir, meta.Cwd)
	}
}

func TestBuildCmd_WithEnvOverrides(t *testing.T) {
	meta := &state.Meta{
		Command:      []string{"echo", "hello"},
		Cwd:          t.TempDir(),
		EnvOverrides: map[string]string{"MY_VAR": "my_value"},
	}
	cmd := buildCmd(meta)
	if cmd.Env == nil {
		t.Fatal("expected non-nil Env with overrides")
	}
	found := false
	for _, e := range cmd.Env {
		if e == "MY_VAR=my_value" {
			found = true
		}
	}
	if !found {
		t.Error("expected MY_VAR=my_value in Env")
	}
}

func TestBuildCmd_EnvOverrideReplacesExisting(t *testing.T) {
	// Set an env var and verify the override replaces it.
	t.Setenv("BGTASK_TEST_OVERRIDE", "original")

	meta := &state.Meta{
		Command:      []string{"echo"},
		Cwd:          t.TempDir(),
		EnvOverrides: map[string]string{"BGTASK_TEST_OVERRIDE": "replaced"},
	}
	cmd := buildCmd(meta)

	count := 0
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "BGTASK_TEST_OVERRIDE=") {
			count++
			if e != "BGTASK_TEST_OVERRIDE=replaced" {
				t.Errorf("expected override value, got %q", e)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 BGTASK_TEST_OVERRIDE entry, got %d", count)
	}
}

func TestBuildCmd_EmptyCommand(t *testing.T) {
	// buildCmd should not panic on empty Command.
	meta := &state.Meta{
		Command: []string{},
		Cwd:     t.TempDir(),
	}
	cmd := buildCmd(meta)
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
}

func TestJsonlWriter_WriteOutput(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.jsonl")
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	w := &jsonlWriter{f: f}
	w.WriteOutput("o", "hello world\n")
	_ = f.Close()

	data, err := os.ReadFile(tmpFile) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}

	var entry LogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("invalid JSON: %v\ndata: %s", err, string(data))
	}
	if entry.Stream != "o" {
		t.Errorf("Stream = %q, want 'o'", entry.Stream)
	}
	if entry.Data != "hello world\n" {
		t.Errorf("Data = %q, want 'hello world\\n'", entry.Data)
	}
	if entry.Time.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestJsonlWriter_WriteEvent(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.jsonl")
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	w := &jsonlWriter{f: f}
	code := 42
	attempt := 3
	w.WriteEvent("restarting", &code, &attempt, "5s")
	_ = f.Close()

	data, err := os.ReadFile(tmpFile) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}

	var entry LogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry.Stream != "x" {
		t.Errorf("Stream = %q, want 'x'", entry.Stream)
	}
	if entry.Data != "restarting" {
		t.Errorf("Data = %q, want 'restarting'", entry.Data)
	}
	if entry.Code == nil || *entry.Code != 42 {
		t.Errorf("Code = %v, want 42", entry.Code)
	}
	if entry.Attempt == nil || *entry.Attempt != 3 {
		t.Errorf("Attempt = %v, want 3", entry.Attempt)
	}
	if entry.Delay != "5s" {
		t.Errorf("Delay = %q, want '5s'", entry.Delay)
	}
}

func TestJsonlWriter_WriteHealthEvent(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.jsonl")
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	w := &jsonlWriter{f: f}
	w.WriteHealthEvent("health_fail", "connection refused")
	_ = f.Close()

	data, err := os.ReadFile(tmpFile) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}

	var entry LogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry.Data != "health_fail" {
		t.Errorf("Data = %q, want 'health_fail'", entry.Data)
	}
	if entry.Message != "connection refused" {
		t.Errorf("Message = %q, want 'connection refused'", entry.Message)
	}
}

func TestJsonlWriter_CloseAndWrite(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.jsonl")
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	w := &jsonlWriter{f: f}
	w.close(f)
	// Write after close should not panic.
	w.WriteOutput("o", "should be silently dropped\n")
}

func TestJsonlWriter_ConcurrentWrites(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.jsonl")
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	w := &jsonlWriter{f: f}

	// Write from multiple goroutines concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w.WriteOutput("o", fmt.Sprintf("line %d\n", n))
		}(i)
	}
	wg.Wait()
	_ = f.Close()

	// Verify: should have 50 valid JSON lines.
	data, err := os.ReadFile(tmpFile) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 50 {
		t.Errorf("expected 50 lines, got %d", len(lines))
	}
	for i, line := range lines {
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestRunAutoRm(t *testing.T) {
	dir := t.TempDir()
	store := &state.Store{Root: dir}

	meta := &state.Meta{
		ID:        state.GenerateID(),
		Name:      "test-auto-rm",
		Command:   echoCmd("goodbye"),
		Cwd:       t.TempDir(),
		AutoRm:    true,
		CreatedAt: time.Now(),
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cfg := &Config{
		StateDir: store.TaskDir(meta.ID),
		Meta:     meta,
		Store:    store,
	}

	if err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// With AutoRm, the task directory should be removed after clean exit.
	if _, err := os.Stat(store.TaskDir(meta.ID)); !os.IsNotExist(err) {
		t.Errorf("task directory should be removed with AutoRm, err: %v", err)
	}
}

func readLogEntries(t *testing.T, path string) []LogEntry {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test helper reading test output
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e LogEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}
