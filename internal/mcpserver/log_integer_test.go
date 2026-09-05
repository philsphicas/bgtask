package mcpserver

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestLogArgumentsRequireExactIntegers(t *testing.T) {
	for _, number := range []string{"2.0000000000000000001", "1.9999999999999999999", "1e-1000000", "1e1000000", "9223372036854775808"} {
		t.Run(number, func(t *testing.T) {
			raw := json.RawMessage(`{"ref":"task","max_bytes":` + number + `}`)
			if _, err := decodeLogsInput(raw); err == nil {
				t.Fatalf("accepted fractional or out-of-range integer %s", number)
			}
			if got := logMaxBytesFromArguments(raw); got != defaultLogMaxBytes {
				t.Fatalf("invalid budget %s selected %d instead of the default", number, got)
			}
		})
	}
	for _, number := range []string{"2", "2.0", "2e0", "20e-1", "0.002E+3", "2.0000000000000000000"} {
		t.Run(number, func(t *testing.T) {
			raw := json.RawMessage(`{"ref":"task","max_bytes":` + number + `}`)
			input, err := decodeLogsInput(raw)
			if err != nil || input.MaxBytes == nil || *input.MaxBytes != 2 {
				t.Fatalf("valid budget %s: %+v, %v", number, input, err)
			}
			if got := logMaxBytesFromArguments(raw); got != 2 {
				t.Fatalf("valid budget %s selected %d", number, got)
			}
		})
	}
	maxInt := int(^uint(0) >> 1)
	for _, want := range []int{maxInt, -maxInt - 1, 0} {
		got, err := exactJSONInteger(json.Number(strconv.Itoa(want)))
		if err != nil || got != want {
			t.Fatalf("integer boundary %d: %d, %v", want, got, err)
		}
	}
	if got, err := exactJSONInteger("0e100000000000000000000"); err != nil || got != 0 {
		t.Fatalf("zero with large exponent: %d, %v", got, err)
	}
}
