package mcpserver

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
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

func TestTaskSummary_BoundsCompactNameAndLabels(t *testing.T) {
	labels := make([]string, maxSummaryLabels+5)
	for i := range labels {
		labels[i] = strings.Repeat("x", 63)
	}
	summary := toTaskSummary(taskservice.PublicTask{
		ID:     "canonical-id",
		Name:   strings.Repeat("n", taskNamePreviewWidth+20),
		Labels: labels,
	})

	if !summary.NameTruncated || runewidth.StringWidth(summary.Name) > taskNamePreviewWidth {
		t.Fatalf("name = %q, truncated=%v", summary.Name, summary.NameTruncated)
	}
	if !summary.LabelsTruncated || len(summary.Labels) != maxSummaryLabels {
		t.Fatalf("labels = %d, truncated=%v", len(summary.Labels), summary.LabelsTruncated)
	}
	if summary.ID != "canonical-id" {
		t.Fatalf("ID = %q, want canonical-id", summary.ID)
	}
}
