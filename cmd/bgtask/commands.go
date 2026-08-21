package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	"github.com/philsphicas/bgtask/internal/taskservice"
	"github.com/philsphicas/bgtask/internal/ui"
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

func (r *RunCmd) Run(svc *taskservice.Service) error {
	args := r.Args

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

	// Parse env overrides.
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

	result, err := svc.Run(context.Background(), taskservice.RunRequest{
		Name:            r.Name,
		Command:         args,
		Cwd:             cwd,
		EnvOverrides:    envOverrides,
		Labels:          r.Labels,
		Restart:         r.Restart,
		RestartDelay:    r.RestartDelay,
		HealthCheck:     r.Health,
		HealthInterval:  r.HealthInterval,
		AutoRm:          r.Rm,
		ReplaceExisting: true, // preserve historical CLI behavior: replace a duplicate name
	})
	if err != nil {
		return err
	}

	lipgloss.Printf("Started: %s (id: %s, pid: %d)\n", ui.Bold.Render(result.Task.Meta.Name), result.Task.ID, result.PID)
	if result.NoBreakaway {
		fmt.Fprintf(os.Stderr, "warning: could not break away from job object; task may not survive session exit\n")
	}
	if result.ImmediateExit != nil && result.ImmediateExit.Code != 0 {
		fmt.Fprintf(os.Stderr, "warning: task exited immediately (code %d). Check: bgtask logs %s\n", result.ImmediateExit.Code, result.Task.Meta.Name)
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

func (l *LsCmd) Run(svc *taskservice.Service) error {
	result, err := svc.List(context.Background(), taskservice.ListRequest{Labels: l.Labels})
	if err != nil {
		return err
	}

	type taskInfo struct {
		Name      string           `json:"name"`
		ID        string           `json:"id"`
		Status    state.TaskStatus `json:"status"`
		Labels    []string         `json:"labels,omitempty"`
		CreatedAt time.Time        `json:"created_at"`
		Command   []string         `json:"command"`
	}

	tasks := make([]taskInfo, 0, len(result.Tasks))
	for _, t := range result.Tasks {
		tasks = append(tasks, taskInfo{
			Name:      t.Meta.Name,
			ID:        t.ID,
			Status:    t.Status,
			Labels:    t.Meta.Labels,
			CreatedAt: t.Meta.CreatedAt,
			Command:   t.Meta.Command,
		})
	}

	if l.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}

	if len(tasks) == 0 {
		if len(l.Labels) > 0 {
			// Distinguish "the store is empty" from "nothing matched the
			// label filter" -- both print a different message.
			all, err := svc.List(context.Background(), taskservice.ListRequest{})
			if err == nil && len(all.Tasks) == 0 {
				fmt.Println("No tasks.")
			} else {
				fmt.Println("No tasks with the specified label(s).")
			}
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

func (s *StatusCmd) Run(svc *taskservice.Service) error {
	result, err := svc.Get(context.Background(), s.Name)
	if err != nil {
		return err
	}
	id := result.Task.ID
	meta := result.Task.Meta
	ts := result.Task.Status

	if s.JSON {
		info := map[string]interface{}{
			"name":       meta.Name,
			"id":         id,
			"command":    meta.Command,
			"cwd":        meta.Cwd,
			"created_at": meta.CreatedAt,
			"restart":    meta.Restart,
			"status":     ts,
			"log":        result.Task.LogPath,
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
	kv("Log:       ", result.Task.LogPath)

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

func (l *LogsCmd) Run(svc *taskservice.Service) error {
	result, err := svc.Logs(context.Background(), taskservice.LogsRequest{
		Ref:    l.Name,
		Tail:   l.Tail,
		Since:  l.Since,
		All:    l.All,
		Stdout: l.Stdout,
		Stderr: l.Stderr,
	})
	if err != nil {
		return err
	}
	return showLogs(result, l.Follow, l.Stdout, l.Stderr, l.Timestamps)
}

// StopCmd stops a running task.
type StopCmd struct {
	Name    []string      `arg:"" optional:"" help:"Task name(s) or ID(s)."`
	Labels  []string      `short:"l" name:"labels" help:"Stop all tasks with this label (repeatable)."`
	Force   bool          `help:"Force stop (SIGKILL)."`
	Timeout time.Duration `help:"Graceful shutdown timeout." default:"10s"`
	All     bool          `short:"a" help:"Stop all running tasks."`
}

func (s *StopCmd) Run(svc *taskservice.Service) error {
	if len(s.Name) == 0 && len(s.Labels) == 0 && !s.All {
		return fmt.Errorf("provide a task name, --labels, or --all")
	}

	sel := taskservice.Selection{Names: s.Name, Labels: s.Labels, All: s.All}
	result, err := svc.Stop(context.Background(), taskservice.StopRequest{
		Selection: sel,
		Force:     s.Force,
		Timeout:   s.Timeout,
	})
	bulk := len(s.Name) == 0
	if err != nil && !bulk {
		return err
	}
	if result == nil {
		return err
	}

	stopped := 0
	for _, item := range result.Items {
		switch {
		case item.Err != nil:
			// Only reachable for a bulk (labels/all) selection, where
			// individual failures are tolerated and simply not counted.
			continue
		case item.Result.NoOp:
			if !bulk {
				fmt.Printf("Task %s is not running.\n", item.Task.Meta.Name)
			}
		case item.Result.Changed:
			lipgloss.Printf("Stopped: %s\n", ui.Bold.Render(item.Task.Meta.Name))
			stopped++
		}
	}
	if bulk && stopped == 0 {
		if len(s.Labels) > 0 {
			fmt.Printf("No running tasks with the specified label(s).\n")
		} else {
			fmt.Println("No running tasks.")
		}
	}
	return nil
}

// RestartCmd restarts a running task (kills child, respawns immediately).
type RestartCmd struct {
	Name   []string `arg:"" optional:"" help:"Task name(s) or ID(s)."`
	Labels []string `short:"l" name:"labels" help:"Restart all tasks with this label (repeatable)."`
	Force  bool     `help:"Force restart (SIGKILL child)."`
}

func (r *RestartCmd) Run(svc *taskservice.Service) error {
	if len(r.Name) == 0 && len(r.Labels) == 0 {
		return fmt.Errorf("provide a task name or --labels")
	}

	sel := taskservice.Selection{Names: r.Name, Labels: r.Labels}
	result, err := svc.Restart(context.Background(), taskservice.RestartRequest{Selection: sel, Force: r.Force})
	bulk := len(r.Name) == 0
	if err != nil && !bulk {
		if taskservice.IsFailedPrecondition(err) && result != nil && len(result.Items) > 0 {
			last := result.Items[len(result.Items)-1]
			name := last.Ref
			if last.Task != nil {
				name = last.Task.Meta.Name
			}
			return fmt.Errorf("task %s is not running; use \"bgtask start %s\" to re-launch it", name, name)
		}
		return err
	}
	if result == nil {
		return err
	}

	restarted := 0
	for _, item := range result.Items {
		if item.Err != nil {
			// Bulk selection: not-running/not-found tasks are silently
			// skipped, matching the CLI's historical behavior.
			continue
		}
		if item.Result.Changed {
			lipgloss.Printf("Restarted: %s\n", ui.Bold.Render(item.Task.Meta.Name))
			restarted++
		}
	}
	if bulk && restarted == 0 {
		fmt.Printf("No running tasks with the specified label(s).\n")
	}
	return nil
}

// StartCmd starts a stopped task by re-launching its supervisor.
type StartCmd struct {
	Name   []string `arg:"" optional:"" help:"Task name(s) or ID(s)."`
	Labels []string `short:"l" name:"labels" help:"Start all stopped tasks with this label (repeatable)."`
}

func (s *StartCmd) Run(svc *taskservice.Service) error {
	if len(s.Name) == 0 && len(s.Labels) == 0 {
		return fmt.Errorf("provide a task name or --labels")
	}

	sel := taskservice.Selection{Names: s.Name, Labels: s.Labels}
	result, err := svc.Start(context.Background(), taskservice.StartRequest{Selection: sel})
	bulk := len(s.Name) == 0
	if err != nil && !bulk {
		if taskservice.IsFailedPrecondition(err) && result != nil && len(result.Items) > 0 {
			last := result.Items[len(result.Items)-1]
			name := last.Ref
			if last.Task != nil {
				name = last.Task.Meta.Name
			}
			return fmt.Errorf("task %s is already running", name)
		}
		return err
	}
	if result == nil {
		return err
	}

	started := 0
	for _, item := range result.Items {
		if item.Err != nil {
			// Bulk selection: already-running/not-found tasks are silently
			// skipped, matching the CLI's historical behavior.
			continue
		}
		lipgloss.Printf("Started: %s (pid: %d)\n", ui.Bold.Render(item.Task.Meta.Name), item.Result.PID)
		if item.Result.NoBreakaway {
			fmt.Fprintf(os.Stderr, "warning: could not break away from job object; task may not survive session exit\n")
		}
		started++
	}
	if bulk && started == 0 {
		fmt.Printf("No stopped tasks with the specified label(s).\n")
	}
	return nil
}

// RenameCmd renames a task.
type RenameCmd struct {
	OldName string `arg:"" help:"Current task name or ID."`
	NewName string `arg:"" help:"New name for the task."`
}

func (r *RenameCmd) Run(svc *taskservice.Service) error {
	_, err := svc.Rename(context.Background(), taskservice.RenameRequest{Ref: r.OldName, NewName: r.NewName})
	if err != nil {
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

func (l *LabelCmd) Run(svc *taskservice.Service) error {
	_, err := svc.SetLabels(context.Background(), taskservice.SetLabelsRequest{Ref: l.Name, Labels: l.Labels})
	if err != nil {
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

func (r *RmCmd) Run(svc *taskservice.Service) error {
	if len(r.Name) == 0 && len(r.Labels) == 0 && !r.All {
		return fmt.Errorf("provide a task name, --labels, or --all")
	}

	sel := taskservice.Selection{Names: r.Name, Labels: r.Labels, All: r.All}
	result, err := svc.Remove(context.Background(), taskservice.RemoveRequest{Selection: sel, Force: r.Force})
	bulk := len(r.Name) == 0
	if err != nil && !bulk {
		return err
	}
	if result == nil {
		return err
	}

	printEmpty := func() {
		if r.All {
			fmt.Println("No tasks to remove.")
		} else {
			fmt.Printf("No tasks with the specified label(s).\n")
		}
	}

	if bulk && len(result.Items) == 0 {
		printEmpty()
		return nil
	}

	removed := 0
	for _, item := range result.Items {
		if item.Err != nil {
			// Only reachable for a bulk (labels/all) selection, where
			// individual failures are tolerated: warn and keep going.
			name := item.Ref
			if item.Task != nil {
				name = item.Task.Meta.Name
			}
			fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", name, item.Err)
			continue
		}
		lipgloss.Printf("Removed: %s\n", ui.Bold.Render(item.Task.Meta.Name))
		removed++
	}
	if bulk && removed == 0 {
		printEmpty()
	}
	return nil
}

// CleanupCmd removes state for all non-running tasks.
type CleanupCmd struct{}

func (c *CleanupCmd) Run(svc *taskservice.Service) error {
	result, err := svc.Cleanup(context.Background(), taskservice.CleanupRequest{})
	if err != nil {
		return err
	}

	removed := 0
	for _, item := range result.Items {
		if item.Err != nil {
			name := item.Ref
			if item.Task != nil {
				name = item.Task.Meta.Name
			}
			fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", name, item.Err)
			continue
		}
		if item.Result.Changed {
			fmt.Printf("Removed: %s (%s)\n", item.Task.Meta.Name, item.TaskID)
			removed++
		}
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

func (s *SupervisorCmd) Run(_ *taskservice.Service) error {
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
