package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-runewidth"
	"github.com/philsphicas/bgtask/internal/process"
	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/supervisor"
	"github.com/philsphicas/bgtask/internal/ui"
)

// RunCmd launches a command in the background.
type RunCmd struct {
	Name           string        `short:"n" help:"Name for the task (auto-generated if omitted)."`
	Dir            string        `short:"d" help:"Working directory for the command." type:"existingdir"`
	Env            []string      `short:"e" help:"Environment variable override (KEY=VAL, repeatable)." placeholder:"KEY=VAL"`
	Tag            []string      `help:"Tag for the task (repeatable, for bulk operations)." placeholder:"TAG"`
	Health         string        `help:"Health check command (run periodically, logged to output)." placeholder:"CMD"`
	HealthInterval time.Duration `help:"Health check interval." default:"30s"`
	Restart        string        `help:"Restart policy (always, on-failure)." placeholder:"POLICY"`
	RestartDelay   time.Duration `help:"Fixed delay between restarts (default: exponential backoff 1s-60s)." default:"0s"`
	Rm             bool          `help:"Automatically remove task state after exit."`
	Args           []string      `arg:"" optional:"" passthrough:"" help:"Command and arguments to run (after --)."`
}

func (r *RunCmd) Run(store *state.Store) error {
	// Strip leading "--" that kong passthrough includes.
	args := r.Args
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	if len(args) == 0 {
		return fmt.Errorf("provide a command after --")
	}

	// Validate restart policy.
	switch r.Restart {
	case "", "always", "on-failure":
		// ok
	default:
		return fmt.Errorf("invalid --restart value %q (expected: always, on-failure)", r.Restart)
	}

	// Parse env overrides (before lock -- no store access needed).
	envOverrides := make(map[string]string)
	for _, e := range r.Env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid --env format %q (expected KEY=VAL)", e)
		}
		envOverrides[parts[0]] = parts[1]
	}

	cwd := r.Dir
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}

	name := r.Name
	if name == "" {
		name = state.AutoName(args)
	}

	// Lock to ensure name uniqueness atomically with Create.
	unlock, err := store.Lock()
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}

	if taken, err := store.IsNameTaken(name); err != nil {
		unlock()
		return err
	} else if taken {
		unlock()
		return fmt.Errorf("name %q is already in use; pick a different name or use bgtask rename", name)
	}

	meta := &state.Meta{
		ID:             state.GenerateID(),
		Name:           name,
		Command:        args,
		Cwd:            cwd,
		EnvOverrides:   envOverrides,
		Tags:           r.Tag,
		Restart:        r.Restart,
		RestartDelay:   r.RestartDelay,
		HealthCheck:    r.Health,
		HealthInterval: r.HealthInterval,
		AutoRm:         r.Rm,
		CreatedAt:      time.Now(),
	}

	if err := store.Create(meta); err != nil {
		unlock()
		return err
	}

	// Release lock -- name is reserved. The rest (detach, startup check)
	// does not need atomicity.
	unlock()

	// Re-exec self as detached supervisor.
	supervisorArgs := []string{"supervisor", store.Root, meta.ID}
	proc, err := process.Detach(supervisorArgs)
	if err != nil {
		_ = store.Remove(meta.ID)
		return fmt.Errorf("detach supervisor: %w", err)
	}

	lipgloss.Printf("Started: %s (id: %s, pid: %d)\n", ui.Bold.Render(name), meta.ID, proc.Pid)

	// Brief startup check: catch immediate failures (typos, missing commands).
	// The supervisor writes exit.json within ~50ms for bad commands.
	time.Sleep(100 * time.Millisecond)
	exit, _ := store.ReadExit(meta.ID)
	if exit != nil && exit.Code != 0 {
		fmt.Fprintf(os.Stderr, "warning: task exited immediately (code %d). Check: bgtask logs %s\n", exit.Code, name)
	}

	return nil
}

// LsCmd lists managed background tasks.
type LsCmd struct {
	JSON    bool   `help:"Output as JSON." json:"-"`
	Tag     string `help:"Filter by tag."`
	NoTrunc bool   `help:"Do not truncate command output."`
}

