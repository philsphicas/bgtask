# bgtask -- Background tasks you can find again

*2026-03-03T17:46:37Z by Showboat dev*
<!-- showboat-id: b1f8aaa7-54f0-4ddb-9cde-b37e7d1ec3e8 -->

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
```

## Launching a task

Start a background task with `bgtask run`. Here we launch a Python HTTP server on a dynamic port (port 0 lets the OS pick a free one). We pass `-e PYTHONUNBUFFERED=1` so Python flushes output immediately -- without it, stdout is block-buffered when not connected to a TTY.

```bash
bgtask run --name my-server -e PYTHONUNBUFFERED=1 -- python3 -m http.server 0
```

```output
Started: my-server (id: 20260303T174637-dd478aa4, pid: 51)
```

## Listing tasks

`bgtask ls` shows all tasks with their status, PID, listening ports, and age. Notice that bgtask automatically detected the dynamically assigned port:

```bash
sleep 1 && bgtask ls
```

```output
 NAME       ID                        PID  STATUS   PORTS   AGE  COMMAND                  
──────────────────────────────────────────────────────────────────────────────────────────
 my-server  20260303T174637-dd478aa4  51   running  :37529  1s   python3 -m http.server 0 
```

## Task details

`bgtask status` shows detailed information including PIDs, detected ports, working directory, and creation time.

```bash
bgtask status my-server
```

```output
Name:       my-server
ID:         20260303T174637-dd478aa4
Command:    python3 -m http.server 0
Cwd:        /home/demo/project
Created:    2026-03-03T17:46:37Z
Restart:    no
Supervisor: PID 51 (running)
Child:      PID 59 (running)
Ports:      :37529
Log:        /home/demo/.config/bgtask/procs/20260303T174637-dd478aa4/output.jsonl
Env overrides:
  PYTHONUNBUFFERED=1
