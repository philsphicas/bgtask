package state

import (
	"os"
	"path/filepath"
	"runtime"
)

const stateDirOverrideEnv = "BGTASK_STATE_DIR"

// configDir returns the platform-appropriate config directory for bgtask.
func configDir() (string, error) {
	// Explicit per-invocation override takes highest priority.
	if override := os.Getenv(stateDirOverrideEnv); override != "" {
		return override, nil
	}

	switch runtime.GOOS {
	case "windows":
		// Check XDG_CONFIG_HOME first (used by tests and power users).
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "bgtask"), nil
		}
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "bgtask"), nil
	case "darwin":
		// macOS: ~/Library/Application Support
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "bgtask"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "bgtask"), nil
	default:
		// Linux: XDG Base Directory
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "bgtask"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "bgtask"), nil
	}
}