func (l *LsCmd) Run(store *state.Store) error {
	ids, err := store.ListIDs()
	if err != nil {
		return err
	}

	if len(ids) == 0 {
		if !l.JSON {
			fmt.Println("No tasks.")
		} else {
			fmt.Println("[]")
		}
		return nil
	}

	type taskInfo struct {
		Name    string   `json:"name"`
		ID      string   `json:"id"`
		PID     int      `json:"pid"`
		Status  string   `json:"status"`
		Ports   []uint32 `json:"ports,omitempty"`
		Age     string   `json:"age"`
		Command string   `json:"command"`
	}

	tasks := make([]taskInfo, 0)
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil {
			continue
		}

		// Filter by tag if specified.
		if l.Tag != "" && !hasTag(meta.Tags, l.Tag) {
			continue
		}

		pid, _ := store.ReadPID(id, "supervisor.pid")
		status := "unknown"
		age := ""
		if pid > 0 {
			if process.IsAlive(pid) {
				status = "running"
				age = formatDuration(time.Since(meta.CreatedAt))
			} else {
				status = "dead"
			}
		}

		exit, _ := store.ReadExit(id)
		if exit != nil {
			status = fmt.Sprintf("exited(%d)", exit.Code)
			age = formatDuration(time.Since(exit.ExitedAt)) + " ago"
		}

		// Detect listening ports for running tasks.
		var ports []uint32
		if status == "running" {
			childPID, _ := store.ReadPID(id, "child.pid")
			if childPID > 0 {
				ports = process.ListeningPorts(childPID)
			}
		}

		tasks = append(tasks, taskInfo{
			Name:    meta.Name,
			ID:      id,
			PID:     pid,
			Status:  status,
			Ports:   ports,
			Age:     age,
			Command: formatCommand(meta),
		})
	}

	if l.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}

	headers := []string{"NAME", "ID", "PID", "STATUS", "PORTS", "AGE", "COMMAND"}
	const numCols = 7
	const cellPad = 2 // Padding(0, 1) adds 1 char each side.

	var rows [][]string
	for _, t := range tasks {
		pidStr := "-"
		if t.PID > 0 {
			pidStr = fmt.Sprintf("%d", t.PID)
		}
		rows = append(rows, []string{
			t.Name, t.ID, pidStr, t.Status,
			formatPorts(t.Ports), t.Age, t.Command,
		})
	}

	// Truncate COMMAND column to fit the terminal unless --no-trunc is set
	// or stdout is not a TTY (piped output gets full commands).
	cmdColWidth := 0
	if tw := terminalWidth(); tw > 0 && !l.NoTrunc {
		// Measure max content width of each fixed column (0–5).
		colWidths := make([]int, numCols)
		for i, h := range headers {
			colWidths[i] = runewidth.StringWidth(h)
		}
		for _, row := range rows {
			for i := 0; i < numCols-1; i++ {
				if w := runewidth.StringWidth(row[i]); w > colWidths[i] {
					colWidths[i] = w
				}
			}
		}
		fixedWidth := 0
		for i := 0; i < numCols-1; i++ {
			fixedWidth += colWidths[i] + cellPad
		}
		cmdColWidth = tw - fixedWidth - cellPad
		if cmdColWidth < 20 {
			cmdColWidth = 20
		}
		for i := range rows {
			rows[i][6] = truncateCommand(rows[i][6], cmdColWidth)
		}
	}

	tbl := table.New().
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(true).BorderColumn(false).BorderRow(false).
		BorderStyle(ui.Dim).
		Headers(headers...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Bold(true)
			}
			// Color the STATUS column.
			if col == 3 && row >= 0 && row < len(rows) {
				return s.Inherit(ui.StatusStyle(rows[row][3]))
			}
			return s
		}).
		Rows(rows...)

	lipgloss.Println(tbl.Render())
	return nil
}

func formatCommand(meta *state.Meta) string {
	return strings.Join(meta.Command, " ")
}

// terminalWidth returns the terminal width, or 0 if stdout is not a TTY.
// Respects the COLUMNS environment variable as an override, which is useful
// in non-TTY contexts (e.g., testing).
func terminalWidth() int {
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if w, err := strconv.Atoi(cols); err == nil && w > 0 {
			return w
		}
	}
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return 0
	}
	return w
}