```

## Hitting the server

We can use `bgtask status --json` with `jq` to extract the detected port and curl it -- no need to know the port in advance:

```bash
curl -s --head http://localhost:$(bgtask status --json my-server | jq ".ports[0]")/ | head -n1
```

```output
HTTP/1.0 200 OK
```

## Viewing logs

Thanks to `PYTHONUNBUFFERED=1`, the server's startup message and access log appear immediately:

```bash
bgtask logs my-server
```

```output
Serving HTTP on 0.0.0.0 port 37529 (http://0.0.0.0:37529/) ...
127.0.0.1 - - [03/Mar/2026 17:46:38] "HEAD / HTTP/1.1" 200 -
```

Use `--timestamps` to prefix each line with its timestamp (millisecond precision, UTC):

```bash
bgtask logs --timestamps my-server
```

```output
2026-03-03T17:46:37.711Z Serving HTTP on 0.0.0.0 port 37529 (http://0.0.0.0:37529/) ...
2026-03-03T17:46:38.840Z 127.0.0.1 - - [03/Mar/2026 17:46:38] "HEAD / HTTP/1.1" 200 -
```

Use `--stderr` to filter by stream, or `--tail` and `--since` to filter by recency:

```bash
bgtask logs --stderr my-server
```

```output
127.0.0.1 - - [03/Mar/2026 17:46:38] "HEAD / HTTP/1.1" 200 -
```

## Renaming a task

```bash
bgtask rename my-server web-server
```

```output
Renamed: my-server → web-server
```

```bash
bgtask ls
```

```output
 NAME        ID                        PID  STATUS   PORTS   AGE  COMMAND                  
───────────────────────────────────────────────────────────────────────────────────────────
 web-server  20260303T174637-dd478aa4  51   running  :37529  1s   python3 -m http.server 0 
```

## Stopping a task

```bash
bgtask stop web-server
```

```output
Stopped: web-server
```

```bash
bgtask ls
```

```output
 NAME        ID                        PID  STATUS      PORTS  AGE     COMMAND                  
────────────────────────────────────────────────────────────────────────────────────────────────
 web-server  20260303T174637-dd478aa4  51   exited(-1)  -      0s ago  python3 -m http.server 0 
```

```bash
bgtask rm web-server
```

```output
Removed: web-server
```

## Auto-restart

`--restart always` restarts on any exit. `--restart on-failure` restarts only on non-zero exit. Both use exponential backoff (1s, 2s, 4s, ... up to 60s) unless `--restart-delay` sets a fixed delay.

```bash
bgtask run --name flaky --restart on-failure -- bash -xc "sleep 1 && exit 1"
```

```output
Started: flaky (id: 20260303T174639-205648e8, pid: 272)
```

```bash
sleep 6 && bgtask logs --timestamps flaky
```

```output
2026-03-03T17:46:39.150Z + sleep 1
2026-03-03T17:46:40.153Z + exit 1
2026-03-03T17:46:40.153Z restarting (code=1) attempt=1 delay=1s
2026-03-03T17:46:41.157Z + sleep 1
2026-03-03T17:46:42.160Z + exit 1
2026-03-03T17:46:42.161Z restarting (code=1) attempt=2 delay=2s
2026-03-03T17:46:44.165Z + sleep 1
2026-03-03T17:46:45.167Z + exit 1
2026-03-03T17:46:45.167Z restarting (code=1) attempt=3 delay=4s
```

The timestamps show the exponential backoff: 1s between the first two attempts, 2s before the third.

```bash
bgtask rm flaky
```

```output
Removed: flaky
```

## Pause and resume

Pause kills the child process; resume starts a **new** child (reinvocation, not a true resume). The supervisor stays alive throughout. To demonstrate, we use a counter that prints incrementing numbers -- after resume, the counter restarts from 0.

```bash
bgtask run --name counter -- bash -c "i=0; while true; do echo \$((i++)); sleep 1; done"
```

```output
Started: counter (id: 20260303T174645-889ad85b, pid: 340)
```

```bash
sleep 3 && bgtask logs --tail 5 counter
```

```output
0
1
2
3
```

Now pause it -- the child is killed:

```bash
bgtask pause counter
```

```output
Paused: counter
```

```bash
sleep 1 && bgtask logs --tail 5 counter
```

```output
1
2
3
paused
child_exited (code=-1) attempt=1
```

Resume -- a **new** child starts. The counter restarts from 0:

```bash
bgtask resume counter
```

```output
Resumed: counter
```

```bash
sleep 3 && bgtask logs --tail 8 counter
```

```output
3
paused
child_exited (code=-1) attempt=1
resumed
0
1
2
3
```

```bash
bgtask rm counter
```

```output
Removed: counter
```

## Tags and bulk operations

Tag tasks for bulk operations. Stop all tasks with a given tag at once.

```bash
bgtask run --tag dev --name api -- sleep 300
```

```output
Started: api (id: 20260303T174653-d84f12dc, pid: 471)
```

```bash
bgtask run --tag dev --name worker -- sleep 300
```

```output
Started: worker (id: 20260303T174653-b1572aa8, pid: 494)
```

```bash
sleep 0.2 && bgtask ls --tag dev
```

```output
 NAME    ID                        PID  STATUS   PORTS  AGE  COMMAND   
───────────────────────────────────────────────────────────────────────
 worker  20260303T174653-b1572aa8  494  running  -      0s   sleep 300 
 api     20260303T174653-d84f12dc  471  running  -      0s   sleep 300 
```

```bash
bgtask stop --tag dev
```

```output
Stopped: worker
Stopped: api
```

```bash
bgtask ls
```

```output
 NAME    ID                        PID  STATUS      PORTS  AGE     COMMAND   
─────────────────────────────────────────────────────────────────────────────
 worker  20260303T174653-b1572aa8  494  exited(-1)  -      0s ago  sleep 300 
 api     20260303T174653-d84f12dc  471  exited(-1)  -      0s ago  sleep 300 
```

```bash
bgtask cleanup
```

```output
Removed: worker (20260303T174653-b1572aa8)
Removed: api (20260303T174653-d84f12dc)
Cleaned up 2 task(s).
```

## Environment overrides

Pass environment variables to the task with `-e KEY=VAL`.

```bash
bgtask run --name echo-env -e MY_VAR=hello -- bash -c 'echo $MY_VAR'
```

```output
Started: echo-env (id: 20260303T174654-7a41b57a, pid: 575)
```

```bash
sleep 0.5 && bgtask logs echo-env
```

```output
hello
exited (code=0)
```

`bgtask status` shows the overrides:

```bash
bgtask status echo-env
```

```output
Name:       echo-env
ID:         20260303T174654-7a41b57a
Command:    bash -c echo $MY_VAR
Cwd:        /home/demo/project
Created:    2026-03-03T17:46:54Z
Restart:    no
Supervisor: PID 575 (dead)
Child:      PID 584 (dead)
Exit code:  0
Exited at:  2026-03-03T17:46:54Z
Log:        /home/demo/.config/bgtask/procs/20260303T174654-7a41b57a/output.jsonl
Env overrides:
  MY_VAR=hello
```

```bash
bgtask rm echo-env
```

```output
Removed: echo-env
```

## JSON output

Both `bgtask ls` and `bgtask status` support `--json` for scripting and integration.

```bash
bgtask run --name json-demo -- sleep 300
```

```output
Started: json-demo (id: 20260303T174655-ab9144a5, pid: 651)
```

```bash
sleep 0.2 && bgtask ls --json
```

```output
[
  {
    "name": "json-demo",
    "id": "20260303T174655-ab9144a5",
    "pid": 651,
    "status": "running",
    "age": "0s",
    "command": "sleep 300"
  }
]
```

```bash
bgtask rm json-demo
```

```output
Removed: json-demo
```

## Auto-remove

With `--rm`, task state is automatically deleted after the command exits. Useful for one-shot scripts.

```bash
bgtask run --rm --name oneshot -- bash -c "echo done"
```

```output
Started: oneshot (id: 20260303T174655-1f056b66, pid: 706)
```

```bash
sleep 0.5 && bgtask ls
```

```output
No tasks.
```

The task ran, exited cleanly, and its state was automatically removed -- `bgtask ls` shows no tasks.
