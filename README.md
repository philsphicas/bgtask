# bgtask

[![CI](https://github.com/philsphicas/bgtask/actions/workflows/ci.yml/badge.svg)](https://github.com/philsphicas/bgtask/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/philsphicas/bgtask)](https://goreportcard.com/report/github.com/philsphicas/bgtask)
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

Download from [Releases](https://github.com/philsphicas/bgtask/releases) for Linux, macOS, and Windows (amd64/arm64).

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

## How it works

When you run `bgtask run`, the CLI:

1. Creates a task directory in `~/.config/bgtask/procs/<id>/` with metadata (`meta.json`)
2. Re-executes itself as a detached **supervisor** process (`bgtask supervisor`)
3. The supervisor starts the child command, captures stdout/stderr to a **JSONL log**, and manages lifecycle (restart, health checks)
4. PID files and process creation timestamps are stored for **PID reuse protection** -- bgtask verifies a process is actually yours before signaling it

State directory locations:

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

## License

[MIT](LICENSE)
