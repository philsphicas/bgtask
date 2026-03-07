package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreCreateAndRead(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:        GenerateID(),
		Name:      "test-task",
		Command:   []string{"echo", "hello"},
		Cwd:       "/tmp",
		CreatedAt: time.Now(),
	}

	if err := s.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.ReadMeta(meta.ID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if got.Name != "test-task" {
		t.Errorf("Name = %q, want %q", got.Name, "test-task")
	}
	if len(got.Command) != 2 || got.Command[0] != "echo" {
		t.Errorf("Command = %v, want [echo hello]", got.Command)
	}
}

func TestStoreResolveByName(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:        GenerateID(),
		Name:      "my-tunnel",
		Command:   []string{"ssh", "-D", "1080"},
		Cwd:       "/home/user",
		CreatedAt: time.Now(),
	}
	if err := s.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Resolve by name.
	id, got, err := s.Resolve("my-tunnel")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id != meta.ID {
		t.Errorf("Resolve ID = %q, want %q", id, meta.ID)
	}
	if got.Name != "my-tunnel" {
		t.Errorf("Resolve Name = %q, want %q", got.Name, "my-tunnel")
	}

	// Resolve by ID.
	id2, _, err := s.Resolve(meta.ID)
	if err != nil {
		t.Fatalf("Resolve by ID: %v", err)
	}
	if id2 != meta.ID {
		t.Errorf("Resolve by ID = %q, want %q", id2, meta.ID)
	}
}

func TestStoreResolveNotFound(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	_, _, err := s.Resolve("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestStoreRename(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:        GenerateID(),
		Name:      "old-name",
		Command:   []string{"sleep", "100"},
		Cwd:       "/tmp",
		CreatedAt: time.Now(),
	}
	if err := s.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Rename(meta.ID, "new-name"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	got, err := s.ReadMeta(meta.ID)
	if err != nil {
		t.Fatalf("ReadMeta after rename: %v", err)
	}
	if got.Name != "new-name" {
		t.Errorf("Name = %q, want %q", got.Name, "new-name")
	}

	// Directory should not have changed.
	if _, err := os.Stat(filepath.Join(dir, meta.ID)); err != nil {
		t.Errorf("directory should still exist: %v", err)
	}
}

func TestStorePIDReadWrite(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:        GenerateID(),
		Name:      "pid-test",
		Command:   []string{"sleep", "10"},
		Cwd:       "/tmp",
		CreatedAt: time.Now(),
	}
	if err := s.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.WritePID(meta.ID, "supervisor.pid", 12345); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	pid, err := s.ReadPID(meta.ID, "supervisor.pid")
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != 12345 {
		t.Errorf("PID = %d, want 12345", pid)
	}
}

func TestStoreExitReadWrite(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:        GenerateID(),
		Name:      "exit-test",
		Command:   []string{"false"},
		Cwd:       "/tmp",
		CreatedAt: time.Now(),
	}
	if err := s.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// No exit.json initially.
	ex, err := s.ReadExit(meta.ID)
	if err != nil {
		t.Fatalf("ReadExit (missing): %v", err)
	}
	if ex != nil {
		t.Errorf("expected nil exit, got %+v", ex)
	}

	// Write exit.
	exitInfo := &Exit{Code: 1, ExitedAt: time.Now()}
	if err := s.WriteExit(meta.ID, exitInfo); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}

	ex, err = s.ReadExit(meta.ID)
	if err != nil {
		t.Fatalf("ReadExit: %v", err)
	}
	if ex.Code != 1 {
		t.Errorf("exit code = %d, want 1", ex.Code)
	}
}

func TestStoreListIDs(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	// Create two tasks.
	for _, name := range []string{"task-a", "task-b"} {
		meta := &Meta{
			ID:        GenerateID(),
			Name:      name,
			Command:   []string{"echo"},
			Cwd:       "/tmp",
			CreatedAt: time.Now(),
		}
		if err := s.Create(meta); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	ids, err := s.ListIDs()
	if err != nil {
		t.Fatalf("ListIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("len(ids) = %d, want 2", len(ids))
	}
}

func TestAutoName(t *testing.T) {
	name := AutoName([]string{"/usr/bin/ssh", "-D", "1080"})
	if len(name) < 5 {
		t.Errorf("auto name too short: %q", name)
	}
	// Should start with "ssh-".
	if name[:4] != "ssh-" {
		t.Errorf("auto name should start with ssh-, got %q", name)
	}

	// No command falls back to "task-".
	name = AutoName(nil)
	if name[:5] != "task-" {
		t.Errorf("empty command auto name should start with task-, got %q", name)
	}
}

func TestIsNameTaken(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:        GenerateID(),
		Name:      "taken",
		Command:   []string{"echo"},
		Cwd:       "/tmp",
		CreatedAt: time.Now(),
	}
	if err := s.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	taken, err := s.IsNameTaken("taken")
	if err != nil {
		t.Fatalf("IsNameTaken: %v", err)
	}
	if !taken {
		t.Error("expected name to be taken")
	}

	taken, err = s.IsNameTaken("not-taken")
	if err != nil {
		t.Fatalf("IsNameTaken: %v", err)
	}
	if taken {
		t.Error("expected name to not be taken")
	}
}

func TestGenerateID_Format(t *testing.T) {
	id := GenerateID()
	// ID format: YYYYMMDDTHHMMSS-XXXXXXXX (24 chars).
	if len(id) != 24 {
		t.Errorf("expected 24-char ID, got %d: %q", len(id), id)
	}
	if id[8] != 'T' {
		t.Errorf("expected 'T' at position 8, got %c in %q", id[8], id)
	}
	if id[15] != '-' {
		t.Errorf("expected '-' at position 15, got %c in %q", id[15], id)
	}
}

func TestGenerateID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateID()
		if seen[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestLock_BasicAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	unlock, err := s.Lock()
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// Lock file should exist.
	lockPath := filepath.Join(dir, ".lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file should exist while locked: %v", err)
	}

	unlock()

	// Lock file should be gone after unlock.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file should be removed after unlock")
	}
}

