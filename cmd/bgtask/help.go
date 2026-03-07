package main

import (
	"io"

	"github.com/alecthomas/kong"
)

func customHelpPrinter(options kong.HelpOptions, ctx *kong.Context) error {
	if ctx.Selected() != nil {
		return kong.DefaultHelpPrinter(options, ctx)
	}
	_, _ = io.WriteString(ctx.Stdout, topLevelHelp)
	return nil
}

const topLevelHelp = `bgtask - Background tasks you can find again.

Bgtask launches commands as supervised background processes and provides
tools to list, inspect, stop, and manage them. Each task gets a name, a
log file, and a supervisor that can restart it on failure. Tasks persist
across terminal sessions; use "ls" to find them again.

Usage:
  bgtask run [--name NAME] [--labels LABEL,...] -- <command> [args...]
  bgtask ls [--json] [--labels LABEL,...] [--wide]
  bgtask status <name> [--json]
  bgtask logs <name> [-f] [--tail N] [--since DUR] [--timestamps]
  bgtask stop <name...> [--force] [--timeout DUR] | stop --labels LABEL,... | stop --all
  bgtask restart <name...> | restart --labels LABEL,...
  bgtask start <name...> | start --labels LABEL,...
  bgtask rename <old-name> <new-name>
  bgtask label <name> [LABEL...]
  bgtask rm <name...> [--force] | rm --labels LABEL,... | rm --all
  bgtask cleanup

Global Options:
  --version    Print version and exit
  --help, -h   Show this help message

Run:
  Launch a command as a supervised background process. A name is
  auto-generated from the command basename if omitted (e.g. "npm-a1b2").
  Prints the task name, ID, and PID on success. If the command exits
  immediately (e.g. typo or missing binary), a warning is printed.

    -n, --name NAME           Task name (auto-generated if omitted)
    -d, --dir DIR             Working directory for the command
    -e, --env KEY=VAL         Environment variable override (repeatable)
    -l, --labels LABEL,...    Labels (comma-separated or repeated -l X -l Y)
        --health CMD          Health check command (run periodically)
        --health-interval DUR Health check interval (default: 30s)
        --restart POLICY      Restart policy: always, on-failure
        --restart-delay DUR   Fixed restart delay (default: exponential backoff 1s-60s)
        --rm                  Auto-remove task state after exit

  If a task with the same name exists, it is stopped and replaced.

  Restart policies:
    always      Restart whenever the child exits (any exit code)
    on-failure  Restart only on non-zero exit codes

  With --restart, health check failures also trigger restarts (after 3
  consecutive failures).

Ls:
  List all tasks in a table: name, status, ports, age, command. Use
  --wide for all columns (ID, PID, labels). The STATUS column includes
  uptime or exit duration (e.g. "running (5m)", "exited(0) (2m ago)").
  AGE shows how long the task has existed since creation.

    -j, --json          Output as JSON array
    -l, --labels LABEL,...  Filter tasks by label (comma-separated, OR semantics)
    -w, --wide          Show all columns (ID, PID, labels)
        --no-trunc      Do not truncate command output

  JSON output schema (ls --json):
    [{ "name", "id", "status": { "state": "running|exited|dead",
       "running?": { "supervisor_pid", "child_pid", "ports?", "since" },
       "exited?":  { "code", "at" },
       "dead?":    { "message" }
     }, "labels?": [...], "created_at", "command": [...] }]

Status:
  Show detailed information about a task: name, ID, command, working
  directory, supervisor/child PIDs, exit code, listening ports, and log
  file path. Accepts task name or ID.

    -j, --json    Output as JSON object

  JSON output schema (status --json):
    { "name", "id", "command": [...], "cwd", "created_at", "restart",
      "status": { "state", "running?|exited?|dead?": ... },
      "log", "labels?": [...], "env_overrides?": { "KEY": "VAL" },
      "restart_delay?", "health_check?", "health_interval?", "auto_rm?" }

Logs:
  Display task output (stdout and stderr interleaved in order). By
  default, only logs from the current/last run are shown. Use --all
  to include logs from previous runs (across restarts).

    -f, --follow       Follow log output (blocks until task exits)
        --tail N       Show last N lines only (0 = no output, omit = all)
        --since DUR    Show entries from the last duration (e.g. 5m, 1h)
    -a, --all          Show logs from all runs (default: current run only)
        --stdout       Show only stdout
        --stderr       Show only stderr
    -T, --timestamps   Prefix each line with ISO-8601 timestamp

Stop:
  Send SIGTERM and wait for graceful shutdown (default: 10s), then
  escalate to SIGKILL. Use --force for immediate SIGKILL. Use --labels to
  stop all tasks matching a label, or --all to stop everything.
  Accepts multiple names: bgtask stop foo bar baz.

        --force         Force stop (SIGKILL immediately)
        --timeout DUR   Graceful shutdown timeout (default: 10s)
    -l, --labels LABEL,...  Stop all tasks with these labels
    -a, --all           Stop all running tasks

Restart:
  Restart a running task: send SIGHUP to the supervisor, which kills the
  child process and respawns it immediately (no backoff). The supervisor
  stays alive throughout. Accepts multiple names.

    -l, --labels LABEL,...  Restart all tasks with these labels
        --force         Force restart (SIGKILL child)

Start:
  Re-launch a stopped/exited task. Creates a new supervisor process
  using the original command and configuration. Accepts multiple names.

    -l, --labels LABEL,...  Start all stopped tasks with these labels

Rename:
  Change a task's name. The task can be in any state (running, exited,
  or dead). Names must be unique across all tasks.

Rm:
  Stop the task (if running) and delete all state including logs.
  Accepts multiple names. Running tasks are gracefully stopped first
  (use --force for immediate SIGKILL).

        --force         Force stop (SIGKILL) before removing
    -l, --labels LABEL,...  Remove all tasks with these labels
    -a, --all           Remove all tasks

Cleanup:
  Remove state for all non-running tasks. Running tasks are left alone.

Label:
  Set or replace labels on an existing task. Pass one or more labels as
  space-separated arguments; pass no labels to clear them all.

    bgtask label myapp dev backend      # set two labels
    bgtask label myapp                  # clear all labels

  When using the --labels flag (on run, ls, stop, etc.), labels are
  comma-separated or repeated:

    bgtask run --name api --labels dev,backend -- ./server
    bgtask stop --labels dev
    bgtask ls -l frontend -l backend

  Label rules:
    - Must start with a letter (a-z, A-Z)
    - Allowed characters: letters, digits, dot, underscore, colon, hyphen
    - Must end with a letter or digit (if more than one character)
    - 1–63 characters; case-sensitive
    - Commas and spaces are not allowed
    - Use colon for namespacing (e.g. project:myapp, env:dev)

Naming:
  Tasks are identified by name or ID (a timestamp-based identifier).
  Names must be unique. Auto-generated names are derived from the command
  basename with a random suffix (e.g. "npm-a1b2"). Use "rename" to
  change a name.

Port detection:
  bgtask detects TCP ports opened by the child process and displays them
  in "ls" and "status" output. Detection reads from /proc (Linux) or
  lsof (macOS). Only ports held by the direct child PID are detected.

State directory:
  Task state (meta.json, logs, PIDs) is stored per-platform:
    Linux:   ~/.config/bgtask/procs/    (or $XDG_CONFIG_HOME/bgtask/procs/)
    macOS:   ~/Library/Application Support/bgtask/procs/
    Windows: %APPDATA%\bgtask\procs\    (or %XDG_CONFIG_HOME%\bgtask\procs\)

Example:
  # Start a dev server with labels
  bgtask run --name devserver --labels project,frontend -- npm run dev

  # Or use short flags
  bgtask run -n api -l backend,dev -e PORT=8080 -- ./server

  # List running tasks
  bgtask ls

  # Filter by label
  bgtask ls --labels backend

  # Follow logs
  bgtask logs devserver -f

  # Check detailed status (including ports)
  bgtask status devserver

  # Get port from JSON output
  bgtask status --json devserver | jq '.status.running.ports[0]'

  # Restart a task
  bgtask restart devserver

  # Stop multiple tasks
  bgtask stop devserver api worker

  # Stop all tasks labeled "project"
  bgtask stop --labels project

  # Stop all running tasks
  bgtask stop --all

  # Set labels on an existing task
  bgtask label devserver frontend dev

  # Clear labels
  bgtask label devserver

  # Force-remove a task
  bgtask rm --force devserver

  # Clean up finished tasks
  bgtask cleanup

Run "bgtask <command> --help" for full flag details.
`
