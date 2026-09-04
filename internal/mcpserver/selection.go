package mcpserver

import (
	"fmt"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

// SelectionInput is the wire format for taskservice.Selection: exactly one
// of Refs, Labels, or All must be set (see parseSelection). Mirrors
// server.SelectionJSON.
type SelectionInput struct {
	Refs   []string `json:"refs,omitempty" jsonschema:"Explicit task names or IDs to target. Processed one at a time, in order; processing stops at the first failure. Exactly one of refs, labels, or all must be set."`
	Labels []string `json:"labels,omitempty" jsonschema:"Target every task that has at least one of these labels (OR match). Processed best-effort: one task's failure does not stop the others. Exactly one of refs, labels, or all must be set."`
	All    bool     `json:"all,omitempty" jsonschema:"Target every task. Processed best-effort. Exactly one of refs, labels, or all must be set."`
}

func selectionMode(sel SelectionInput) string {
	switch {
	case len(sel.Refs) > 0:
		return "refs"
	case len(sel.Labels) > 0:
		return "labels"
	case sel.All:
		return "all"
	default:
		return ""
	}
}

// parseSelection validates that sel specifies exactly one selection mode
// and converts it to a taskservice.Selection.
func parseSelection(sel SelectionInput) (taskservice.Selection, error) {
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
	if len(sel.Refs) > maxBatchItems {
		return taskservice.Selection{}, fmt.Errorf("refs must contain at most %d tasks", maxBatchItems)
	}
	return taskservice.Selection{Names: sel.Refs, Labels: sel.Labels, All: sel.All}, nil
}
