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
	"github.com/philsphicas/bgtask/internal/ui"
)

func showLogs(logFiles []string, exitJSONPath string, follow bool, tail int, since time.Duration, sinceTime time.Time, stdoutOnly, stderrOnly, timestamps bool) error {
	if len(logFiles) == 0 {
		fmt.Println("No logs yet.")
		return nil
	}

	// Read entries from all log files (oldest first = reverse of logFiles,
	// which are returned newest-first by ListLogFiles).
	var entries []supervisor.LogEntry
	for i := len(logFiles) - 1; i >= 0; i-- {
		fileEntries, err := readLogFile(logFiles[i])
		if err != nil {
			return err
		}
		entries = append(entries, fileEntries...)
	}

	// Filter to current run (sinceTime from child.starttime).
	if !sinceTime.IsZero() {
		var filtered []supervisor.LogEntry
		for _, e := range entries {
			if !e.Time.Before(sinceTime) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	// Apply --since filter (relative duration).
	if since > 0 {
		cutoff := time.Now().Add(-since)
		var filtered []supervisor.LogEntry
		for _, e := range entries {
			if !e.Time.Before(cutoff) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	// Apply tail.
	if tail >= 0 {
		if tail == 0 {
			entries = nil
		} else if len(entries) > tail {
			entries = entries[len(entries)-tail:]
		}
	}

	// Print entries.
	for _, e := range entries {
		if stdoutOnly && e.Stream != "o" {
			continue
		}
		if stderrOnly && e.Stream != "e" {
			continue
		}
		printLogEntry(e, timestamps)
	}

	if !follow {
		return nil
	}

	// Follow mode: poll the current (newest) log file for new lines.
	f, err := os.Open(logFiles[0]) //nolint:gosec // path from store.OutputPath
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
				if eofCount%5 == 0 && exitJSONPath != "" {
					if _, statErr := os.Stat(exitJSONPath); statErr == nil {
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

// readLogFile reads all JSONL entries from a single log file.
func readLogFile(path string) ([]supervisor.LogEntry, error) {
	f, err := os.Open(path) //nolint:gosec // path is constructed from store
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []supervisor.LogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)
	for scanner.Scan() {
		var e supervisor.LogEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("read log %s: %w", path, err)
	}
	return entries, nil
}
