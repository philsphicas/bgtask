//go:build !windows

package process

// isRetryableRenameErr reports whether a rename error is worth retrying.
// POSIX rename(2) is atomic and does not fail with sharing violations, so
// on non-Windows platforms there is nothing to retry.
func isRetryableRenameErr(error) bool {
	return false
}
