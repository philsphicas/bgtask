package process

import (
	"os"
	"testing"
)

func TestIsAlive_Self(t *testing.T) {
	if !IsAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}
}

func TestIsAlive_Nonexistent(t *testing.T) {
	// PID 4194304 is above the default Linux pid_max (4194304), so it
	// should not exist on any reasonable system.
	if IsAlive(4194304) {
		t.Error("expected nonexistent PID to not be alive")
	}
}

func TestCreateTime_Self(t *testing.T) {
	ct := CreateTime(os.Getpid())
	if ct <= 0 {
		t.Errorf("expected positive create time for current process, got %d", ct)
	}
}

func TestCreateTime_Nonexistent(t *testing.T) {
	ct := CreateTime(4194304)
	if ct != 0 {
		t.Errorf("expected 0 for nonexistent PID, got %d", ct)
	}
}

func TestVerifyPID_ZeroSavedCreateTime(t *testing.T) {
	// When savedCreateTime is 0 (unavailable), VerifyPID should return true.
	if !VerifyPID(os.Getpid(), 0) {
		t.Error("expected true when savedCreateTime is 0")
	}
}

func TestVerifyPID_MatchingCreateTime(t *testing.T) {
	pid := os.Getpid()
	ct := CreateTime(pid)
	if ct == 0 {
		t.Skip("cannot get create time for current process")
	}
	if !VerifyPID(pid, ct) {
		t.Error("expected true for matching create time")
	}
}

func TestVerifyPID_MismatchedCreateTime(t *testing.T) {
	pid := os.Getpid()
	ct := CreateTime(pid)
	if ct == 0 {
		t.Skip("cannot get create time for current process")
	}
	// Use a deliberately wrong create time.
	if VerifyPID(pid, ct-1000000) {
		t.Error("expected false for mismatched create time")
	}
}

func TestVerifyPID_NonexistentPID(t *testing.T) {
	// When the PID doesn't exist, CreateTime returns 0, so VerifyPID
	// should return true (verification unavailable).
	if !VerifyPID(4194304, 12345) {
		t.Error("expected true when current create time is unavailable")
	}
}

func TestSignalTerm_Nonexistent(t *testing.T) {
	err := SignalTerm(4194304)
	if err == nil {
		t.Error("expected error for nonexistent PID")
	}
}

func TestSignalKill_Nonexistent(t *testing.T) {
	err := SignalKill(4194304)
	if err == nil {
		t.Error("expected error for nonexistent PID")
	}
}

func TestListeningPorts_NoPorts(t *testing.T) {
	// The test process shouldn't be listening on any TCP ports.
	ports := ListeningPorts(os.Getpid())
	if len(ports) != 0 {
		t.Errorf("expected no listening ports, got %v", ports)
	}
}

func TestListeningPorts_Nonexistent(t *testing.T) {
	ports := ListeningPorts(4194304)
	if ports != nil {
		t.Errorf("expected nil for nonexistent PID, got %v", ports)
	}
}
