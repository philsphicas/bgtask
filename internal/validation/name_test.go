package validation

import "testing"

func TestValidateName(t *testing.T) {
	valid := []string{
		"a",
		"myserver",
		"my-server",
		"my_server",
		"my.server",
		"api-1",
		"npm-a1b2",
		"20240101-not-quite-an-id",
	}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}

	invalid := []struct {
		name string
		desc string
	}{
		{"", "empty"},
		{"   ", "whitespace only"},
		{"\t\n", "whitespace only (tabs/newlines)"},
		{".", "dot"},
		{"..", "dot-dot"},
		{"a/b", "contains slash"},
		{"a\\b", "contains backslash"},
		{"../../etc/passwd", "path traversal"},
		{"bad\x00name", "contains NUL"},
		{"bad\x1bname", "contains ESC"},
		{"bad\x7fname", "contains DEL"},
		{"20240101T120000-abcdef12", "matches generated ID pattern"},
	}
	for _, tc := range invalid {
		if err := ValidateName(tc.name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error (%s)", tc.name, tc.desc)
		}
	}
}
