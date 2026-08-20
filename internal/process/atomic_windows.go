//go:build windows

package process

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// isRetryableRenameErr reports whether err from os.Rename on Windows looks
// like a transient condition worth retrying: another process briefly
// holding an incompatible handle open on the destination (sharing
// violation), a momentary access-denied (common with antivirus scanners),
// or a byte-range lock violation.
func isRetryableRenameErr(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case windows.ERROR_SHARING_VIOLATION, windows.ERROR_ACCESS_DENIED, windows.ERROR_LOCK_VIOLATION:
			return true
		}
	}
	return errors.Is(err, os.ErrPermission)
}
