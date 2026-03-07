package validation

import (
	"fmt"
	"regexp"
)

// labelPattern enforces the label format:
// - Must start with a letter
// - Can contain letters, digits, hyphens, underscores, dots, and colons
// - Must end with a letter or digit (if length > 1)
// - Max length 63 characters
var labelPattern = regexp.MustCompile(`^[a-zA-Z]([a-zA-Z0-9._:-]*[a-zA-Z0-9])?$`)

// ValidateLabel checks if a label string is valid.
func ValidateLabel(label string) error {
	if len(label) == 0 {
		return fmt.Errorf("label cannot be empty")
	}
	if len(label) > 63 {
		return fmt.Errorf("label %q is too long (max 63 characters)", label)
	}
	if !labelPattern.MatchString(label) {
		return fmt.Errorf("invalid label %q: must start with a letter, end with alphanumeric, allowed: [a-zA-Z0-9._:-]", label)
	}
	return nil
}

// ValidateLabels validates a slice of labels.
func ValidateLabels(labels []string) error {
	for _, l := range labels {
		if err := ValidateLabel(l); err != nil {
			return err
		}
	}
	return nil
}
