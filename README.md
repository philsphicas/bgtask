# bgtask

[![CI](https://github.com/philsphicas/bgtask/actions/workflows/ci.yml/badge.svg)](https://github.com/philsphicas/bgtask/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/philsphicas/bgtask)](https://github.com/philsphicas/bgtask/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Background tasks you can find again.

Launch long-running processes, close your terminal, and come back to them later -- by name.

![demo](https://github.com/philsphicas/bgtask/releases/download/assets/demo.gif)

## Why

You start a dev server, a tunnel, a build watcher. You close the terminal. Now what?

- `nohup` and `&` lose the output.
- `tmux`/`screen` require a session to be running.
- `systemd` units are overkill for ad-hoc dev tasks.

**bgtask** gives you named background tasks with structured logs, auto-restart, health checks, and a simple CLI to manage them.

## Install

### From source

```sh
go install github.com/philsphicas/bgtask/cmd/bgtask@latest
```

### Pre-built binaries

Download stable versions from [Releases](https://github.com/philsphicas/bgtask/releases).
Stable releases are built from `vMAJOR.MINOR.PATCH` tags on `main` and are
available for Linux and macOS (amd64/arm64) and Windows (amd64). Each release
includes a SHA-256 checksum manifest.

The rolling [`edge` prerelease](https://github.com/philsphicas/bgtask/releases/tag/edge)
tracks the latest commit on `main` that passed CI. Edge builds are development
snapshots for early testing: their tag and assets are replaced on each update,
and support is best effort. Use a stable release for production or long-lived
installations.

### Build locally

```sh
git clone https://github.com/philsphicas/bgtask.git
cd bgtask
make build    # output: bin/bgtask
```

## Quick start

```sh
# Start a background task
bgtask run --name api -- python3 server.py 8080

# List tasks
bgtask ls

# View logs (with follow)
bgtask logs -f api

# Stop
bgtask stop api
```

See the [full interactive demo](docs/demo.md) for more.

## Commands

| Command                       | Description                                      |
| ----------------------------- | ------------------------------------------------ |
| `bgtask run -- CMD [ARGS...]` | Launch a background task                         |
| `bgtask ls`                   | List all tasks                                   |
| `bgtask status NAME`          | Show detailed task info (PIDs, ports, exit code) |
| `bgtask logs NAME`            | View task logs                                   |
| `bgtask stop NAME`            | Stop a running task                              |
| `bgtask restart NAME`         | Restart a running task                           |
| `bgtask start NAME`           | Re-launch a stopped/exited task                  |
| `bgtask rename OLD NEW`       | Rename a task                                    |
| `bgtask rm NAME`              | Stop and delete a task                           |
| `bgtask cleanup`              | Remove all non-running task state                |
| `bgtask serve`                | Serve the REST API and MCP endpoint              |
| `bgtask completion`           | Output shell completion script                   |

## Features

### Named tasks

Give tasks a name with `--name` or let bgtask auto-generate one from the command:

```sh
bgtask run --name my-server -- ./server
bgtask logs my-server
bgtask stop my-server
```

### Auto-restart

Restart on any exit with exponential backoff (1s-60s), or only on failure:

```sh
bgtask run --restart always -- ./my-service
bgtask run --restart on-failure -- ./flaky-service
bgtask run --restart always --restart-delay 5s -- ./my-service
```

### Health checks

Run a command periodically to check task health. When a restart policy is set,
health check failures also trigger restarts (after 3 consecutive failures):

```sh
bgtask run --health "curl -sf http://localhost:8080/healthz" --health-interval 10s -- ./server
bgtask run --restart on-failure --health "curl -sf localhost:8080" -- ./server
```

### Restart

Restart a running task (kill child, respawn immediately):

```sh
bgtask restart my-server
bgtask restart --labels dev    # restart all labeled tasks
```

### Start a stopped task

Re-launch a task that has exited:

```sh
bgtask start my-server
bgtask start --labels dev      # start all stopped labeled tasks
```

### Labels and bulk operations

Label tasks for bulk stop, restart, or removal:

```sh
bgtask run --labels dev --name api -- ./api-server
bgtask run --labels dev --name worker -- ./worker
bgtask stop --labels dev    # stops both
bgtask stop --all          # stops everything
bgtask rm --labels dev      # removes both
bgtask rm --all          # removes all non-running tasks
bgtask rm --force my-server   # force-kill and remove
```

### Log viewing

Structured logs with filtering:

```sh
bgtask logs my-server              # all output
bgtask logs -f my-server           # follow (like tail -f)
bgtask logs --tail 50 my-server    # last 50 lines
bgtask logs --since 5m my-server   # last 5 minutes
bgtask logs --stdout my-server     # stdout only
bgtask logs --stderr my-server     # stderr only
```

### Port detection

`bgtask ls` and `bgtask status` automatically detect listening TCP ports for running tasks.

### Environment overrides

```sh
bgtask run -e PORT=9090 -e DEBUG=1 -- ./server
```

### JSON output

```sh
bgtask ls --json
bgtask status --json my-server
```

### Auto-remove

Automatically clean up task state after exit:

```sh
bgtask run --rm -- ./one-shot-script.sh
```

### Shell completions

```sh
bgtask completion             # install completions for your current shell
bgtask completion --uninstall # remove them
```

### REST API and MCP server

`bgtask serve` runs a foreground HTTP server backed by the same state directory
as the CLI:

```sh
bgtask serve
# {"event":"listening","addr":"127.0.0.1:8420","pid":12345}
```

REST and MCP are enabled by default. Either surface can be exposed alone:

```sh
bgtask serve --expose rest
bgtask serve --expose mcp
bgtask serve --expose rest --expose mcp
```

The REST API is mounted at `http://127.0.0.1:8420/api/v1`; MCP clients connect
to the Streamable HTTP endpoint at `http://127.0.0.1:8420/mcp`. A health check
is always available at `/healthz`.

To supervise the server with bgtask itself:

```sh
bgtask run --name bgtask-api \
  --restart on-failure \
  --health "curl -fsS http://127.0.0.1:8420/healthz" \
  -- bgtask serve
```

Multiple server and CLI processes can safely use the same state directory.
They do not cache an authoritative task view; each operation reads current
state and cross-process locks coordinate mutations.

#### REST examples

```sh
# List tasks
curl -s http://127.0.0.1:8420/api/v1/tasks

# Launch a task (duplicate names return 409 unless replace_existing is true)
curl -sS -X POST http://127.0.0.1:8420/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"name":"api","command":["python3","server.py","8080"]}'

# Inspect, stop, and remove it
curl -s http://127.0.0.1:8420/api/v1/tasks/api
curl -sS -X POST http://127.0.0.1:8420/api/v1/tasks/api/stop \
  -H "Content-Type: application/json" -d '{}'
curl -sS -X DELETE http://127.0.0.1:8420/api/v1/tasks/api

# Read the last 100 structured log entries
curl -s "http://127.0.0.1:8420/api/v1/tasks/api/logs?tail=100&all=true"
```

REST routes:

| Method          | Route                                                 | Operation                    |
| --------------- | ----------------------------------------------------- | ---------------------------- |
| `GET`, `POST`   | `/api/v1/tasks`                                       | List or launch tasks         |
| `GET`, `DELETE` | `/api/v1/tasks/{name-or-id}`                          | Inspect or remove a task     |
| `GET`           | `/api/v1/tasks/{name-or-id}/logs`                     | Read bounded structured logs |
| `POST`          | `/api/v1/tasks/{name-or-id}/{start,stop,restart}`     | Control lifecycle            |
| `POST`          | `/api/v1/tasks/{name-or-id}/rename`                   | Rename a task                |
| `PUT`           | `/api/v1/tasks/{name-or-id}/labels`                   | Replace labels               |
| `POST`          | `/api/v1/actions/{start,stop,restart,remove,cleanup}` | Bulk action                  |

Task responses include environment variable names but redact their values.
Commands, working directories, and logs are returned as stored.

#### MCP tools

The MCP endpoint exposes:

`bgtask_list`, `bgtask_get`, `bgtask_run`, `bgtask_logs`, `bgtask_start`,
`bgtask_stop`, `bgtask_restart`, `bgtask_rename`, `bgtask_set_labels`,
`bgtask_remove`, and `bgtask_cleanup`.

`bgtask_list` returns compact task summaries rather than complete task
configuration. It supports `states` and `labels` filters, returns 20 tasks by
default (maximum 100), and uses `next_cursor`/`cursor` for continuation. For
example, use `{"states":["running"]}` to inventory currently running tasks,
then call `bgtask_get` with a returned name or ID when exact argv, cwd, PIDs,
restart settings, or log paths are needed.

`bgtask_logs` is bounded by both entry count (default 100, maximum 2000) and
rendered bytes (default 32 KiB, maximum 128 KiB). Select `stream` as `all`,
`stdout`, or `stderr`; truncation is reported explicitly.

Lifecycle tools accept exactly one top-level selector: `refs`, `labels`, or
`all`. Their responses contain complete aggregate counts and at most 50
prioritized per-task details, rather than repeating full task configuration for
every affected task.

![REST and MCP server demo](https://github.com/philsphicas/bgtask/releases/download/assets/server-demo.gif)

Configure any MCP client that supports Streamable HTTP with:

```json
{
  "mcpServers": {
    "bgtask": {
      "type": "http",
      "url": "http://127.0.0.1:8420/mcp"
    }
  }
}
```

MCP and REST log reads are bounded snapshots; poll again for newer entries. MCP
uses the stricter count and byte limits described above. REST retains its
existing default of 200 entries and maximum of 5000. The CLI remains the
interface for following logs continuously with `bgtask logs -f`.

See [Using bgtask from an agent](docs/agent-usage.md) for Windows-to-WSL setup,
MCP configuration, tool descriptions, example agent prompts, and REST
verification commands.

> [!WARNING]
> The server does not implement authentication or TLS. Binding to a
> non-loopback address (for example, `--bind 0.0.0.0`) grants anyone who can
> reach the port the ability to execute commands and read task output. Protect
> remote access with firewall rules, an SSH tunnel, or an authenticated reverse
> proxy.

## How it works

When you run `bgtask run`, the CLI:

1. Creates a task directory in `~/.config/bgtask/procs/<id>/` with metadata (`meta.json`)
2. Re-executes itself as a detached **supervisor** process (`bgtask supervisor`)
3. The supervisor starts the child command, captures stdout/stderr to a **JSONL log**, and manages lifecycle (restart, health checks)
4. PID files and process creation timestamps are stored for **PID reuse protection** -- bgtask verifies a process is actually yours before signaling it

CLI and server mutations use owned filesystem leases. This allows multiple CLI
and server processes to share one state directory without maintaining separate
in-memory task databases.

State directory locations:

`BGTASK_STATE_DIR` can be set to the state root that contains `procs/` and takes precedence over platform defaults.

| Platform | Path                                    |
| -------- | --------------------------------------- |
| Linux    | `~/.config/bgtask/`                     |
| macOS    | `~/Library/Application Support/bgtask/` |
| Windows  | `%APPDATA%\bgtask\`                     |

### Known limitations

- **Process trees**: `bgtask stop` terminates the direct child process only.
  If the child forks subprocesses, those may not be terminated. For shell
  scripts that spawn background processes, consider using `exec` to replace
  the shell process.
- **Server security**: `bgtask serve` currently has no authentication or TLS.
- **Self-management**: If a supervised server stops, restarts, or removes
  itself through its own API, the request may disconnect before receiving the
  final response.

## License

[MIT](LICENSE)
