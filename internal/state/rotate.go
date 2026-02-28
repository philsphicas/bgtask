package state

import (
	"fmt"
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
	return s.rotateLog(s.OutputPath(id))
}

func (s *Store) rotateLog(logPath string) error {
	// Shift existing rotated files: .3 -> delete, .2 -> .3, .1 -> .2
	for i := MaxLogFiles; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", logPath, i)
		if i == MaxLogFiles {
			_ = os.Remove(old)
		} else {
			newPath := fmt.Sprintf("%s.%d", logPath, i+1)
			_ = os.Rename(old, newPath)
		}
	}

	// Current -> .1
	return os.Rename(logPath, logPath+".1")
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
