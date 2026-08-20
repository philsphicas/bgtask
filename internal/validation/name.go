package validation

import (
	"fmt"
	"regexp"
	"strings"
)

// generatedIDPattern matches the task ID format produced by state.GenerateID
// (YYYYMMDDTHHMMSS-XXXXXXXX). Names are rejected if they collide with this
// shape so a user-supplied name can never be confused with a canonical task
// ID when resolving by name-or-ID.
var generatedIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}-[0-9a-fA-F]{8}$`)

// ValidateName checks that a task name is safe to store and resolve by.
// It rejects:
//   - empty or whitespace-only names
//   - names containing a path separator ('/' or '\')
//   - "." or ".." (reserved directory names)
//   - names containing control characters
//   - names matching the generated task ID pattern, so a name can never be
//     mistaken for an ID (or vice versa) when resolving.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name cannot be empty or whitespace")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name %q is reserved", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("name %q cannot contain a path separator", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("name %q cannot contain control characters", name)
		}
	}
	if generatedIDPattern.MatchString(name) {
		return fmt.Errorf("name %q looks like a generated task ID and is not allowed", name)
	}
	return nil
}
