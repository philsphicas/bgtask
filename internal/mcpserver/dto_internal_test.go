package mcpserver

import (
	"strings"
	"testing"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestTaskSummary_CommandPreviewSanitizesControls(t *testing.T) {
	summary := toTaskSummary(taskservice.PublicTask{
		Command: []string{"printf", "first\nsecond\tvalue"},
	})

	if strings.ContainsAny(summary.CommandPreview, "\n\r\t") {
		t.Fatalf("command preview contains table-breaking controls: %q", summary.CommandPreview)
	}
	if summary.CommandPreview != "printf first second value" {
		t.Fatalf("command preview = %q, want sanitized text", summary.CommandPreview)
	}
}
