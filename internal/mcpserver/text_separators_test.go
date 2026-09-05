package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestTaskSummary_SanitizesAllLineSeparators(t *testing.T) {
	for _, separator := range []string{"\n", "\r", "\t", "\v", "\f", "\u0085", "\u2028", "\u2029"} {
		t.Run(fmt.Sprintf("%q", separator), func(t *testing.T) {
			value := "first" + separator + "second"
			task := taskservice.PublicTask{
				ID: "20260904T120000-12345678", Name: value,
				Command: []string{"echo", value}, Labels: []string{value},
			}
			summary := toTaskSummary(task)
			if summary.Name != "first second" || summary.CommandPreview != "echo first second" ||
				len(summary.Labels) != 1 || summary.Labels[0] != "first second" {
				t.Fatalf("separator %q survived summary projection: %+v", separator, summary)
			}
			text := renderTaskList(ListOutput{Tasks: []TaskSummary{summary}, Returned: 1, Total: 1})
			if strings.Contains(text, "\u2028") || strings.Contains(text, "\u2029") || strings.Count(text, "\n") != 2 {
				t.Fatalf("separator %q broke table layout: %q", separator, text)
			}
			if task.Name != value || task.Command[1] != value || task.Labels[0] != value {
				t.Fatal("summary projection mutated the full task data")
			}
		})
	}
}

func TestRenderTaskInfo_QuotesExitSignal(t *testing.T) {
	text := renderTaskInfo(TaskInfo{
		Status: StatusInfo{State: "exited", Exited: &ExitedInfo{Signal: "TERM\nInjected\u2028section\u2029"}},
	})
	if !strings.Contains(text, `Signal: "TERM\nInjected\u2028section\u2029"`) {
		t.Fatalf("signal was not safely quoted: %q", text)
	}
}
