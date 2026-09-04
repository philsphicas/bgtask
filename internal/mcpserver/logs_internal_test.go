package mcpserver

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8Bytes_TinyLimitsRemainValidUTF8(t *testing.T) {
	for _, limit := range []int{1, 2} {
		got, truncated := truncateUTF8Bytes("éé", limit)
		if !truncated || !utf8.ValidString(got) || len(got) > limit {
			t.Fatalf("limit %d: got %q (%d bytes), truncated=%v, valid=%v", limit, got, len(got), truncated, utf8.ValidString(got))
		}
	}
}
