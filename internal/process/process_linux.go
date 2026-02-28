package process

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// CreateTime returns an opaque process start-time identifier for PID reuse
// protection. On Linux this is the raw starttime in clock ticks from
// /proc/[pid]/stat (field 22), which is monotonic and immune to wall-clock
// adjustments (NTP, WSL clock sync). Returns 0 if unavailable.
func CreateTime(pid int) int64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	return parseStarttime(string(data))
}

// parseStarttime extracts field 22 (starttime) from /proc/[pid]/stat.
// The comm field (field 2) is wrapped in parentheses and may contain
// spaces, so we find the last ')' to skip past it reliably.
func parseStarttime(stat string) int64 {
	// Find end of comm field: last occurrence of ')'.
	i := strings.LastIndex(stat, ")")
	if i < 0 || i+2 >= len(stat) {
		return 0
	}
	// Fields after comm start at index i+2 (skip ") ").
	// Field 3 is state, field 4 is ppid, ..., field 22 is starttime.
	// That's field index 19 (0-based) in the remaining fields.
	fields := strings.Fields(stat[i+2:])
	if len(fields) < 20 {
		return 0
	}
	v, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// ListeningPorts returns the TCP ports that a process is listening on.
func ListeningPorts(pid int) []uint32 {
	var ports []uint32
	seen := make(map[uint32]bool)

	for _, proto := range []string{"tcp", "tcp6"} {
		path := fmt.Sprintf("/proc/%d/net/%s", pid, proto)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, port := range parseProcNetTCP(string(data), pid) {
			if !seen[port] {
				ports = append(ports, port)
				seen[port] = true
			}
		}
	}
	return ports
}

// parseProcNetTCP parses /proc/[pid]/net/tcp{,6} and returns ports in
// LISTEN state (st=0A). We match against the process's socket inodes
// to filter to only this PID's sockets.
func parseProcNetTCP(data string, pid int) []uint32 {
	// Collect inodes owned by this process from /proc/[pid]/fd/.
	inodes := processInodes(pid)
	if len(inodes) == 0 {
		return nil
	}

	var ports []uint32
	lines := strings.Split(data, "\n")
	for _, line := range lines[1:] { // skip header
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		// Field 3 (0-based) is st (state). 0A = LISTEN.
		if fields[3] != "0A" {
			continue
		}
		// Field 9 is the inode.
		inode := fields[9]
		if !inodes[inode] {
			continue
		}
		// Field 1 is local_address (hex IP:port).
		port := parseHexPort(fields[1])
		if port > 0 {
			ports = append(ports, port)
		}
	}
	return ports
}

// processInodes returns the set of socket inodes for a process.
func processInodes(pid int) map[string]bool {
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return nil
	}
	inodes := make(map[string]bool)
	for _, e := range entries {
		link, err := os.Readlink(fmt.Sprintf("%s/%s", fdDir, e.Name()))
		if err != nil {
			continue
		}
		// Socket links look like "socket:[12345]".
		if strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
			inode := link[8 : len(link)-1]
			inodes[inode] = true
		}
	}
	return inodes
}

// parseHexPort extracts the port from a hex address like "0100007F:1F90".
func parseHexPort(addr string) uint32 {
	parts := strings.SplitN(addr, ":", 2)
	if len(parts) != 2 {
		return 0
	}
	port, err := strconv.ParseUint(parts[1], 16, 32)
	if err != nil {
		return 0
	}
	return uint32(port)
}