func TestLock_Contention(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	unlock1, err := s.Lock()
	if err != nil {
		t.Fatalf("Lock 1: %v", err)
	}

	// Release the first lock after a short delay, then second lock should succeed.
	done := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		unlock1()
	}()

	go func() {
		unlock2, err := s.Lock()
		if err != nil {
			done <- err
			return
		}
		unlock2()
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Lock 2 failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Lock 2 timed out (deadlock?)")
	}
}

func TestLock_StaleLockRemoval(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	// Create a stale lock file with an old modification time.
	lockPath := filepath.Join(dir, ".lock")
	if err := os.WriteFile(lockPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	// Set mtime to 1 minute ago (> lockStaleDuration of 30s).
	staleTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lockPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	// Lock should succeed by removing the stale lock.
	unlock, err := s.Lock()
	if err != nil {
		t.Fatalf("Lock should succeed after stale lock removal: %v", err)
	}
	unlock()
}

func TestWriteCreateTime_ReadCreateTime(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:      GenerateID(),
		Name:    "ct-test",
		Command: []string{"echo"},
		Cwd:     "/tmp",
	}
	if err := s.Create(meta); err != nil {
		t.Fatal(err)
	}

	var ct int64 = 1709312345678
	if err := s.WriteCreateTime(meta.ID, ct); err != nil {
		t.Fatalf("WriteCreateTime: %v", err)
	}

	got := s.ReadCreateTime(meta.ID)
	if got != ct {
		t.Errorf("ReadCreateTime = %d, want %d", got, ct)
	}
}

func TestReadCreateTime_Missing(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	got := s.ReadCreateTime("nonexistent")
	if got != 0 {
		t.Errorf("ReadCreateTime for missing file = %d, want 0", got)
	}
}

func TestReadCreateTime_Invalid(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:      GenerateID(),
		Name:    "ct-invalid",
		Command: []string{"echo"},
		Cwd:     "/tmp",
	}
	if err := s.Create(meta); err != nil {
		t.Fatal(err)
	}

	// Write invalid content.
	p := filepath.Join(s.TaskDir(meta.ID), "createtime")
	if err := os.WriteFile(p, []byte("not-a-number"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := s.ReadCreateTime(meta.ID)
	if got != 0 {
		t.Errorf("ReadCreateTime for invalid content = %d, want 0", got)
	}
}

func TestOutputPath(t *testing.T) {
	s := &Store{Root: "/tmp/bgtask/procs"}
	got := s.OutputPath("test-id")
	want := filepath.Join("/tmp/bgtask/procs", "test-id", "output.jsonl")
	if got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}

func TestTaskDir(t *testing.T) {
	s := &Store{Root: "/tmp/bgtask/procs"}
	got := s.TaskDir("test-id")
	want := filepath.Join("/tmp/bgtask/procs", "test-id")
	if got != want {
		t.Errorf("TaskDir = %q, want %q", got, want)
	}
}

func TestListIDs_IgnoresFiles(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	// Create a regular file (not a directory) in the store root.
	if err := os.WriteFile(filepath.Join(dir, "not-a-task"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Create a real task directory.
	meta := &Meta{
		ID:        GenerateID(),
		Name:      "real-task",
		Command:   []string{"echo"},
		Cwd:       "/tmp",
		CreatedAt: time.Now(),
	}
	if err := s.Create(meta); err != nil {
		t.Fatal(err)
	}

	ids, err := s.ListIDs()
	if err != nil {
		t.Fatalf("ListIDs: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("expected 1 ID (ignoring files), got %d: %v", len(ids), ids)
	}
}

func TestReadMeta_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	id := "corrupt-meta"
	taskDir := filepath.Join(dir, id)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "meta.json"), []byte("{invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := s.ReadMeta(id)
	if err == nil {
		t.Fatal("expected error for corrupt meta.json")
	}
}

func TestReadExit_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	id := "corrupt-exit"
	taskDir := filepath.Join(dir, id)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "exit.json"), []byte("{invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := s.ReadExit(id)
	if err == nil {
		t.Fatal("expected error for corrupt exit.json")
	}
}

func TestRename_NonexistentID(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	err := s.Rename("nonexistent-id", "new-name")
	if err == nil {
		t.Fatal("expected error for nonexistent ID")
	}
}

func TestRotateLogMaxFilesDropped(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}
	id := "test-max-rotate"
	taskDir := filepath.Join(dir, id)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}

	logPath := s.OutputPath(id)

	// Create .1, .2, .3 and current log.
	if err := os.WriteFile(logPath+".1", []byte("rot-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath+".2", []byte("rot-2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath+".3", []byte("rot-3-should-be-dropped"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.RotateLog(id); err != nil {
		t.Fatalf("RotateLog: %v", err)
	}

	// .1 should have "current"
	data, err := os.ReadFile(logPath + ".1") //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "current" {
		t.Errorf(".1 should have 'current', got %q", string(data))
	}

	// .2 should have "rot-1"
	data, err = os.ReadFile(logPath + ".2") //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "rot-1" {
		t.Errorf(".2 should have 'rot-1', got %q", string(data))
	}

	// .3 should have "rot-2" (old .3 was dropped)
	data, err = os.ReadFile(logPath + ".3") //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "rot-2" {
		t.Errorf(".3 should have 'rot-2', got %q", string(data))
	}
}

