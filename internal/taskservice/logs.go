package taskservice

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/philsphicas/bgtask/internal/supervisor"
)

// Logs returns log entries for a task, applying tail/since/all/stream
// filters. It avoids opening older rotated log files once either:
//
//   - a bounded tail request (req.Tail >= 0) has already collected enough
//     matching entries from newer files, or
//   - the scan has crossed the effective lower time bound (the current
//     run's start time, and/or a --since cutoff), since files rotated
//     before that point are entirely irrelevant.
//
// This keeps a single Logs call network-friendly: a bounded tail read on a
// task with many rotated logs need not load them all.
func (s *Service) Logs(ctx context.Context, req LogsRequest) (*LogsResult, error) {
	const op = "logs"
	if cerr := checkContext(op, req.Ref, ctx); cerr != nil {
		return nil, cerr
	}
	if req.Stdout && req.Stderr {
		return nil, InvalidArgument(op, req.Ref, "", "stdout and stderr are mutually exclusive")
	}

	id, _, err := s.resolve(op, req.Ref)
	if err != nil {
		return nil, err
	}

	var effectiveSince time.Time
	if !req.All {
		effectiveSince = s.Store.ReadChildStartTime(id)
	}
	if req.Since > 0 {
		cutoff := time.Now().Add(-req.Since)
		if effectiveSince.IsZero() || cutoff.After(effectiveSince) {
			effectiveSince = cutoff
		}
	}

	logFiles := s.Store.ListLogFiles(id) // newest first
	taskDir := s.Store.TaskDir(id)
	result := &LogsResult{
		HasAnyLogFile: len(logFiles) > 0,
		OutputPath:    s.Store.OutputPath(id),
		ExitPath:      filepath.Join(taskDir, "exit.json"),
	}

	// chunks holds, per scanned file (newest to oldest), that file's
	// stream+since-filtered entries. Concatenating chunks in reverse order
	// yields the overall chronological entry list.
	var chunks [][]supervisor.LogEntry
	matched := 0
	for _, f := range logFiles {
		raw, err := readLogFile(f)
		if err != nil {
			return nil, Internal(op, req.Ref, id, err)
		}
		filtered := filterEntries(raw, req.Stdout, req.Stderr, effectiveSince)
		chunks = append(chunks, filtered)
		matched += len(filtered)

		// Once this file's oldest raw entry predates the effective lower
		// bound, every older (rotated) file is entirely irrelevant.
		crossedBoundary := !effectiveSince.IsZero() && len(raw) > 0 && raw[0].Time.Before(effectiveSince)
		if crossedBoundary {
			break
		}
		if req.Tail >= 0 && matched >= req.Tail {
			break
		}
	}

	var entries []supervisor.LogEntry
	for i := len(chunks) - 1; i >= 0; i-- {
		entries = append(entries, chunks[i]...)
	}

	if req.Tail >= 0 {
		if req.Tail == 0 {
			entries = nil
		} else if len(entries) > req.Tail {
			entries = entries[len(entries)-req.Tail:]
		}
	}

	result.Entries = entries
	return result, nil
}

// filterEntries returns the entries in raw that pass the stream and
// since filters.
func filterEntries(raw []supervisor.LogEntry, stdoutOnly, stderrOnly bool, since time.Time) []supervisor.LogEntry {
	var out []supervisor.LogEntry
	for _, e := range raw {
		if stdoutOnly && e.Stream != "o" {
			continue
		}
		if stderrOnly && e.Stream != "e" {
			continue
		}
		if !since.IsZero() && e.Time.Before(since) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// readLogFile reads all JSONL entries from a single log file, oldest
// first (the order they were appended in). A missing file yields an empty
// slice, not an error.
func readLogFile(path string) ([]supervisor.LogEntry, error) {
	f, err := os.Open(path) //nolint:gosec // path is constructed from the store
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
		return entries, err
	}
	return entries, nil
}
