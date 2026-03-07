// Package state manages the bgtask state directory and task metadata.
//
// State is stored in ~/.config/bgtask/procs/<id>/ where id is a
// timestamp-based identifier (YYYYMMDDTHHMMSS-XXXX). Task names are
// mutable metadata stored in meta.json, not in the directory path.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Meta describes a managed background task.
type Meta struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Command        []string          `json:"command"`
	Cwd            string            `json:"cwd"`
	EnvOverrides   map[string]string `json:"env_overrides,omitempty"`
	Labels         []string          `json:"labels,omitempty"`
	Restart        string            `json:"restart,omitempty"`
	RestartDelay   time.Duration     `json:"restart_delay,omitempty"`
	HealthCheck    string            `json:"health_check,omitempty"`
	HealthInterval time.Duration     `json:"health_interval,omitempty"`
	AutoRm         bool              `json:"auto_rm,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

// Exit records how a task's supervisor exited.
type Exit struct {
	Code     int       `json:"code"`
	Signal   string    `json:"signal,omitempty"`
	ExitedAt time.Time `json:"exited_at"`
}

// TaskStatus describes the current state of a task with typed sub-state.
type TaskStatus struct {
	State   string       `json:"state"` // "running", "exited", "dead", "unknown"
	Running *RunningInfo `json:"running,omitempty"`
	Exited  *ExitedInfo  `json:"exited,omitempty"`
	Dead    *DeadInfo    `json:"dead,omitempty"`
}

// RunningInfo holds details for a running task.
type RunningInfo struct {
	SupervisorPID int        `json:"supervisor_pid"`
	ChildPID      int        `json:"child_pid"`
	Ports         []uint32   `json:"ports,omitempty"`
	Since         *time.Time `json:"since,omitempty"`
}

// ExitedInfo holds details for an exited task.
type ExitedInfo struct {
	Code   int       `json:"code"`
	Signal string    `json:"signal,omitempty"`
	At     time.Time `json:"at"`
}

// DeadInfo holds details for a dead task (supervisor crashed).
type DeadInfo struct {
	Message string `json:"message"`
}

// Store provides access to the bgtask state directory.
type Store struct {
	Root string // e.g. ~/.config/bgtask/procs
}

const (
	lockRetries  = 50
	lockInterval = 100 * time.Millisecond
)

// lockStaleDuration is the maximum age of a lock file before it is
// considered stale. Lock is only held for very brief operations (name
// uniqueness checks, renames), so anything older is from a crashed process.
const lockStaleDuration = 30 * time.Second

// Lock acquires an advisory lock on the store directory.
// Returns a function to release the lock. Used for operations that require
// atomicity (name uniqueness checks, renames).
//
// Stale locks (older than 30 seconds) are automatically removed.
func (s *Store) Lock() (unlock func(), err error) {
	lockPath := filepath.Join(s.Root, ".lock")
	var f *os.File
	for i := 0; i < lockRetries; i++ {
		f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is constructed internally
		if err == nil {
			_ = f.Close()
			break
		}
		// Check for stale lock by age.
		if info, statErr := os.Stat(lockPath); statErr == nil {
			if time.Since(info.ModTime()) > lockStaleDuration {
				_ = os.Remove(lockPath)
				continue // Retry immediately after removing stale lock.
			}
		}
		time.Sleep(lockInterval)
	}
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	return func() { _ = os.Remove(lockPath) }, nil
}

// DefaultStore returns a Store using the platform-appropriate config directory.
func DefaultStore() (*Store, error) {
	root, err := configDir()
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	procsDir := filepath.Join(root, "procs")
	if err := os.MkdirAll(procsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create procs dir: %w", err)
	}
	return &Store{Root: procsDir}, nil
}

// GenerateID creates a new task ID: YYYYMMDDTHHMMSS-XXXXXXXX.
func GenerateID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	suffix := hex.EncodeToString(b)
	return time.Now().Format("20060102T150405") + "-" + suffix
}

// AutoName generates a name from a command's basename + 4 hex chars.
func AutoName(command []string) string {
	var base string
	if len(command) > 0 {
		base = filepath.Base(command[0])
	} else {
		base = "task"
	}
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return base + "-" + hex.EncodeToString(b)
}

// TaskDir returns the directory for a task by ID.
func (s *Store) TaskDir(id string) string {
	return filepath.Join(s.Root, id)
}

// Create initializes a new task directory and writes meta.json.
func (s *Store) Create(meta *Meta) error {
	dir := s.TaskDir(meta.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create task dir: %w", err)
	}
	return s.writeMeta(meta)
}

// ReadMeta reads meta.json from a task directory.
func (s *Store) ReadMeta(id string) (*Meta, error) {
	p := filepath.Join(s.TaskDir(id), "meta.json")
	data, err := os.ReadFile(p) //nolint:gosec // path is constructed internally
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse meta.json: %w", err)
	}
	return &m, nil
}

func (s *Store) writeMeta(meta *Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.TaskDir(meta.ID), "meta.json"), data)
}

// WritePID writes a PID to the named file (supervisor.pid or child.pid).
func (s *Store) WritePID(id, filename string, pid int) error {
	p := filepath.Join(s.TaskDir(id), filename)
	return os.WriteFile(p, []byte(strconv.Itoa(pid)), 0o600)
}

// ReadPID reads a PID from the named file.
func (s *Store) ReadPID(id, filename string) (int, error) {
	p := filepath.Join(s.TaskDir(id), filename)
	data, err := os.ReadFile(p) //nolint:gosec // path is constructed internally
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// WriteCreateTime writes the process creation time (milliseconds since epoch).
func (s *Store) WriteCreateTime(id string, createTime int64) error {
	p := filepath.Join(s.TaskDir(id), "createtime")
	return os.WriteFile(p, []byte(strconv.FormatInt(createTime, 10)), 0o600)
}

// ReadCreateTime reads the stored process creation time.
func (s *Store) ReadCreateTime(id string) int64 {
	p := filepath.Join(s.TaskDir(id), "createtime")
	data, err := os.ReadFile(p) //nolint:gosec // path is constructed internally
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// WriteChildStartTime writes the child process start time.
func (s *Store) WriteChildStartTime(id string, t time.Time) error {
	p := filepath.Join(s.TaskDir(id), "child.starttime")
	return os.WriteFile(p, []byte(t.Format(time.RFC3339Nano)), 0o600)
}

// ReadChildStartTime reads the child process start time.
func (s *Store) ReadChildStartTime(id string) time.Time {
	p := filepath.Join(s.TaskDir(id), "child.starttime")
	data, err := os.ReadFile(p) //nolint:gosec // path is constructed internally
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// WriteChildCreateTime writes the child process OS-level creation time
// (for PID reuse protection, analogous to WriteCreateTime for the supervisor).
func (s *Store) WriteChildCreateTime(id string, createTime int64) error {
	p := filepath.Join(s.TaskDir(id), "child.createtime")
	return os.WriteFile(p, []byte(strconv.FormatInt(createTime, 10)), 0o600)
}

// ReadChildCreateTime reads the child process OS-level creation time.
func (s *Store) ReadChildCreateTime(id string) int64 {
	p := filepath.Join(s.TaskDir(id), "child.createtime")
	data, err := os.ReadFile(p) //nolint:gosec // path is constructed internally
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// WriteExit writes exit.json.
func (s *Store) WriteExit(id string, exit *Exit) error {
	data, err := json.MarshalIndent(exit, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.TaskDir(id), "exit.json"), data)
}

// ReadExit reads exit.json. Returns nil if not present.
func (s *Store) ReadExit(id string) (*Exit, error) {
	p := filepath.Join(s.TaskDir(id), "exit.json")
	data, err := os.ReadFile(p) //nolint:gosec // path is constructed internally
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var e Exit
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("parse exit.json: %w", err)
	}
	return &e, nil
}

// OutputPath returns the path to the JSONL output log.
func (s *Store) OutputPath(id string) string {
	return filepath.Join(s.TaskDir(id), "output.jsonl")
}

// ListIDs returns all task IDs (directory names under procs/).
func (s *Store) ListIDs() ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// Resolve finds a task ID by name or ID. Returns the ID and meta.
func (s *Store) Resolve(nameOrID string) (string, *Meta, error) {
	// Try as direct ID first, but only if it looks like a safe directory name
	// (no path separators or traversal). Defense-in-depth against path traversal.
	if nameOrID == filepath.Base(nameOrID) && nameOrID != "." && nameOrID != ".." {
		if meta, err := s.ReadMeta(nameOrID); err == nil {
			return nameOrID, meta, nil
		}
	}

	// Search by name.
	ids, err := s.ListIDs()
	if err != nil {
		return "", nil, err
	}
	var matches []string
	var matchMeta *Meta
	for _, id := range ids {
		meta, err := s.ReadMeta(id)
		if err != nil {
			continue
		}
		if meta.Name == nameOrID {
			matches = append(matches, id)
			matchMeta = meta
		}
	}
	switch len(matches) {
	case 0:
		return "", nil, fmt.Errorf("task not found: %s", nameOrID)
	case 1:
		return matches[0], matchMeta, nil
	default:
		return "", nil, fmt.Errorf("ambiguous name %q matches %d tasks; use the task ID instead", nameOrID, len(matches))
	}
}

// Rename updates the name field in a task's meta.json.
func (s *Store) Rename(id, newName string) error {
	meta, err := s.ReadMeta(id)
	if err != nil {
		return err
	}
	meta.Name = newName
	return s.writeMeta(meta)
}

// SetLabels replaces the labels on a task.
func (s *Store) SetLabels(id string, labels []string) error {
	meta, err := s.ReadMeta(id)
	if err != nil {
		return err
	}
	meta.Labels = labels
	return s.writeMeta(meta)
}

// IsNameTaken checks if any active task already uses the given name.
func (s *Store) IsNameTaken(name string) (bool, error) {
	ids, err := s.ListIDs()
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		meta, err := s.ReadMeta(id)
		if err != nil {
			continue
		}
		if meta.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// atomicWrite writes data to a file atomically: write to .tmp, then rename.
// On Windows, os.Rename fails if the destination exists, so remove it first.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	return os.Rename(tmp, path)
}

// ClearExit removes exit.json so a stopped task can be re-started.
func (s *Store) ClearExit(id string) error {
	p := filepath.Join(s.TaskDir(id), "exit.json")
	err := os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Remove deletes a task's state directory entirely.
// On Windows, recently-terminated processes may still hold file handles
// briefly, so we retry a few times before giving up.
func (s *Store) Remove(id string) error {
	dir := s.TaskDir(id)
	var err error
	for i := 0; i < 5; i++ {
		err = os.RemoveAll(dir)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}
