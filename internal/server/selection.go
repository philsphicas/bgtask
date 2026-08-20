package server

import (
	"fmt"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

// SelectionJSON is the wire format for taskservice.Selection: exactly one
// of Refs, Labels, or All must be set (see parseSelection). It is not used
// for "cleanup", which needs no selection at all.
type SelectionJSON struct {
	Refs   []string `json:"refs,omitempty"`
	Labels []string `json:"labels,omitempty"`
	All    bool     `json:"all,omitempty"`
}

// parseSelection validates that sel specifies exactly one selection mode
// and converts it to a taskservice.Selection.
func parseSelection(sel SelectionJSON) (taskservice.Selection, error) {
	modes := 0
	if len(sel.Refs) > 0 {
		modes++
	}
	if len(sel.Labels) > 0 {
		modes++
	}
	if sel.All {
		modes++
	}
	if modes != 1 {
		return taskservice.Selection{}, fmt.Errorf("selection must specify exactly one of refs, labels, or all")
	}
	return taskservice.Selection{Names: sel.Refs, Labels: sel.Labels, All: sel.All}, nil
}
