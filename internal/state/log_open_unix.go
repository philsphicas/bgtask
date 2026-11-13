//go:build !windows

package state

import "os"

// OpenLog opens a log for reading without preventing rotation.
func OpenLog(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // callers provide a task log path
}
