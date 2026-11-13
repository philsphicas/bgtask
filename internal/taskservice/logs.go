package taskservice

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/philsphicas/bgtask/internal/state"
	"github.com/philsphicas/bgtask/internal/supervisor"
)

// Logs returns log entries for a task, applying tail/since/all/stream
// filters. It avoids reading older rotated log files once either:
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

	logFiles := state.OpenLogSnapshot(s.Store.OutputPath(id))
	// follow is the newest file handed to the caller; it must stay open.
	var follow *os.File
	defer func() {
		for _, lf := range logFiles {
			if lf.File != nil && lf.File != follow {
				_ = lf.File.Close()
			}
		}
	}()
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
	var newestEnd int64
	for i, lf := range logFiles {
		// An older file that is never reached cannot fail the request.
		if lf.File == nil {
			return nil, Internal(op, req.Ref, id, lf.Err)
		}
		raw, end, err := readLogFile(lf.File)
		if err != nil {
			return nil, Internal(op, req.Ref, id, err)
		}
		if i == 0 {
			newestEnd = end
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

	// The newest file is always read first, so it is positioned exactly
	// after the entries above and a follower misses nothing written since.
	if req.Follow && len(logFiles) > 0 {
		if _, err := logFiles[0].File.Seek(newestEnd, io.SeekStart); err != nil {
			return nil, Internal(op, req.Ref, id, err)
		}
		follow = logFiles[0].File
		result.Follow = follow
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

// readLogFile reads all JSONL entries from an open log file, oldest first.
// end is the offset just past the last record consumed. A trailing fragment
// that is not yet a complete record is excluded, so a follower resuming at
// end reads it once the writer has finished it.
func readLogFile(f *os.File) (entries []supervisor.LogEntry, end int64, err error) {
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var e supervisor.LogEntry
			parsed := json.Unmarshal(line, &e) == nil
			if parsed {
				entries = append(entries, e)
			}
			if parsed || readErr == nil {
				end += int64(len(line))
			}
		}
		if readErr == io.EOF {
			return entries, end, nil
		}
		if readErr != nil {
			return entries, end, readErr
		}
	}
}
