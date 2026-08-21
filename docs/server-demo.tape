# VHS demo for the bgtask REST and MCP server
# https://github.com/charmbracelet/vhs

Output docs/server-demo.gif

Require bgtask
Require curl
Require jq

Set Shell "bash"
Set FontSize 14
Set Width 1000
Set Height 560
Set Padding 10
Set TypingSpeed 30ms
Set WindowBar Colorful
Set LoopOffset 70%
Set PlaybackSpeed 1.35

Type "# Start the REST + MCP server as a supervised bgtask"
Enter
Sleep 1s

Type "bgtask run --name bgtask-api -- bgtask serve"
Enter
Sleep 2s

Type "# Verify the always-on health endpoint"
Enter
Type "curl -s http://127.0.0.1:8420/healthz | jq"
Enter
Sleep 1.5s

Type "# An agent launches a Linux command through the REST API"
Enter
Type `curl -sS -X POST http://127.0.0.1:8420/api/v1/tasks -H 'Content-Type: application/json' -d '{"name":"agent-job","command":["bash","-lc","echo from-agent; sleep 1; echo done"],"labels":["agent"]}' | jq '{id: .task.id, name: .task.name}'`
Enter
Sleep 2s

Type "# CLI and API see the same task state"
Enter
Type "bgtask ls"
Enter
Sleep 2s

Type "# Read structured logs over HTTP"
Enter
Type "curl -s 'http://127.0.0.1:8420/api/v1/tasks/agent-job/logs?tail=10&all=true' | jq '[.entries[] | {stream, data, message}]'"
Enter
Sleep 2.5s

Type "# MCP agents connect at http://127.0.0.1:8420/mcp"
Enter
Type "# Tools include bgtask_run, bgtask_list, bgtask_logs, and lifecycle control"
Enter
Sleep 2s

Type "# Clean up the agent task and the server"
Enter
Type "curl -sS -X DELETE http://127.0.0.1:8420/api/v1/tasks/agent-job | jq '{removed: .task.name}'"
Enter
Sleep 1s
Type "bgtask rm bgtask-api"
Enter
Sleep 2s