// truncateCommand truncates s to maxWidth display columns, appending "…" if truncated.
func truncateCommand(s string, maxWidth int) string {
	if maxWidth <= 0 || runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return "…"
	}
	return runewidth.Truncate(s, maxWidth, "…")
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, hours)
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func formatPorts(ports []uint32) string {
	if len(ports) == 0 {
		return "-"
	}
	var parts []string
	for _, p := range ports {
		parts = append(parts, fmt.Sprintf(":%d", p))
	}
	return strings.Join(parts, ",")
}

// StatusCmd shows detailed status of a task.
type StatusCmd struct {
	Name string `arg:"" help:"Task name or ID."`
	JSON bool   `help:"Output as JSON." json:"-"`
}

func (s *StatusCmd) Run(store *state.Store) error {
	id, meta, err := store.Resolve(s.Name)
	if err != nil {
		return err
	}

	supPID, _ := store.ReadPID(id, "supervisor.pid")
	childPID, _ := store.ReadPID(id, "child.pid")
	exit, _ := store.ReadExit(id)

	if s.JSON {
		info := map[string]interface{}{
			"name":           meta.Name,
			"id":             id,
			"command":        meta.Command,
			"cwd":            meta.Cwd,
			"created_at":     meta.CreatedAt,
			"restart":        meta.Restart,
			"supervisor_pid": supPID,
			"child_pid":      childPID,
			"log":            store.OutputPath(id),
		}
		if supPID > 0 {
			info["supervisor_alive"] = process.IsAlive(supPID)
		}
		if childPID > 0 {
			info["child_alive"] = process.IsAlive(childPID)
			if ports := process.ListeningPorts(childPID); len(ports) > 0 {
				info["ports"] = ports
			}
		}
		if exit != nil {
			info["exit_code"] = exit.Code
			info["exited_at"] = exit.ExitedAt
			if exit.Signal != "" {
				info["signal"] = exit.Signal
			}
		}
		if len(meta.EnvOverrides) > 0 {
			info["env_overrides"] = meta.EnvOverrides
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}

	kv := func(label, value string) {
		lipgloss.Printf("%s %s\n", ui.Label.Render(label), value)
	}

	kv("Name:      ", ui.Bold.Render(meta.Name))
	kv("ID:        ", id)
	kv("Command:   ", formatCommand(meta))
	kv("Cwd:       ", meta.Cwd)
	kv("Created:   ", meta.CreatedAt.Format(time.RFC3339))
	restartStr := "no"
	if meta.Restart != "" {
		restartStr = meta.Restart
	}
	kv("Restart:   ", restartStr)

	if supPID > 0 {
		alive := process.IsAlive(supPID)
		kv("Supervisor:", fmt.Sprintf("PID %d (%s)", supPID, styledAlive(alive)))
	}
	if childPID > 0 {
		alive := process.IsAlive(childPID)
		kv("Child:     ", fmt.Sprintf("PID %d (%s)", childPID, styledAlive(alive)))
		if ports := process.ListeningPorts(childPID); len(ports) > 0 {
			kv("Ports:     ", formatPorts(ports))
		}
	}
	if exit != nil {
		kv("Exit code: ", fmt.Sprintf("%d", exit.Code))
		kv("Exited at: ", exit.ExitedAt.Format(time.RFC3339))
		if exit.Signal != "" {
			kv("Signal:    ", exit.Signal)
		}
	}
	kv("Log:       ", store.OutputPath(id))

	if len(meta.EnvOverrides) > 0 {
		lipgloss.Println(ui.Label.Render("Env overrides:"))
		for k, v := range meta.EnvOverrides {
			fmt.Printf("  %s=%s\n", k, v)
		}
	}

	return nil
}

func styledAlive(alive bool) string {
	if alive {
		return ui.Green.Render("running")
	}
	return ui.Red.Render("dead")
}

// LogsCmd views task output logs.
type LogsCmd struct {
	Name       string        `arg:"" help:"Task name or ID."`
	Follow     bool          `short:"f" help:"Follow log output."`
	Tail       int           `short:"t" help:"Number of lines to show from the end." default:"0"`
	Since      time.Duration `help:"Show entries from the last duration (e.g. 5m, 1h)." default:"0s"`
	Stdout     bool          `help:"Show only stdout."`
	Stderr     bool          `help:"Show only stderr."`
	Timestamps bool          `help:"Prefix each line with its timestamp."`
}

func (l *LogsCmd) Run(store *state.Store) error {
	if l.Stdout && l.Stderr {
		return fmt.Errorf("--stdout and --stderr are mutually exclusive")
	}

	id, _, err := store.Resolve(l.Name)
	if err != nil {
		return err
	}

	exitPath := filepath.Join(store.TaskDir(id), "exit.json")
	return showLogs(store.ListLogFiles(id), exitPath, l.Follow, l.Tail, l.Since, l.Stdout, l.Stderr, l.Timestamps)
}

// StopCmd stops a running task.
type StopCmd struct {
	Name  string `arg:"" optional:"" help:"Task name or ID."`
	Tag   string `help:"Stop all tasks with this tag."`
	Force bool   `help:"Force stop (SIGKILL)."`
}

func (s *StopCmd) Run(store *state.Store) error {
	if s.Name == "" && s.Tag == "" {
		return fmt.Errorf("provide a task name or --tag")
	}

	if s.Tag != "" {
		return s.stopByTag(store)
	}

	return s.stopOne(store, s.Name)
}

func (s *StopCmd) stopByTag(store *state.Store) error {
	ids, err := store.ListIDs()
	if err != nil {
		return err
	}
	stopped := 0
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil || !hasTag(meta.Tags, s.Tag) {
			continue
		}
		pid, alive := verifyAndGetPID(store, id)
		if alive {
			if err := s.signalAndWait(store, id, pid); err == nil {
				lipgloss.Printf("Stopped: %s\n", ui.Bold.Render(meta.Name))
				stopped++
			}
		}
	}
	if stopped == 0 {
		fmt.Printf("No running tasks with tag %q.\n", s.Tag)
	}
	return nil
}

