// Package process provides cross-platform process management.
//
// Platform-specific implementations are in process_unix.go (shared Unix),
// process_linux.go, process_darwin.go, and process_windows.go.
package process

// VerifyPID checks that a PID still belongs to the same process by comparing
// creation times. Returns true if verified, or if verification is unavailable.
func VerifyPID(pid int, savedCreateTime int64) bool {
	if savedCreateTime == 0 {
		return true
	}
	current := CreateTime(pid)
	if current == 0 {
		return true
	}
	return current == savedCreateTime
}
