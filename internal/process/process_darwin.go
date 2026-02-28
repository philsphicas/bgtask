package process

import (
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// CreateTime returns an opaque process start-time identifier for PID reuse
// protection. On macOS this is the process start time in milliseconds since
// epoch, obtained via sysctl kern.proc.pid. Returns 0 if unavailable.
func CreateTime(pid int) int64 {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0
	}
	sec := int64(kp.Proc.P_starttime.Sec)
	usec := int64(kp.Proc.P_starttime.Usec)
	if sec <= 0 {
		return 0
	}
	return sec*1000 + usec/1000
}

// ListeningPorts returns the TCP ports that a process is listening on.
// On macOS, this uses lsof as there is no /proc filesystem.
func ListeningPorts(pid int) []uint32 {
	out, err := exec.Command(
		"lsof", "-a", "-nP", "-iTCP", "-sTCP:LISTEN",
		"-p", strconv.Itoa(pid),
		"-F", "n",
	).Output()
	if err != nil {
		return nil
	}

	var ports []uint32
	seen := make(map[uint32]bool)
	for _, line := range strings.Split(string(out), "\n") {
		// lsof -F n outputs lines like "n*:8080" or "n127.0.0.1:8080".
		if !strings.HasPrefix(line, "n") {
			continue
		}
		addr := line[1:]
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
