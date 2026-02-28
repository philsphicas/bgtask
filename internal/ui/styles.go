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
func StatusStyle(status string) lipgloss.Style {
	switch {
	case status == "running":
		return Green
	case status == "dead":
		return Yellow
	case len(status) > 7 && status[:7] == "exited(":
		if status == "exited(0)" {
			return Dim
		}
		return Red
	default:
		return lipgloss.NewStyle()
	}
}
