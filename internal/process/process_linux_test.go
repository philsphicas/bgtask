package process

import (
	"net"
	"os"
	"testing"
)

func TestParseStarttime_Normal(t *testing.T) {
	// Realistic /proc/[pid]/stat line. Field 22 (0-indexed from after comm) is starttime.
	stat := "12345 (bash) S 1234 12345 12345 0 -1 4194304 500 0 0 0 10 5 0 0 20 0 1 0 98765 12345678 100 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0"
	got := parseStarttime(stat)
	if got != 98765 {
		t.Errorf("expected 98765, got %d", got)
	}
}

func TestParseStarttime_CommWithSpaces(t *testing.T) {
	// comm field can contain spaces and parentheses.
	stat := "12345 (my (weird) app) S 1234 12345 12345 0 -1 4194304 500 0 0 0 10 5 0 0 20 0 1 0 55555 12345678 100 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0"
	got := parseStarttime(stat)
	if got != 55555 {
		t.Errorf("expected 55555, got %d", got)
	}
}

func TestParseStarttime_Empty(t *testing.T) {
	if got := parseStarttime(""); got != 0 {
		t.Errorf("expected 0 for empty, got %d", got)
	}
}

func TestParseStarttime_Truncated(t *testing.T) {
	// Not enough fields after comm.
	stat := "12345 (bash) S 1234"
	if got := parseStarttime(stat); got != 0 {
		t.Errorf("expected 0 for truncated stat, got %d", got)
	}
}

func TestParseHexPort(t *testing.T) {
	tests := []struct {
		addr string
		want uint32
	}{
		{"0100007F:1F90", 8080},
		{"00000000:0050", 80},
		{"00000000:01BB", 443},
		{"", 0},
		{"nocolon", 0},
		{"00000000:ZZZZ", 0},
	}
	for _, tt := range tests {
		got := parseHexPort(tt.addr)
		if got != tt.want {
			t.Errorf("parseHexPort(%q) = %d, want %d", tt.addr, got, tt.want)
		}
	}
}

func TestCreateTime_Stable(t *testing.T) {
	pid := os.Getpid()
	ct1 := CreateTime(pid)
	ct2 := CreateTime(pid)
	ct3 := CreateTime(pid)
	if ct1 == 0 {
		t.Skip("cannot get create time")
	}
	if ct1 != ct2 || ct2 != ct3 {
		t.Errorf("CreateTime not stable: %d, %d, %d", ct1, ct2, ct3)
	}
}

func TestListeningPorts_ActiveListener(t *testing.T) {
	// Start a TCP listener and verify ListeningPorts finds it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := uint32(ln.Addr().(*net.TCPAddr).Port)
	ports := ListeningPorts(os.Getpid())

	found := false
	for _, p := range ports {
		if p == port {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected port %d in %v", port, ports)
	}
}
