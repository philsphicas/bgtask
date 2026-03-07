// Package ui provides shared terminal styling for bgtask CLI output.
package ui

import (
	lipgloss "charm.land/lipgloss/v2"
)

var (
	Green  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	Red    = lipgloss.NewStyle().Foreground(lipgloss.Red)
	Yellow = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	Dim    = lipgloss.NewStyle().Faint(true)
	Bold   = lipgloss.NewStyle().Bold(true)

	// Label is for key-value output labels.
	Label = Dim
)

// StatusStyle returns the appropriate style for a task status string.
// Accepts state names, display strings, and strings with duration suffixes
// (e.g., "running (5m)", "exited(1) (2m ago)").
func StatusStyle(status string) lipgloss.Style {
	switch {
	case len(status) >= 7 && status[:7] == "running":
		return Green
	case status == "dead":
		return Red
	case len(status) >= 6 && status[:6] == "exited":
		if len(status) >= 9 && status[:9] == "exited(0)" {
			return Dim
		}
		return Red
	default:
		return lipgloss.NewStyle()
	}
}
