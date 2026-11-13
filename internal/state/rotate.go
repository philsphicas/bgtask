package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const (
	// MaxLogSize is the default max log file size before rotation (10 MB).
	MaxLogSize = 10 * 1024 * 1024
	// MaxLogFiles is the number of rotated log files to keep.
	MaxLogFiles = 3
)

// ShouldRotateLog checks if the output.jsonl exceeds maxBytes.
func (s *Store) ShouldRotateLog(id string, maxBytes int64) (bool, error) {
	logPath := s.OutputPath(id)
	info, err := os.Stat(logPath)
	if err != nil {
		return false, err
	}
	return info.Size() >= maxBytes, nil
}

// RotateLog rotates the output.jsonl file. The caller must ensure the file
// is not open (closed before calling). Rotated files: .1, .2, .3.
func (s *Store) RotateLog(id string) error {
	return s.NewLogRotation(id).Rotate()
}

// LogRotation tracks completed steps so a failed rotation can be retried
// without shifting or deleting the same backup twice.
type LogRotation struct {
	path string
	step int
}

func (s *Store) NewLogRotation(id string) *LogRotation {
	return &LogRotation{path: s.OutputPath(id), step: MaxLogFiles}
}

func (r *LogRotation) Rotate() error {
	for r.step > 0 {
		old := fmt.Sprintf("%s.%d", r.path, r.step)
		if r.step == MaxLogFiles {
			if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove oldest log: %w", err)
			}
		} else {
			newPath := fmt.Sprintf("%s.%d", r.path, r.step+1)
			if err := os.Rename(old, newPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("shift log %s: %w", old, err)
			}
		}
		r.step--
	}
	if r.step == 0 {
		if err := os.Rename(r.path, r.path+".1"); err != nil {
			return fmt.Errorf("rotate current log: %w", err)
		}
		r.step--
	}
	return nil
}

// ListLogFiles returns all log files for a task (current + rotated), newest first.
func (s *Store) ListLogFiles(id string) []string {
	logPath := s.OutputPath(id)
	var files []string

	if _, err := os.Stat(logPath); err == nil {
		files = append(files, logPath)
	}

	for i := 1; i <= MaxLogFiles; i++ {
		rotated := fmt.Sprintf("%s.%d", logPath, i)
		if _, err := os.Stat(rotated); err == nil {
			files = append(files, rotated)
		}
	}

	return files
}

// LogFile is one file of a log snapshot. File is nil when the file exists but
// could not be opened; Err then records why, so a reader fails only if it
// actually needs that file.
type LogFile struct {
	File *os.File
	Err  error
}

// OpenLogSnapshot opens a newest-first snapshot of the task's log files. A
// file can move while the names are scanned, so duplicate identities are
// discarded.
func OpenLogSnapshot(path string) []LogFile {
	return openLogSnapshot(path, OpenLog)
}

func openLogSnapshot(path string, open func(string) (*os.File, error)) []LogFile {
	var files []LogFile
	var infos []os.FileInfo
	for i := 0; i <= MaxLogFiles; i++ {
		name := path
		if i > 0 {
			name = fmt.Sprintf("%s.%d", path, i)
		}
		f, err := open(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			files = append(files, LogFile{Err: err})
			continue
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			files = append(files, LogFile{Err: err})
			continue
		}
		duplicate := false
		for _, previous := range infos {
			if os.SameFile(info, previous) {
				duplicate = true
				break
			}
		}
		if duplicate {
			_ = f.Close()
			continue
		}
		files = append(files, LogFile{File: f})
		infos = append(infos, info)
	}
	return files
}

// CloseLogSnapshot closes the handles returned by OpenLogSnapshot.
func CloseLogSnapshot(files []LogFile) {
	for _, f := range files {
		if f.File != nil {
			_ = f.File.Close()
		}
	}
}

// OpenLogFiles is like OpenLogSnapshot but fails if any file cannot be
// opened.
func OpenLogFiles(path string) ([]*os.File, error) {
	snapshot := OpenLogSnapshot(path)
	files := make([]*os.File, 0, len(snapshot))
	for _, f := range snapshot {
		if f.Err != nil {
			CloseLogSnapshot(snapshot)
			return nil, f.Err
		}
		files = append(files, f.File)
	}
	return files, nil
}

// CloseLogFiles closes the handles returned by OpenLogFiles.
func CloseLogFiles(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
