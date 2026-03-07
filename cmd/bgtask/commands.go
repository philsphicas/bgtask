package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	"github.com/philsphicas/bgtask/internal/validation"
)

// RunCmd launches a command in the background.
type RunCmd struct {
	Name           string        `short:"n" help:"Name for the task (auto-generated if omitted)."`
	Dir            string        `short:"d" help:"Working directory for the command." type:"existingdir"`
	Env            []string      `short:"e" help:"Environment variable override (KEY=VAL, repeatable)." placeholder:"KEY=VAL"`
	Labels         []string      `short:"l" name:"labels" help:"Label for the task (repeatable, for bulk operations)." placeholder:"LABEL"`
	Health         string        `help:"Health check command (run periodically, logged to output)." placeholder:"CMD"`
	HealthInterval time.Duration `help:"Health check interval." default:"30s"`
	Restart        string        `help:"Restart policy (always, on-failure)." placeholder:"POLICY"`
	RestartDelay   time.Duration `help:"Fixed delay between restarts (default: exponential backoff 1s-60s)." default:"0s"`
	Rm             bool          `help:"Automatically remove task state after exit."`
	Args           []string      `arg:"" optional:"" passthrough:"" help:"Command and arguments to run (after --)."`
}

func (r *RunCmd) Run(store *state.Store) error {
	args := r.Args

	name := r.Name

	// Require "--" separator to prevent typos in flags from being silently
	// swallowed as part of the command (e.g., --labels instead of --label).
	if len(args) == 0 || args[0] != "--" {
		return fmt.Errorf("provide a command after --, e.g.: bgtask run --name myserver -- ./server")
	}
	args = args[1:]

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
		// Stop and remove the existing task.
		existingID, _, resolveErr := store.Resolve(name)
		if resolveErr != nil {
			unlock()
			return resolveErr
		}
		pid, alive := verifyAndGetPID(store, existingID)
		// Release the lock before potentially slow graceful stop.
		unlock()
		if alive {
			gracefulStopTask(store, existingID, pid, 10*time.Second)
		}
		_ = store.Remove(existingID)
		// Re-acquire lock for Create.
		unlock, err = store.Lock()
		if err != nil {
			return fmt.Errorf("re-acquire lock: %w", err)
		}
	}

	if err := validation.ValidateLabels(r.Labels); err != nil {
		unlock()
		return err
	}

	meta := &state.Meta{
		ID:             state.GenerateID(),
		Name:           name,
		Command:        args,
		Cwd:            cwd,
		EnvOverrides:   envOverrides,
		Labels:         r.Labels,
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
	JSON    bool     `short:"j" help:"Output as JSON." json:"-"`
	Labels  []string `short:"l" name:"labels" help:"Filter by label (repeatable)."`
	Wide    bool     `short:"w" help:"Show all columns (ID, PID, labels)."`
	NoTrunc bool     `help:"Do not truncate command output."`
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
		Name      string           `json:"name"`
		ID        string           `json:"id"`
		Status    state.TaskStatus `json:"status"`
		Labels    []string         `json:"labels,omitempty"`
		CreatedAt time.Time        `json:"created_at"`
		Command   []string         `json:"command"`
	}

	tasks := make([]taskInfo, 0)
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil {
			continue
		}

		// Filter by label if specified (OR semantics: match any).
		if len(l.Labels) > 0 && !hasAnyLabel(meta.Labels, l.Labels) {
			continue
		}

		ts := resolveTaskStatus(store, id)

		tasks = append(tasks, taskInfo{
			Name:      meta.Name,
			ID:        id,
			Status:    ts,
			Labels:    meta.Labels,
			CreatedAt: meta.CreatedAt,
			Command:   meta.Command,
		})
	}

	if l.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}

	if len(tasks) == 0 {
		if len(l.Labels) > 0 {
			fmt.Println("No tasks with the specified label(s).")
		} else {
			fmt.Println("No tasks.")
		}
		return nil
	}

	if l.Wide {
		var rows [][]string
		for _, t := range tasks {
			pidStr := "-"
			if t.Status.Running != nil && t.Status.Running.SupervisorPID > 0 {
				pidStr = fmt.Sprintf("%d", t.Status.Running.SupervisorPID)
			}
			labelsStr := "-"
			if len(t.Labels) > 0 {
				labelsStr = strings.Join(t.Labels, ",")
			}
			var portsStr string
			if t.Status.Running != nil {
				portsStr = formatPorts(t.Status.Running.Ports)
			} else {
				portsStr = "-"
			}
			rows = append(rows, []string{
				t.Name, t.ID, pidStr, statusDisplayString(t.Status),
				portsStr, labelsStr, formatDuration(time.Since(t.CreatedAt)), strings.Join(t.Command, " "),
			})
		}

		statusCol := 3
		tbl := table.New().
			BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
			BorderHeader(true).BorderColumn(false).BorderRow(false).
			BorderStyle(ui.Dim).
			Headers("NAME", "ID", "PID", "STATUS", "PORTS", "LABELS", "AGE", "COMMAND").
			StyleFunc(func(row, col int) lipgloss.Style {
				s := lipgloss.NewStyle().Padding(0, 1)
				if row == table.HeaderRow {
					return s.Bold(true)
				}
				if col == statusCol && row >= 0 && row < len(rows) {
					return s.Inherit(ui.StatusStyle(rows[row][statusCol]))
				}
				return s
			}).
			Rows(rows...)

		lipgloss.Println(tbl.Render())
		return nil
	}

	// Default columns: NAME, STATUS, PORTS, AGE, COMMAND.
	headers := []string{"NAME", "STATUS", "PORTS", "AGE", "COMMAND"}
	const numCols = 5
	const cellPad = 2 // Padding(0, 1) adds 1 char each side.

	var rows [][]string
	for _, t := range tasks {
		var portsStr string
		if t.Status.Running != nil {
			portsStr = formatPorts(t.Status.Running.Ports)
		} else {
			portsStr = "-"
		}
		rows = append(rows, []string{
			t.Name, statusDisplayString(t.Status), portsStr, formatDuration(time.Since(t.CreatedAt)), strings.Join(t.Command, " "),
		})
	}

	// Truncate COMMAND column to fit the terminal unless --no-trunc is set
	// or stdout is not a TTY (piped output gets full commands).
	if tw := terminalWidth(); tw > 0 && !l.NoTrunc {
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
		cmdColWidth := tw - fixedWidth - cellPad
		if cmdColWidth < 20 {
			cmdColWidth = 20
		}
		for i := range rows {
			rows[i][numCols-1] = truncateCommand(rows[i][numCols-1], cmdColWidth)
		}
	}

	statusCol := 1
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
			if col == statusCol && row >= 0 && row < len(rows) {
				return s.Inherit(ui.StatusStyle(rows[row][statusCol]))
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

// terminalWidth returns the terminal width. Returns 0 if stdout is not a TTY
// and COLUMNS is not set. The COLUMNS environment variable overrides TTY
// detection, which is useful in non-TTY contexts (e.g., testing).
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

func hasLabel(labels []string, label string) bool {
	for _, l := range labels {
		if l == label {
			return true
		}
	}
	return false
}

func hasAnyLabel(labels []string, filterLabels []string) bool {
	for _, fl := range filterLabels {
		if hasLabel(labels, fl) {
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
	JSON bool   `short:"j" help:"Output as JSON." json:"-"`
}

func (s *StatusCmd) Run(store *state.Store) error {
	id, meta, err := store.Resolve(s.Name)
	if err != nil {
		return err
	}

	ts := resolveTaskStatus(store, id)

	if s.JSON {
		info := map[string]interface{}{
			"name":       meta.Name,
			"id":         id,
			"command":    meta.Command,
			"cwd":        meta.Cwd,
			"created_at": meta.CreatedAt,
			"restart":    meta.Restart,
			"status":     ts,
			"log":        store.OutputPath(id),
		}
		if len(meta.EnvOverrides) > 0 {
			info["env_overrides"] = meta.EnvOverrides
		}
		if len(meta.Labels) > 0 {
			info["labels"] = meta.Labels
		}
		if meta.RestartDelay > 0 {
			info["restart_delay"] = meta.RestartDelay.String()
		}
		if meta.HealthCheck != "" {
			info["health_check"] = meta.HealthCheck
			info["health_interval"] = meta.HealthInterval.String()
		}
		if meta.AutoRm {
			info["auto_rm"] = true
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
	if meta.RestartDelay > 0 {
		kv("Rst delay: ", meta.RestartDelay.String())
	}
	if len(meta.Labels) > 0 {
		kv("Labels:    ", strings.Join(meta.Labels, ", "))
	}
	if meta.HealthCheck != "" {
		kv("Health:    ", fmt.Sprintf("%s (every %s)", meta.HealthCheck, meta.HealthInterval))
	}
	if meta.AutoRm {
		kv("Auto-rm:   ", "yes")
	}
	stateStr := statusState(ts)
	kv("Status:    ", ui.StatusStyle(stateStr).Render(stateStr))

	if ts.Running != nil {
		kv("Supervisor:", fmt.Sprintf("PID %d (%s)", ts.Running.SupervisorPID, ui.Green.Render("running")))
		if ts.Running.ChildPID > 0 {
			kv("Child:     ", fmt.Sprintf("PID %d (%s)", ts.Running.ChildPID, styledAlive(process.IsAlive(ts.Running.ChildPID))))
		}
		if len(ts.Running.Ports) > 0 {
			kv("Ports:     ", formatPorts(ts.Running.Ports))
		}
		if ts.Running.Since != nil {
			kv("Since:     ", ts.Running.Since.Format(time.RFC3339))
		}
	}
	if ts.Exited != nil {
		kv("Exit code: ", fmt.Sprintf("%d", ts.Exited.Code))
		kv("Exited at: ", ts.Exited.At.Format(time.RFC3339))
		if ts.Exited.Signal != "" {
			kv("Signal:    ", ts.Exited.Signal)
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

// resolveTaskStatus builds a TaskStatus for a given task ID.
func resolveTaskStatus(store *state.Store, id string) state.TaskStatus {
	exit, _ := store.ReadExit(id)

	if exit != nil {
		return state.TaskStatus{
			State: "exited",
			Exited: &state.ExitedInfo{
				Code:   exit.Code,
				Signal: exit.Signal,
				At:     exit.ExitedAt,
			},
		}
	}

	pid, alive := verifyAndGetPID(store, id)
	if pid > 0 {
		if alive {
			childPID, _ := store.ReadPID(id, "child.pid")
			var ports []uint32
			if childPID > 0 {
				ports = process.ListeningPorts(childPID)
			}
			since := store.ReadChildStartTime(id)
			var sincePtr *time.Time
			if !since.IsZero() {
				sincePtr = &since
			}
			return state.TaskStatus{
				State: "running",
				Running: &state.RunningInfo{
					SupervisorPID: pid,
					ChildPID:      childPID,
					Ports:         ports,
					Since:         sincePtr,
				},
			}
		}
		return state.TaskStatus{
			State: "dead",
			Dead: &state.DeadInfo{
				Message: "supervisor process no longer exists",
			},
		}
	}

	return state.TaskStatus{State: "unknown"}
}

// statusDisplayString returns a compact status string for table display,
// including the duration (e.g., "running (5m)", "exited(1) (2m ago)").
func statusDisplayString(ts state.TaskStatus) string {
	switch ts.State {
	case "running":
		if ts.Running != nil && ts.Running.Since != nil {
			return fmt.Sprintf("running (%s)", formatDuration(time.Since(*ts.Running.Since)))
		}
		return "running"
	case "exited":
		if ts.Exited != nil {
			dur := formatDuration(time.Since(ts.Exited.At))
			return fmt.Sprintf("exited(%d) (%s ago)", ts.Exited.Code, dur)
		}
		return "exited"
	default:
		return ts.State
	}
}

// statusState returns just the base state string for styling purposes.
func statusState(ts state.TaskStatus) string {
	if ts.State == "exited" && ts.Exited != nil {
		return fmt.Sprintf("exited(%d)", ts.Exited.Code)
	}
	return ts.State
}

// LogsCmd views task output logs.
type LogsCmd struct {
	Name       string        `arg:"" help:"Task name or ID."`
	Follow     bool          `short:"f" help:"Follow log output."`
	Tail       int           `help:"Number of lines to show from the end." default:"-1"`
	Since      time.Duration `help:"Show entries from the last duration (e.g. 5m, 1h)." default:"0s"`
	All        bool          `short:"a" help:"Show logs from all runs (default: current run only)."`
	Stdout     bool          `help:"Show only stdout."`
	Stderr     bool          `help:"Show only stderr."`
	Timestamps bool          `short:"T" help:"Prefix each line with its timestamp."`
}

func (l *LogsCmd) Run(store *state.Store) error {
	if l.Stdout && l.Stderr {
		return fmt.Errorf("--stdout and --stderr are mutually exclusive")
	}

	id, _, err := store.Resolve(l.Name)
	if err != nil {
		return err
	}

	// Default to current run only; --all shows everything.
	var sinceTime time.Time
	if !l.All {
		sinceTime = store.ReadChildStartTime(id)
	}

	exitPath := filepath.Join(store.TaskDir(id), "exit.json")
	return showLogs(store.ListLogFiles(id), exitPath, l.Follow, l.Tail, l.Since, sinceTime, l.Stdout, l.Stderr, l.Timestamps)
}

// StopCmd stops a running task.
type StopCmd struct {
	Name    []string      `arg:"" optional:"" help:"Task name(s) or ID(s)."`
	Labels  []string      `short:"l" name:"labels" help:"Stop all tasks with this label (repeatable)."`
	Force   bool          `help:"Force stop (SIGKILL)."`
	Timeout time.Duration `help:"Graceful shutdown timeout." default:"10s"`
	All     bool          `short:"a" help:"Stop all running tasks."`
}

func (s *StopCmd) Run(store *state.Store) error {
	if len(s.Name) == 0 && len(s.Labels) == 0 && !s.All {
		return fmt.Errorf("provide a task name, --labels, or --all")
	}

	if s.All {
		return s.stopAll(store)
	}

	if len(s.Labels) > 0 {
		return s.stopByLabel(store)
	}

	for _, name := range s.Name {
		if err := s.stopOne(store, name); err != nil {
			return err
		}
	}
	return nil
}

func (s *StopCmd) stopAll(store *state.Store) error {
	ids, err := store.ListIDs()
	if err != nil {
		return err
	}
	stopped := 0
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil {
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
		fmt.Println("No running tasks.")
	}
	return nil
}

func (s *StopCmd) stopByLabel(store *state.Store) error {
	ids, err := store.ListIDs()
	if err != nil {
		return err
	}
	stopped := 0
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil || !hasAnyLabel(meta.Labels, s.Labels) {
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
		fmt.Printf("No running tasks with the specified label(s).\n")
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
		_ = process.SignalKill(pid)
		killChildIfVerified(store, id)
		return nil
	}
	gracefulStopTask(store, id, pid, s.Timeout)
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

// killChildIfVerified kills the child process only after verifying the PID
// hasn't been reused by an unrelated process (via create-time comparison).
func killChildIfVerified(store *state.Store, id string) {
	childPID, _ := store.ReadPID(id, "child.pid")
	if childPID <= 0 || !process.IsAlive(childPID) {
		return
	}
	savedCreate := store.ReadChildCreateTime(id)
	if process.VerifyPID(childPID, savedCreate) {
		_ = process.SignalKill(childPID)
	}
}

// gracefulStopTask sends a stop signal to the supervisor, waits up to the
// given timeout, then escalates to SIGKILL. Also terminates the child process
// to prevent orphans.
func gracefulStopTask(store *state.Store, id string, supervisorPID int, timeout time.Duration) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	if runtime.GOOS == "windows" {
		// On Windows, TerminateProcess kills instantly without cleanup.
		// Try the ctl file first for graceful shutdown, then fall back to
		// TerminateProcess if the supervisor doesn't exit within the timeout.
		ctlWorked := false
		if err := process.SignalStop(supervisorPID); err == nil {
			ctlTicks := int(timeout / (100 * time.Millisecond))
			if ctlTicks < 1 {
				ctlTicks = 1
			}
			for i := 0; i < ctlTicks; i++ {
				if !process.IsAlive(supervisorPID) {
					ctlWorked = true
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		if !ctlWorked {
			_ = process.SignalTerm(supervisorPID)
			time.Sleep(200 * time.Millisecond)
		}
	} else {
		_ = process.SignalTerm(supervisorPID)
	}

	// Wait for the process to exit (may already be done from ctl file path).
	ticks := int(timeout / (100 * time.Millisecond))
	if ticks < 1 {
		ticks = 1
	}
	for i := 0; i < ticks; i++ {
		if !process.IsAlive(supervisorPID) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if process.IsAlive(supervisorPID) {
		_ = process.SignalKill(supervisorPID)
		// On Windows, give the OS a moment to finalize process termination.
		if runtime.GOOS == "windows" {
			time.Sleep(500 * time.Millisecond)
		}
	}
	// Ensure child is also terminated in case the supervisor didn't have
	// a chance to clean up (e.g., it was killed by SIGKILL escalation).
	killChildIfVerified(store, id)
}

// RestartCmd restarts a running task (kills child, respawns immediately).
type RestartCmd struct {
	Name   []string `arg:"" optional:"" help:"Task name(s) or ID(s)."`
	Labels []string `short:"l" name:"labels" help:"Restart all tasks with this label (repeatable)."`
	Force  bool     `help:"Force restart (SIGKILL child)."`
}

func (r *RestartCmd) Run(store *state.Store) error {
	if len(r.Name) == 0 && len(r.Labels) == 0 {
		return fmt.Errorf("provide a task name or --labels")
	}

	if len(r.Labels) > 0 {
		return r.restartByLabel(store)
	}

	for _, name := range r.Name {
		if err := r.restartOne(store, name); err != nil {
			return err
		}
	}
	return nil
}

func (r *RestartCmd) restartByLabel(store *state.Store) error {
	ids, err := store.ListIDs()
	if err != nil {
		return err
	}
	restarted := 0
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil || !hasAnyLabel(meta.Labels, r.Labels) {
			continue
		}
		pid, alive := verifyAndGetPID(store, id)
		if !alive {
			continue
		}
		if err := r.sendRestart(store, id, pid); err == nil {
			lipgloss.Printf("Restarted: %s\n", ui.Bold.Render(meta.Name))
			restarted++
		}
	}
	if restarted == 0 {
		fmt.Printf("No running tasks with the specified label(s).\n")
	}
	return nil
}

func (r *RestartCmd) restartOne(store *state.Store, nameOrID string) error {
	id, meta, err := store.Resolve(nameOrID)
	if err != nil {
		return err
	}

	pid, alive := verifyAndGetPID(store, id)
	if !alive {
		return fmt.Errorf("task %s is not running; use \"bgtask start %s\" to re-launch it", meta.Name, meta.Name)
	}

	if err := r.sendRestart(store, id, pid); err != nil {
		return err
	}

	lipgloss.Printf("Restarted: %s\n", ui.Bold.Render(meta.Name))
	return nil
}

func (r *RestartCmd) sendRestart(store *state.Store, id string, pid int) error {
	if r.Force {
		killChildIfVerified(store, id)
	}
	if err := process.SignalRestart(pid); err != nil {
		return fmt.Errorf("send restart signal: %w", err)
	}
	time.Sleep(200 * time.Millisecond)
	return nil
}

// StartCmd starts a stopped task by re-launching its supervisor.
type StartCmd struct {
	Name   []string `arg:"" optional:"" help:"Task name(s) or ID(s)."`
	Labels []string `short:"l" name:"labels" help:"Start all stopped tasks with this label (repeatable)."`
}

func (s *StartCmd) Run(store *state.Store) error {
	if len(s.Name) == 0 && len(s.Labels) == 0 {
		return fmt.Errorf("provide a task name or --labels")
	}

	if len(s.Labels) > 0 {
		return s.startByLabel(store)
	}

	for _, name := range s.Name {
		if err := s.startOne(store, name); err != nil {
			return err
		}
	}
	return nil
}

func (s *StartCmd) startByLabel(store *state.Store) error {
	// Lock to prevent concurrent starts from spawning duplicate supervisors.
	unlock, err := store.Lock()
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer unlock()

	ids, err := store.ListIDs()
	if err != nil {
		return err
	}
	started := 0
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil || !hasAnyLabel(meta.Labels, s.Labels) {
			continue
		}
		_, alive := verifyAndGetPID(store, id)
		if alive {
			continue // already running
		}
		if err := s.launchSupervisor(store, id, meta); err == nil {
			started++
		}
	}
	if started == 0 {
		fmt.Printf("No stopped tasks with the specified label(s).\n")
	}
	return nil
}

func (s *StartCmd) startOne(store *state.Store, nameOrID string) error {
	// Lock to prevent concurrent starts from spawning duplicate supervisors.
	unlock, err := store.Lock()
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer unlock()

	id, meta, err := store.Resolve(nameOrID)
	if err != nil {
		return err
	}

	_, alive := verifyAndGetPID(store, id)
	if alive {
		return fmt.Errorf("task %s is already running", meta.Name)
	}

	return s.launchSupervisor(store, id, meta)
}

func (s *StartCmd) launchSupervisor(store *state.Store, id string, meta *state.Meta) error {
	// Kill any orphan child from a dead supervisor.
	killChildIfVerified(store, id)

	// Clear previous exit state so the supervisor starts fresh.
	if err := store.ClearExit(id); err != nil {
		return fmt.Errorf("clear exit state: %w", err)
	}

	supervisorArgs := []string{"supervisor", store.Root, id}
	proc, err := process.Detach(supervisorArgs)
	if err != nil {
		return fmt.Errorf("detach supervisor: %w", err)
	}

	lipgloss.Printf("Started: %s (pid: %d)\n", ui.Bold.Render(meta.Name), proc.Pid)
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

// LabelCmd sets labels on an existing task.
type LabelCmd struct {
	Name   string   `arg:"" help:"Task name or ID."`
	Labels []string `arg:"" optional:"" help:"Labels to set (replaces existing labels)."`
}

func (l *LabelCmd) Run(store *state.Store) error {
	id, _, err := store.Resolve(l.Name)
	if err != nil {
		return err
	}
	if err := validation.ValidateLabels(l.Labels); err != nil {
		return err
	}
	if err := store.SetLabels(id, l.Labels); err != nil {
		return err
	}
	if len(l.Labels) == 0 {
		lipgloss.Printf("Cleared labels: %s\n", ui.Bold.Render(l.Name))
	} else {
		lipgloss.Printf("Labels set: %s → %s\n", ui.Bold.Render(l.Name), strings.Join(l.Labels, ", "))
	}
	return nil
}

// RmCmd removes a task (stop + delete state).
type RmCmd struct {
	Name   []string `arg:"" optional:"" help:"Task name(s) or ID(s)."`
	Labels []string `short:"l" name:"labels" help:"Remove all tasks with this label (repeatable)."`
	Force  bool     `help:"Force stop (SIGKILL) before removing."`
	All    bool     `short:"a" help:"Remove all tasks."`
}

func (r *RmCmd) Run(store *state.Store) error {
	if len(r.Name) == 0 && len(r.Labels) == 0 && !r.All {
		return fmt.Errorf("provide a task name, --labels, or --all")
	}

	if r.All {
		return r.rmAll(store)
	}

	if len(r.Labels) > 0 {
		return r.rmByLabel(store)
	}

	for _, name := range r.Name {
		if err := r.rmOne(store, name); err != nil {
			return err
		}
	}
	return nil
}

func (r *RmCmd) rmAll(store *state.Store) error {
	ids, err := store.ListIDs()
	if err != nil {
		return err
	}

	if len(ids) == 0 {
		fmt.Println("No tasks to remove.")
		return nil
	}

	removed := 0
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil {
			continue
		}
		pid, alive := verifyAndGetPID(store, id)
		if alive {
			if r.Force {
				_ = process.SignalKill(pid)
				killChildIfVerified(store, id)
			} else {
				gracefulStopTask(store, id, pid, 10*time.Second)
			}
		}
		if err := store.Remove(id); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", meta.Name, err)
			continue
		}
		lipgloss.Printf("Removed: %s\n", ui.Bold.Render(meta.Name))
		removed++
	}
	if removed == 0 {
		fmt.Println("No tasks to remove.")
	}
	return nil
}

func (r *RmCmd) rmByLabel(store *state.Store) error {
	ids, err := store.ListIDs()
	if err != nil {
		return err
	}

	// Collect matching candidates first.
	type candidate struct {
		id   string
		meta *state.Meta
	}
	var candidates []candidate
	for _, id := range ids {
		meta, err := store.ReadMeta(id)
		if err != nil || !hasAnyLabel(meta.Labels, r.Labels) {
			continue
		}
		candidates = append(candidates, candidate{id, meta})
	}

	if len(candidates) == 0 {
		fmt.Printf("No tasks with the specified label(s).\n")
		return nil
	}

	removed := 0
	for _, c := range candidates {
		freshPID, nowAlive := verifyAndGetPID(store, c.id)
		if nowAlive {
			if r.Force {
				_ = process.SignalKill(freshPID)
				killChildIfVerified(store, c.id)
			} else {
				gracefulStopTask(store, c.id, freshPID, 10*time.Second)
			}
		}
		if err := store.Remove(c.id); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", c.meta.Name, err)
			continue
		}
		lipgloss.Printf("Removed: %s\n", ui.Bold.Render(c.meta.Name))
		removed++
	}
	if removed == 0 {
		fmt.Printf("No tasks with the specified label(s).\n")
	}
	return nil
}

func (r *RmCmd) rmOne(store *state.Store, nameOrID string) error {
	id, meta, err := store.Resolve(nameOrID)
	if err != nil {
		return err
	}

	// Stop if running, using the appropriate method.
	pid, alive := verifyAndGetPID(store, id)
	if alive {
		if r.Force {
			_ = process.SignalKill(pid)
			killChildIfVerified(store, id)
		} else {
			gracefulStopTask(store, id, pid, 10*time.Second)
		}
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