func (s *StopCmd) stopOne(store *state.Store, nameOrID string) error {
	id, meta, err := store.Resolve(nameOrID)
	if err != nil {
		return err
	}

	pid, alive := verifyAndGetPID(store, id)
	if !alive {
		fmt.Printf("Task %s is not running.\n", meta.Name)
		return nil
	}

	if err := s.signalAndWait(store, id, pid); err != nil {
		return err
	}

	lipgloss.Printf("Stopped: %s\n", ui.Bold.Render(meta.Name))
	return nil
}

func (s *StopCmd) signalAndWait(store *state.Store, id string, pid int) error {
	if s.Force {
		// Force: immediately terminate both supervisor and child.
		childPID, _ := store.ReadPID(id, "child.pid")
		_ = process.SignalKill(pid)
		if childPID > 0 && process.IsAlive(childPID) {
			_ = process.SignalKill(childPID)
		}
		return nil
	}
	gracefulStopTask(store, id, pid)
	return nil
}

// verifyAndGetPID reads the supervisor PID and verifies it hasn't been reused.
// Returns the PID and true if the process is alive and verified.
func verifyAndGetPID(store *state.Store, id string) (int, bool) {
	pid, err := store.ReadPID(id, "supervisor.pid")
	if err != nil || pid <= 0 {
		return 0, false
	}
	if !process.IsAlive(pid) {
		return pid, false
	}
	savedCreate := store.ReadCreateTime(id)
	if !process.VerifyPID(pid, savedCreate) {
		return pid, false
	}
	return pid, true
}

