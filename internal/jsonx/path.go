package jsonx

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidatePath checks v1 object-property paths. Numeric-only components are
// reserved for future array addressing and rejected for now.
func ValidatePath(path []string) error {
	for i, part := range path {
		if part == "" {
			return fmt.Errorf("path component %d is empty", i)
		}
		if _, err := strconv.ParseUint(part, 10, 64); err == nil {
			return fmt.Errorf("path component %q looks like an array index", part)
		}
	}
	return nil
}

func PathString(path []string) string {
	if len(path) == 0 {
		return "/"
	}
	return "/" + strings.Join(path, "/")
}

func ClonePath(path []string) []string {
	if path == nil {
		return nil
	}
	out := make([]string, len(path))
	copy(out, path)
	return out
}
