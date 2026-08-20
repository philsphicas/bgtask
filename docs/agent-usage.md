# Using bgtask from an agent

`bgtask serve` exposes the same tasks and state directory as the CLI through
REST and MCP Streamable HTTP. An agent can launch work, inspect status, read
logs, and control task lifecycles without parsing terminal output.

## Start the server

Run the server in the foreground:

```bash
bgtask serve
```

Or let bgtask supervise its own server:

```bash
bgtask run --name bgtask-api \
  --restart on-failure \
  --health "curl -fsS http://127.0.0.1:8420/healthz" \
  -- bgtask serve
```

REST and MCP are both enabled by default:

| Surface | URL                             |
| ------- | ------------------------------- |
| Health  | `http://127.0.0.1:8420/healthz` |
| REST    | `http://127.0.0.1:8420/api/v1`  |
| MCP     | `http://127.0.0.1:8420/mcp`     |

Use `--expose rest` or `--expose mcp` to enable only one surface.

## Windows agent to WSL server

Build and run bgtask inside WSL, listening on all WSL interfaces:

```bash
# Run inside WSL.
go build -o /tmp/bgtask ./cmd/bgtask
BGTASK_STATE_DIR=/tmp/bgtask-state \
  /tmp/bgtask serve --bind 0.0.0.0 --port 8420
```

From Windows, try localhost first:

```powershell
Invoke-RestMethod http://127.0.0.1:8420/healthz
```

WSL localhost forwarding depends on the Windows/WSL networking configuration.
If localhost is unavailable, use the WSL VM address:

```powershell
$wslIP = ((wsl.exe -- bash -lc 'hostname -I').Trim() -split '\s+')[0]
$bgtask = "http://${wslIP}:8420"

Invoke-RestMethod "$bgtask/healthz" -NoProxy
```

The WSL VM address can change after WSL restarts. Rediscover it before updating
an MCP client configuration.

> [!WARNING]
> bgtask does not currently authenticate HTTP clients. Binding to `0.0.0.0`
> gives every client that can reach the WSL address the ability to execute
> commands and read task output. Use firewall rules or an SSH tunnel when the
> address is reachable outside your machine.

## Configure an MCP client

Point a Streamable HTTP MCP client at the `/mcp` endpoint. For a client running
on the same OS as bgtask:

```json
{
  "mcpServers": {
    "bgtask": {
      "type": "http",
      "url": "http://127.0.0.1:8420/mcp",
      "tools": ["*"]
    }
  }
}
```

For a Windows client talking to WSL, replace `127.0.0.1` with the WSL address
discovered above.

GitHub Copilot cloud agent cannot connect directly to a private WSL address.
This setup is intended for an MCP client running on the same Windows/WSL
machine. A cloud client requires a separately secured, routable endpoint.

The server exposes these tools:

| Tool                | Purpose                                   |
| ------------------- | ----------------------------------------- |
| `bgtask_list`       | List tasks, optionally filtered by labels |
| `bgtask_get`        | Get task configuration and current status |
| `bgtask_run`        | Launch a supervised background command    |
| `bgtask_logs`       | Read a bounded log snapshot               |
| `bgtask_start`      | Re-launch stopped tasks                   |
| `bgtask_stop`       | Gracefully or forcibly stop tasks         |
| `bgtask_restart`    | Restart running tasks                     |
| `bgtask_rename`     | Rename a task                             |
| `bgtask_set_labels` | Replace task labels                       |
| `bgtask_remove`     | Stop and permanently remove tasks         |
| `bgtask_cleanup`    | Remove state for every non-running task   |

MCP task creation does not replace a duplicate name unless
`replace_existing: true` is explicitly supplied.

## Start a one-off Copilot CLI agent

Copilot CLI can add the bgtask MCP server for one session without changing the
global MCP configuration:

```powershell
$wslIP = ((wsl.exe -- bash -lc 'hostname -I').Trim() -split '\s+')[0]
$mcpConfig = @{
  mcpServers = @{
    bgtask = @{
      type = "http"
      url = "http://${wslIP}:8420/mcp"
      tools = @("*")
    }
  }
} | ConvertTo-Json -Depth 6 -Compress

$prompt = @'
Use only the bgtask MCP tools. Do not call shell, PowerShell, or wsl.exe.

Start a bgtask named `quoting-proof` in `/tmp` with this argv list:
["bash", "-lc", "printf 'value: <%s>\n' \"$DEMO_VALUE\"; printf 'alpha\nbeta\n' | grep beta"]
Set DEMO_VALUE to `hello from MCP`, wait for the task to exit, then report its
status and stdout.
'@

copilot -p $prompt `
  --additional-mcp-config $mcpConfig `
  --disable-builtin-mcps `
  --allow-all-tools `
  --no-custom-instructions `
  --no-ask-user
```

In an end-to-end Windows-to-WSL run, the agent used exactly
`bgtask_run` → `bgtask_get` → `bgtask_logs`; it did not invoke a shell tool or
`wsl.exe`. The Linux task exited with code 0 and returned:

```text
value with spaces: <hello from MCP agent>
pipe result: beta
```

The important difference from launching through `wsl.exe` is that
`bgtask_run.command` is an argv-style JSON array. The shell program is one
argument in that array, so it does not pass through nested PowerShell,
`wsl.exe`, and Linux-shell quoting layers.

## Give an agent a task

Once the MCP server is configured, prompts can describe the desired lifecycle
instead of shell commands:

```text
Start `npm run dev` as a bgtask named `web`, label it `dev`, and wait until it
is running. Report any listening ports.
```

```text
Show the last 100 stderr entries from `worker`. If it exited unsuccessfully,
restart it and confirm its new status.
```

```text
Stop and remove every task labeled `preview`, but leave all other tasks alone.
```

Agents should prefer canonical task IDs returned by bgtask for later mutations.
Names remain supported for convenience.

## Verify with REST

REST is useful for setup checks and non-MCP automation:

```powershell
$body = @{
  name = "agent-demo"
  command = @("bash", "-lc", "echo started; sleep 2; echo done")
  labels = @("agent")
} | ConvertTo-Json

Invoke-RestMethod -Method Post `
  -Uri "$bgtask/api/v1/tasks" `
  -ContentType "application/json" `
  -Body $body `
  -NoProxy

Invoke-RestMethod "$bgtask/api/v1/tasks/agent-demo/logs?tail=20&all=true" -NoProxy
```

All CLI and server processes using the same `BGTASK_STATE_DIR` see the same
tasks. The servers do not maintain separate in-memory task databases.

## Logs

REST and MCP return bounded log snapshots:

- default: 200 entries
- maximum: 5000 entries
- call again to poll for new entries

Use the CLI when continuous follow behavior is needed:

```bash
bgtask logs -f agent-demo
```
