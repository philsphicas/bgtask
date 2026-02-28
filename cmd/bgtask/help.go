package main

import (
	"fmt"

	"github.com/alecthomas/kong"
)

func customHelpPrinter(options kong.HelpOptions, ctx *kong.Context) error {
	if ctx.Selected() != nil {
		return kong.DefaultHelpPrinter(options, ctx)
	}
	_, _ = fmt.Fprint(ctx.Stdout, topLevelHelp)
	return nil
}

const topLevelHelp = `bgtask - Background tasks you can find again.

Bgtask launches commands as supervised background processes and provides
tools to list, inspect, stop, and manage them. Each task gets a name, a
log file, and a supervisor that can restart it on failure. Tasks persist
across terminal sessions; use "ls" to find them again.

Usage:
  bgtask run [--name NAME] [--tag TAG] -- <command> [args...]
  bgtask ls [--json] [--tag TAG]
  bgtask status <name> [--json]
  bgtask logs <name> [-f] [-t N] [--since DUR] [--timestamps]
  bgtask stop <name> [--force] | stop --tag TAG
  bgtask pause <name>
  bgtask resume <name>
  bgtask rename <old-name> <new-name>
  bgtask rm <name>
  bgtask cleanup

Global Options:
  --version    Print version and exit
  --help, -h   Show this help message

Run:
  Launch a command as a supervised background process. A name is
  auto-generated from the command if --name is omitted. Prints the task
  name, ID, and PID on success. If the command exits immediately (e.g.
  typo or missing binary), a warning is printed to stderr.

    -n, --name NAME          Task name (auto-generated if omitted)
    -d, --dir DIR            Working directory for the command
    -e, --env KEY=VAL        Environment variable override (repeatable)
        --tag TAG            Tag for the task (repeatable, for bulk operations)
        --health CMD         Health check command (run periodically)
        --health-interval    Health check interval (default: 30s)
        --restart POLICY     Restart policy: always, on-failure
        --restart-delay DUR  Fixed restart delay (default: exponential backoff 1s-60s)
        --rm                 Auto-remove task state after exit

Ls:
  List all tasks in a table: name, ID, PID, status, ports, age, command.
  Status is one of: running, exited(N), dead. Listening TCP ports are
  auto-detected for running tasks.

        --json          Output as JSON array
        --tag STRING    Filter tasks by tag

Status:
  Show detailed information about a task: name, ID, command, working
  directory, supervisor/child PIDs, exit code, listening ports, and log
  file path.

        --json    Output as JSON object

Logs:
  Display task output (stdout and stderr interleaved in order). With -f,
  follows new output until the task exits.

    -f, --follow       Follow log output (blocks until task exits)
    -t, --tail N       Show last N lines only
        --since DUR    Show entries from the last duration (e.g. 5m, 1h)
        --stdout       Show only stdout
        --stderr       Show only stderr
        --timestamps   Prefix each line with ISO-8601 timestamp

Stop:
  Send SIGTERM and wait up to 10s for graceful shutdown, then escalate to
  SIGKILL. Use --force for immediate SIGKILL. Use --tag to stop all tasks
  matching a tag.

        --force         Force stop (SIGKILL immediately)
        --tag STRING    Stop all tasks with this tag

Pause / Resume:
  Pause suspends the child process (SIGSTOP); the supervisor stays alive.
  Resume continues it (SIGCONT).

Rm:
  Stop the task (if running) and delete all state including logs.

Cleanup:
  Remove state for all non-running tasks. Running tasks are left alone.

Naming:
  Tasks are identified by name or ID (a short hex string). Names must be
  unique. Auto-generated names are derived from the command basename with
  a random suffix (e.g. "npm-a1b2"). Use "rename" to change a name.

Example:
  # Start a dev server
  bgtask run --name devserver --tag project -- npm run dev

  # List running tasks
  bgtask ls

  # Follow logs
  bgtask logs devserver -f

  # Check detailed status (including ports)
  bgtask status devserver

  # Stop a specific task
  bgtask stop devserver

  # Stop all tasks tagged "project"
  bgtask stop --tag project

  # Clean up finished tasks
  bgtask cleanup

Run "bgtask <command> --help" for full flag details.
`
