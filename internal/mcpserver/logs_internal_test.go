package mcpserver

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/philsphicas/bgtask/internal/supervisor"
)

func TestTruncateUTF8Bytes_TinyLimitsRemainValidUTF8(t *testing.T) {
	for _, limit := range []int{1, 2} {
		got, truncated := truncateUTF8Bytes("éé", limit)
		if !truncated || !utf8.ValidString(got) || len(got) > limit {
			t.Fatalf("limit %d: got %q (%d bytes), truncated=%v, valid=%v", limit, got, len(got), truncated, utf8.ValidString(got))
		}
	}
}

func TestRenderLogs_UsesExactSeparatorBudget(t *testing.T) {
	entries := []supervisor.LogEntry{
		{Time: time.Unix(0, 0).UTC(), Stream: "o", Data: "first"},
		{Time: time.Unix(1, 0).UTC(), Stream: "o", Data: "second"},
	}
	full, _, _, _ := renderLogs(entries, 1024)

	got, returned, omitted, _ := renderLogs(entries, len(full))
	if got != full || returned != 2 || omitted != 0 {
		t.Fatalf("exact-fit render = %q, returned=%d, omitted=%d; want both entries", got, returned, omitted)
	}
}

func TestLogFallbackMessageCanBeByteBounded(t *testing.T) {
	message := "2 matching log entries exceeded max_bytes; increase max_bytes to read them."
	for _, limit := range []int{1, 2, 16} {
		got, _ := truncateUTF8Bytes(message, limit)
		if len(got) > limit || !utf8.ValidString(got) {
			t.Fatalf("limit %d: fallback %q is %d bytes or invalid UTF-8", limit, got, len(got))
		}
	}
}
