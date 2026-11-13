package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/philsphicas/bgtask/internal/state"
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
	return followLogs(result.OutputPath, result.ExitPath, 200*time.Millisecond, nextLogFile, func(e supervisor.LogEntry) {
		if stdoutOnly && e.Stream != "o" {
			return
		}
		if stderrOnly && e.Stream != "e" {
			return
		}
		printLogEntry(e, timestamps)
	})
}

func followLogs(path, exitPath string, interval time.Duration, findNext func(*os.File, string) (*os.File, error), emit func(supervisor.LogEntry)) error {
	f, err := state.OpenLog(path)
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
	var pending string
	var next *os.File
	defer func() {
		if next != nil {
			_ = next.Close()
		}
	}()
	for {
		line, err := reader.ReadString('\n')
		pending += line
		if err != nil {
			if err == io.EOF {
				if next != nil {
					_ = f.Close()
					f = next
					next = nil
					reader = bufio.NewReader(f)
					pending = ""
					eofCount = 0
					continue
				}
				var nextErr error
				next, nextErr = findNext(f, path)
				if nextErr != nil {
					return nextErr
				}
				if next != nil {
					// Drain any writes that arrived after the first EOF,
					// before switching away from the rotated file.
					continue
				}
				eofCount++
				// Every ~1s (5 * 200ms), check if the task has exited.
				if eofCount%5 == 0 && exitPath != "" {
					if _, statErr := os.Stat(exitPath); statErr == nil {
						return nil
					}
				}
				time.Sleep(interval)
				continue
			}
			return err
		}
		eofCount = 0
		var e supervisor.LogEntry
		if err := json.Unmarshal([]byte(pending), &e); err != nil {
			pending = ""
			continue
		}
		pending = ""
		emit(e)
	}
}

// nextLogFile advances to the next surviving file in chronological order.
func nextLogFile(current *os.File, path string) (*os.File, error) {
	currentInfo, err := current.Stat()
	if err != nil {
		return nil, err
	}
	files, err := state.OpenLogFiles(path)
	if err != nil {
		return nil, err
	}
	currentIndex := -1
	for i, f := range files {
		info, err := f.Stat()
		if err != nil {
			state.CloseLogFiles(files)
			return nil, err
		}
		if os.SameFile(info, currentInfo) {
			currentIndex = i
		}
	}
	nextIndex := currentIndex - 1
	if currentIndex == -1 {
		nextIndex = len(files) - 1
	}
	var next *os.File
	for i, f := range files {
		if i == nextIndex {
			next = f
		} else {
			_ = f.Close()
		}
	}
	return next, nil
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
