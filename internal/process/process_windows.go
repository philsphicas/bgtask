package process

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// IsAlive checks if a process with the given PID is still running.
// On Windows, OpenProcess can succeed for non-existent PIDs when pid%4 != 0
// (see https://devblogs.microsoft.com/oldnewthing/20080606-00/?p=22043),
// so we enumerate all PIDs for those cases.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if pid%4 != 0 {
		return pidInList(uint32(pid))
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// pidInList checks if a PID exists by enumerating all process IDs.
func pidInList(pid uint32) bool {
	var pids [4096]uint32
	var needed uint32
	if err := windows.EnumProcesses(pids[:], &needed); err != nil {
		return false
	}
	count := needed / 4
	for i := uint32(0); i < count; i++ {
		if pids[i] == pid {
			return true
		}
	}
	return false
}

// CreateTime returns an opaque process start-time identifier for PID reuse
// protection. On Windows this is the process creation time in 100-nanosecond
// intervals since January 1, 1601 (FILETIME). Returns 0 if unavailable.
func CreateTime(pid int) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	err = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user)
	if err != nil {
		return 0
	}
	return *(*int64)(unsafe.Pointer(&creation))
}

// SignalTerm terminates a process. Windows has no graceful termination signal;
// TerminateProcess is used for both term and kill.
func SignalTerm(pid int) error {
	return terminateProcess(pid)
}

// SignalKill forcefully terminates a process.
func SignalKill(pid int) error {
	return terminateProcess(pid)
}

func terminateProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// ListeningPorts returns the TCP ports that a process is listening on.
// On Windows, this uses netstat as a simple, reliable approach.
func ListeningPorts(pid int) []uint32 {
	out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return nil
	}

	pidStr := strconv.Itoa(pid)
	var ports []uint32
	seen := make(map[uint32]bool)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "LISTENING") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// Last field is PID.
		if fields[len(fields)-1] != pidStr {
			continue
		}
		// Second field is local address (e.g., "0.0.0.0:8080" or "[::]:8080").
		addr := fields[1]
		idx := strings.LastIndex(addr, ":")
		if idx < 0 {
			continue
		}
		port, err := strconv.ParseUint(addr[idx+1:], 10, 32)
		if err != nil {
			continue
		}
		p := uint32(port)
		if !seen[p] {
			ports = append(ports, p)
			seen[p] = true
		}
	}
	return ports
}