func TestWriteChildStartTime_ReadChildStartTime(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:      GenerateID(),
		Name:    "cst-test",
		Command: []string{"echo"},
		Cwd:     "/tmp",
	}
	if err := s.Create(meta); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Nanosecond)
	if err := s.WriteChildStartTime(meta.ID, now); err != nil {
		t.Fatalf("WriteChildStartTime: %v", err)
	}

	got := s.ReadChildStartTime(meta.ID)
	if !got.Equal(now) {
		t.Errorf("ReadChildStartTime = %v, want %v", got, now)
	}
}

func TestReadChildStartTime_Missing(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	got := s.ReadChildStartTime("nonexistent")
	if !got.IsZero() {
		t.Errorf("ReadChildStartTime for missing file = %v, want zero", got)
	}
}

func TestClearExit_Existing(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:      GenerateID(),
		Name:    "clear-exit-test",
		Command: []string{"false"},
		Cwd:     "/tmp",
	}
	if err := s.Create(meta); err != nil {
		t.Fatal(err)
	}

	exitInfo := &Exit{Code: 1, ExitedAt: time.Now()}
	if err := s.WriteExit(meta.ID, exitInfo); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}

	// exit.json should exist.
	ex, err := s.ReadExit(meta.ID)
	if err != nil {
		t.Fatalf("ReadExit: %v", err)
	}
	if ex == nil {
		t.Fatal("expected exit.json to exist before ClearExit")
	}

	if err := s.ClearExit(meta.ID); err != nil {
		t.Fatalf("ClearExit: %v", err)
	}

	// exit.json should be gone.
	ex, err = s.ReadExit(meta.ID)
	if err != nil {
		t.Fatalf("ReadExit after ClearExit: %v", err)
	}
	if ex != nil {
		t.Errorf("expected nil exit after ClearExit, got %+v", ex)
	}
}

func TestClearExit_Missing(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:      GenerateID(),
		Name:    "clear-exit-missing",
		Command: []string{"echo"},
		Cwd:     "/tmp",
	}
	if err := s.Create(meta); err != nil {
		t.Fatal(err)
	}

	// ClearExit on a task with no exit.json should not error.
	if err := s.ClearExit(meta.ID); err != nil {
		t.Fatalf("ClearExit (missing): %v", err)
	}
}

func TestWriteChildCreateTime_ReadChildCreateTime(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	meta := &Meta{
		ID:      GenerateID(),
		Name:    "cct-test",
		Command: []string{"echo"},
		Cwd:     "/tmp",
	}
	if err := s.Create(meta); err != nil {
		t.Fatal(err)
	}

	var ct int64 = 123456789
	if err := s.WriteChildCreateTime(meta.ID, ct); err != nil {
		t.Fatalf("WriteChildCreateTime: %v", err)
	}

	got := s.ReadChildCreateTime(meta.ID)
	if got != ct {
		t.Errorf("ReadChildCreateTime = %d, want %d", got, ct)
	}
}

func TestReadChildCreateTime_Missing(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Root: dir}

	got := s.ReadChildCreateTime("nonexistent")
	if got != 0 {
		t.Errorf("ReadChildCreateTime for missing file = %d, want 0", got)
	}
}
