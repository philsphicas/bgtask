# bgtask -- Background tasks you can find again

*2026-03-07T06:44:34Z by Showboat dev*
<!-- showboat-id: 78d02cf4-0770-4436-a1a7-567a043d579d -->

bgtask launches commands in the background and lets you find them again by name. It captures structured logs, auto-restarts on failure, detects listening ports, and works across Linux, macOS, and Windows.

## Getting started

```bash
bgtask --help
```

```output
bgtask - Background tasks you can find again.

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
```

## Launching a task

Start a Python HTTP server in the background with an auto-detected name:

```bash
bgtask run -e PYTHONUNBUFFERED=1 -- python3 -m http.server 0
```

```output
Started: python3-ac3d (id: 20260306T224446-673ddd40, pid: 2993498)
```

Or specify a name explicitly:

```bash
bgtask run --name my-server -e PYTHONUNBUFFERED=1 -- python3 -m http.server 0
```

```output
Started: my-server (id: 20260306T224447-0a45e590, pid: 2993532)
```

## Listing tasks

The default view shows name, status (with uptime), detected ports, age, and command:

```bash
bgtask ls
```

```output
 NAME          STATUS        PORTS   AGE  COMMAND                  
───────────────────────────────────────────────────────────────────
 python3-ac3d  running (2s)  :37885  2s   python3 -m http.server 0 
 my-server     running (1s)  :43259  1s   python3 -m http.server 0 
```

Use \`--wide\` to see all columns including ID, PID, and labels:

```bash
bgtask ls --wide
```

```output
 NAME          ID                        PID      STATUS        PORTS   LABELS  AGE  COMMAND                  
──────────────────────────────────────────────────────────────────────────────────────────────────────────────
 python3-ac3d  20260306T224446-673ddd40  2993498  running (2s)  :37885  -       2s   python3 -m http.server 0 
 my-server     20260306T224447-0a45e590  2993532  running (1s)  :43259  -       1s   python3 -m http.server 0 
```

## Task details

Get detailed status including ports, PIDs, and log file path:

```bash
bgtask status my-server
```

```output
Name:       my-server
ID:         20260306T224447-0a45e590
Command:    python3 -m http.server 0
Cwd:        /home/phil/code/bgtask
Created:    2026-03-06T22:44:47-08:00
Restart:    no
Status:     running
Supervisor: PID 2993532 (running)
Child:      PID 2993541 (running)
Ports:      :43259
Since:      2026-03-06T22:44:47-08:00
Log:        /home/phil/.config/bgtask/procs/20260306T224447-0a45e590/output.jsonl
Env overrides:
  PYTHONUNBUFFERED=1
