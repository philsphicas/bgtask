package validation

import "testing"

func TestValidateLabel(t *testing.T) {
	valid := []string{
		"a",
		"dev",
		"Dev",
		"production",
		"my-app",
		"my_app",
		"my.app",
		"project:myapp",
		"env:dev",
		"a1",
		"v2",
		"team:backend-v2",
		"a.b.c",
		"A",
		"MyLabel",
	}
	for _, l := range valid {
		if err := ValidateLabel(l); err != nil {
			t.Errorf("ValidateLabel(%q) = %v, want nil", l, err)
		}
	}

	invalid := []struct {
		label string
		desc  string
	}{
		{"", "empty"},
		{"1abc", "starts with digit"},
		{"123", "all digits"},
		{"-abc", "starts with hyphen"},
		{"abc-", "ends with hyphen"},
		{"abc.", "ends with dot"},
		{"abc:", "ends with colon"},
		{"has space", "contains space"},
		{"has,comma", "contains comma"},
		{"has=equals", "contains equals"},
		{"has/slash", "contains slash"},
		{"has!bang", "contains bang"},
		{"has@at", "contains at"},
		{string(make([]byte, 64)), "too long (64 chars)"},
	}
	for _, tc := range invalid {
		if err := ValidateLabel(tc.label); err == nil {
			t.Errorf("ValidateLabel(%q) = nil, want error (%s)", tc.label, tc.desc)
		}
	}
}

func TestValidateLabels(t *testing.T) {
	// Empty slice is valid (used for clearing labels).
	if err := ValidateLabels(nil); err != nil {
		t.Errorf("ValidateLabels(nil) = %v, want nil", err)
	}
	if err := ValidateLabels([]string{}); err != nil {
		t.Errorf("ValidateLabels([]) = %v, want nil", err)
	}

	// Valid labels.
	if err := ValidateLabels([]string{"dev", "project:myapp"}); err != nil {
		t.Errorf("ValidateLabels valid = %v, want nil", err)
	}

	// One invalid label fails the batch.
	if err := ValidateLabels([]string{"dev", "123"}); err == nil {
		t.Errorf("ValidateLabels with invalid = nil, want error")
	}
}
