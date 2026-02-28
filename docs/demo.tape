# VHS demo for bgtask
# https://github.com/charmbracelet/vhs

Output docs/demo.gif

Require bgtask
Require python3
Require curl
Require jq

Set Shell "bash"
Set FontSize 14
Set Width 900
Set Height 500
Set Padding 10
Set TypingSpeed 40ms
Set WindowBar Colorful
Set LoopOffset 60%
Set PlaybackSpeed 1.5

Type "# bgtask: Background tasks you can find again."
Enter
Sleep 2s

Type "# Launch a background task"
Enter
Sleep 500ms

Type "bgtask run --name my-server -e PYTHONUNBUFFERED=1 -- python3 -m http.server 0"
Enter
Sleep 1.5s

Type "# List running tasks (port auto-detected)"
Enter
Sleep 500ms

Type "bgtask ls"
Enter
Sleep 2s

Type "# Curl the server using the detected port"
Enter
Sleep 500ms

Type "curl -s --head http://localhost:$(bgtask status --json my-server | jq '.ports[0]')/ | head -n1"
Enter
Sleep 2s

Type "# Check detailed status"
Enter
Sleep 500ms

Type "bgtask status my-server"
Enter
Sleep 3s

Type "# Stop the task"
Enter
Sleep 500ms

Type "bgtask stop my-server"
Enter
Sleep 1.5s

Type "# Logs persist after the task stops"
Enter
Sleep 500ms

Type "bgtask logs my-server"
Enter
Sleep 2s

Type "bgtask rm my-server"
Enter
Sleep 2s
