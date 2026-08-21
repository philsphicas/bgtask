package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/philsphicas/bgtask/internal/supervisor"
	"github.com/philsphicas/bgtask/internal/taskservice"
	"github.com/philsphicas/bgtask/internal/ui"
)

// showLogs prints the entries already resolved by taskservice.Service.Logs
// (respecting tail/since/all/stream filters), then optionally follows the
// task's current log file for new lines. The follow loop is CLI-only: it
// polls the file directly and is not part of the service's bounded read.
func showLogs(result *taskservice.LogsResult, follow, stdoutOnly, stderrOnly, timestamps bool) error {
	if !result.HasAnyLogFile {
		fmt.Println("No logs yet.")
		return nil
	}

	for _, e := range result.Entries {
		printLogEntry(e, timestamps)
	}

	if !follow {
		return nil
	}

	// Follow mode: poll the current (newest) log file for new lines.
	f, err := os.Open(result.OutputPath) //nolint:gosec // path from taskservice.LogsResult
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Seek to end so we only show new lines.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	eofCount := 0
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				eofCount++
				// Every ~1s (5 * 200ms), check if the task has exited.
				if eofCount%5 == 0 && result.ExitPath != "" {
					if _, statErr := os.Stat(result.ExitPath); statErr == nil {
						return nil
					}
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}
			return err
		}
		eofCount = 0
		var e supervisor.LogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if stdoutOnly && e.Stream != "o" {
			continue
		}
		if stderrOnly && e.Stream != "e" {
			continue
		}
		printLogEntry(e, timestamps)
	}
}

// milliTimestamp formats a time as RFC3339 with millisecond precision in UTC.
const milliFormat = "2006-01-02T15:04:05.000Z"

func printLogEntry(e supervisor.LogEntry, timestamps bool) {
	ts := ""
	if timestamps {
		ts = ui.Dim.Render(e.Time.UTC().Format(milliFormat)) + " "
	}
	switch e.Stream {
	case "o":
		if ts != "" {
			lipgloss.Print(ts)
		}
		fmt.Print(e.Data)
	case "e":
		if ts != "" {
			lipgloss.Print(ts)
		}
		fmt.Print(e.Data)
	case "x":
		detail := e.Data
		if e.Code != nil {
			detail += fmt.Sprintf(" (code=%d)", *e.Code)
		}
		if e.Attempt != nil {
			detail += fmt.Sprintf(" attempt=%d", *e.Attempt)
		}
		if e.Delay != "" {
			detail += fmt.Sprintf(" delay=%s", e.Delay)
		}
		if e.Message != "" {
			detail += fmt.Sprintf(" %s", e.Message)
		}
		if ts != "" {
			lipgloss.Print(ts)
		}
		lipgloss.Println(ui.Dim.Render(detail))
	}
}