```

## Hitting the server

Extract the detected port from JSON output and curl it:

```bash
curl -s --head http://localhost:$(bgtask status --json my-server | jq -r ".status.running.ports[0]")/ | head -n1
```

```output
HTTP/1.0 200 OK
```

## Viewing logs

Logs are captured from stdout and stderr. By default, only the current run is shown:

```bash
bgtask logs --tail 5 my-server
```

```output
Serving HTTP on 0.0.0.0 port 43259 (http://0.0.0.0:43259/) ...
127.0.0.1 - - [06/Mar/2026 22:45:00] "HEAD / HTTP/1.1" 200 -
```

Use \`--all\` to include logs from previous runs (across restarts):

```bash
bgtask logs --all --tail 5 my-server
```

```output
Serving HTTP on 0.0.0.0 port 43259 (http://0.0.0.0:43259/) ...
127.0.0.1 - - [06/Mar/2026 22:45:00] "HEAD / HTTP/1.1" 200 -
```

## Renaming a task

```bash
bgtask rename python3-ac3d web-backend
```

```output
Renamed: python3-ac3d → web-backend
```

```bash
bgtask ls
```

```output
 NAME         STATUS         PORTS   AGE  COMMAND                  
───────────────────────────────────────────────────────────────────
 web-backend  running (25s)  :37885  25s  python3 -m http.server 0 
 my-server    running (24s)  :43259  24s  python3 -m http.server 0 
```

## Restarting a task

Restart sends SIGHUP to the supervisor, which kills the child and respawns it immediately:

```bash
bgtask restart my-server
```

```output
Restarted: my-server
```

```bash
bgtask ls
```

```output
 NAME         STATUS         PORTS   AGE  COMMAND                  
───────────────────────────────────────────────────────────────────
 web-backend  running (26s)  :37885  26s  python3 -m http.server 0 
 my-server    running (1s)   :39277  25s  python3 -m http.server 0 
```

Notice the STATUS shows "running (1s)" for my-server — the child just restarted — while AGE still shows the original creation time.

## Stopping a task

Stop sends SIGTERM and waits for graceful shutdown (default 10s), then escalates to SIGKILL:

```bash
bgtask stop my-server
```

```output
Stopped: my-server
```

```bash
bgtask ls
```

```output
 NAME         STATUS               PORTS   AGE  COMMAND                  
─────────────────────────────────────────────────────────────────────────
 web-backend  running (35s)        :37885  35s  python3 -m http.server 0 
 my-server    exited(-1) (0s ago)  -       33s  python3 -m http.server 0 
```

## Starting a stopped task

Re-launch a stopped task. A new supervisor is created using the original command and configuration:

```bash
bgtask start my-server
```

```output
Started: my-server (pid: 2994279)
```

```bash
bgtask ls
```

```output
 NAME         STATUS         PORTS   AGE  COMMAND                  
───────────────────────────────────────────────────────────────────
 web-backend  running (36s)  :37885  36s  python3 -m http.server 0 
 my-server    running (1s)   :37321  34s  python3 -m http.server 0 
```

## Auto-restart

Use \`--restart always\` to automatically restart a task whenever it exits (any exit code), or \`--restart on-failure\` for non-zero exits only:

```bash
bgtask run --name ticker --restart always -- bash -c "echo tick && sleep 2"
```

```output
Started: ticker (id: 20260306T224531-61685562, pid: 2994474)
```

After a few seconds, we can see the task has restarted multiple times:

```bash
bgtask logs --all ticker
```

```output
tick
restarting (code=0) attempt=1 delay=1s
tick
restarting (code=0) attempt=2 delay=2s
```

## Labels and bulk operations

Labels let you organize and operate on groups of tasks. Assign labels at launch:

```bash
bgtask run --name api --labels dev -- sleep 300
```

```output
Started: api (id: 20260306T224546-d8034a64, pid: 2994717)
```

```bash
bgtask run --name worker --labels dev -- sleep 300
```

```output
Started: worker (id: 20260306T224546-465436fa, pid: 2994742)
```

```bash
bgtask ls --labels dev --wide
```

```output
 NAME    ID                        PID      STATUS        PORTS  LABELS  AGE  COMMAND   
────────────────────────────────────────────────────────────────────────────────────────
 worker  20260306T224546-465436fa  2994742  running (0s)  -      dev     0s   sleep 300 
 api     20260306T224546-d8034a64  2994717  running (1s)  -      dev     1s   sleep 300 
```

Stop all tasks with a label:

```bash
bgtask stop --labels dev
```

```output
Stopped: worker
Stopped: api
```

Or set/change labels on existing tasks:

```bash
bgtask label api backend production
```

```output
Labels set: api → backend, production
```

```bash
bgtask ls --wide
```

```output
 NAME         ID                        PID      STATUS               PORTS   LABELS              AGE   COMMAND                  
─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 web-backend  20260306T224446-673ddd40  2993498  running (1m1s)       :37885  -                   1m1s  python3 -m http.server 0 
 my-server    20260306T224447-0a45e590  2994279  running (26s)        :37321  -                   1m0s  python3 -m http.server 0 
 worker       20260306T224546-465436fa  -        exited(-1) (0s ago)  -       dev                 1s    sleep 300                
 api          20260306T224546-d8034a64  -        exited(-1) (0s ago)  -       backend,production  1s    sleep 300                
```

## Environment overrides

Pass environment variables with \`-e\`:

```bash
bgtask run --name env-demo -e FOO=bar -e BAZ=qux -- bash -c "echo FOO=\$FOO BAZ=\$BAZ"
```

```output
Started: env-demo (id: 20260306T224558-658e5867, pid: 2994998)
```

```bash
bgtask logs env-demo
```

```output
FOO=bar BAZ=qux
exited (code=0)
```

## JSON output

Both \`ls --json\` and \`status --json\` produce structured JSON for scripting:

```bash
bgtask status --json my-server | jq .
```

```output
{
  "command": [
    "python3",
    "-m",
    "http.server",
    "0"
  ],
  "created_at": "2026-03-06T22:44:47.334895941-08:00",
  "cwd": "/home/phil/code/bgtask",
  "env_overrides": {
    "PYTHONUNBUFFERED": "1"
  },
  "id": "20260306T224447-0a45e590",
  "log": "/home/phil/.config/bgtask/procs/20260306T224447-0a45e590/output.jsonl",
  "name": "my-server",
  "restart": "",
  "status": {
    "state": "running",
    "running": {
      "supervisor_pid": 2994279,
      "child_pid": 2994288,
      "ports": [
        37321
      ],
      "since": "2026-03-06T22:45:21.28300927-08:00"
    }
  }
}
```

## Auto-remove

Use \`--rm\` to automatically clean up task state when it exits:

```bash
bgtask run --name ephemeral --rm -- echo "one-shot job done"
```

```output
Started: ephemeral (id: 20260306T224559-bbf51ade, pid: 2995078)
```

```bash
bgtask ls
```

```output
 NAME         STATUS                PORTS   AGE    COMMAND                        
──────────────────────────────────────────────────────────────────────────────────
 web-backend  running (1m14s)       :37885  1m14s  python3 -m http.server 0       
 my-server    running (39s)         :37321  1m13s  python3 -m http.server 0       
 worker       exited(-1) (13s ago)  -       13s    sleep 300                      
 api          exited(-1) (13s ago)  -       14s    sleep 300                      
 env-demo     exited(0) (1s ago)    -       1s     bash -c echo FOO=$FOO BAZ=$BAZ 
```

The ephemeral task ran and was automatically removed — it does not appear in \`ls\`.

## Cleanup

Remove all non-running tasks in one shot:

```bash
bgtask cleanup
```

```output
Removed: worker (20260306T224546-465436fa)
Removed: api (20260306T224546-d8034a64)
Removed: env-demo (20260306T224558-658e5867)
Cleaned up 3 task(s).
```

```bash
bgtask ls
```

```output
 NAME         STATUS           PORTS   AGE    COMMAND                  
───────────────────────────────────────────────────────────────────────
 web-backend  running (1m22s)  :37885  1m22s  python3 -m http.server 0 
 my-server    running (47s)    :37321  1m21s  python3 -m http.server 0 
```

Finally, stop and remove everything:

```bash
bgtask rm --all
```

```output
Removed: web-backend
Removed: my-server
```

```bash
bgtask ls
```

```output
No tasks.
```
