//go:build windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Detach starts a detached child process that survives the parent's exit.
func Detach(args []string) (*os.Process, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start detached: %w", err)
	}

	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return &os.Process{Pid: pid}, nil
}

// SignalPause writes a pause command to the control file.
func SignalPause(pid int) error {
	return writeCtlFile(pid, "pause")
}

// SignalResume writes a resume command to the control file.
func SignalResume(pid int) error {
	return writeCtlFile(pid, "resume")
}

func writeCtlFile(pid int, action string) error {
	// Resolve the procs directory using the same logic as state.configDir().
	// We can't import internal/state (would create a cycle), so we duplicate
	// the directory resolution here.
	var procsDir string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		procsDir = filepath.Join(xdg, "bgtask", "procs")
	} else {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		procsDir = filepath.Join(appData, "bgtask", "procs")
	}

	entries, err := os.ReadDir(procsDir)
	if err != nil {
		return fmt.Errorf("read procs dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pidFile := filepath.Join(procsDir, e.Name(), "supervisor.pid")
		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == fmt.Sprintf("%d", pid) {
			ctlFile := filepath.Join(procsDir, e.Name(), "ctl")
			tmpFile := ctlFile + ".tmp"
			if err := os.WriteFile(tmpFile, []byte(action), 0o600); err != nil {
				return err
			}
			return os.Rename(tmpFile, ctlFile)
		}
	}
	return fmt.Errorf("no task found with supervisor PID %d", pid)
}