// gracefulStopTask sends SIGTERM to the supervisor, waits up to 10s, then
// escalates. Also terminates the child process to prevent orphans (critical
// on Windows where TerminateProcess doesn't allow cleanup).
func gracefulStopTask(store *state.Store, id string, supervisorPID int) {
	_ = process.SignalTerm(supervisorPID)
	for i := 0; i < 100; i++ {
		if !process.IsAlive(supervisorPID) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if process.IsAlive(supervisorPID) {
		_ = process.SignalKill(supervisorPID)
	}
	// Ensure child is also terminated (supervisor may not have had a chance
	// to clean up, especially on Windows where TerminateProcess is instant).
	childPID, _ := store.ReadPID(id, "child.pid")
	if childPID > 0 && process.IsAlive(childPID) {
		_ = process.SignalKill(childPID)
	}
}

// PauseCmd pauses a running task (supervisor stays alive).
type PauseCmd struct {
	Name string `arg:"" help:"Task name or ID."`
}

func (p *PauseCmd) Run(store *state.Store) error {
	id, meta, err := store.Resolve(p.Name)
	if err != nil {
		return err
	}

	pid, alive := verifyAndGetPID(store, id)
	if !alive {
		return fmt.Errorf("task %s is not running", meta.Name)
	}

	if err := process.SignalPause(pid); err != nil {
		return fmt.Errorf("send pause signal: %w", err)
	}

	lipgloss.Printf("Paused: %s\n", ui.Bold.Render(meta.Name))
	return nil
}

// ResumeCmd resumes a paused task.
type ResumeCmd struct {
	Name string `arg:"" help:"Task name or ID."`
}

func (r *ResumeCmd) Run(store *state.Store) error {
	id, meta, err := store.Resolve(r.Name)
	if err != nil {
		return err
	}

	pid, alive := verifyAndGetPID(store, id)
	if !alive {
		return fmt.Errorf("task %s supervisor is not running (cannot resume)", meta.Name)
	}

	if err := process.SignalResume(pid); err != nil {
		return fmt.Errorf("send resume signal: %w", err)
	}

	lipgloss.Printf("Resumed: %s\n", ui.Bold.Render(meta.Name))
	return nil
}

// RenameCmd renames a task.
type RenameCmd struct {
	OldName string `arg:"" help:"Current task name or ID."`
	NewName string `arg:"" help:"New name for the task."`
}

func (r *RenameCmd) Run(store *state.Store) error {
	unlock, err := store.Lock()
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer unlock()

	id, _, err := store.Resolve(r.OldName)
	if err != nil {
		return err
	}

	if taken, err := store.IsNameTaken(r.NewName); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("name %q is already in use", r.NewName)
	}

	if err := store.Rename(id, r.NewName); err != nil {
		return err
	}

	lipgloss.Printf("Renamed: %s → %s\n", r.OldName, ui.Bold.Render(r.NewName))
	return nil
}

// RmCmd removes a task (stop + delete state).
type RmCmd struct {
	Name string `arg:"" help:"Task name or ID."`
}

func (r *RmCmd) Run(store *state.Store) error {
	id, meta, err := store.Resolve(r.Name)
	if err != nil {
		return err
	}

	// Stop if running, using the same graceful wait as stop.
	pid, alive := verifyAndGetPID(store, id)
	if alive {
		gracefulStopTask(store, id, pid)
	}

	if err := store.Remove(id); err != nil {
		return fmt.Errorf("remove state: %w", err)
	}

	lipgloss.Printf("Removed: %s\n", ui.Bold.Render(meta.Name))
	return nil
}

// CleanupCmd removes state for all non-running tasks.
type CleanupCmd struct{}

func (c *CleanupCmd) Run(store *state.Store) error {
	ids, err := store.ListIDs()
	if err != nil {
		return err
	}

	removed := 0
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil {
			continue
		}

		_, alive := verifyAndGetPID(store, id)
		if alive {
			continue // Still running, skip.
		}

		if err := store.Remove(id); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", meta.Name, err)
			continue
		}
		fmt.Printf("Removed: %s (%s)\n", meta.Name, id)
		removed++
	}

	if removed == 0 {
		fmt.Println("Nothing to clean up.")
	} else {
		fmt.Printf("Cleaned up %d task(s).\n", removed)
	}
	return nil
}

// SupervisorCmd is the hidden re-exec supervisor shim.
type SupervisorCmd struct {
	Root string `arg:"" help:"Store root directory."`
	ID   string `arg:"" help:"Task ID."`
}

func (s *SupervisorCmd) Run(_ *state.Store) error {
	store := &state.Store{Root: s.Root}
	meta, err := store.ReadMeta(s.ID)
	if err != nil {
		return fmt.Errorf("read meta: %w", err)
	}

	cfg := &supervisor.Config{
		StateDir:       store.TaskDir(s.ID),
		Meta:           meta,
		Store:          store,
		Restart:        meta.Restart,
		RestartDelay:   meta.RestartDelay,
		HealthCheck:    meta.HealthCheck,
		HealthInterval: meta.HealthInterval,
	}

	return supervisor.Run(cfg)
}
