package hsync

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSize parses an optional whole-number byte size using binary B, KB, MB,
// GB, or TB units. For example, 5MB is 5*1024*1024 bytes.
func ParseSize(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	units := []struct {
		suffix string
		factor int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}}
	for _, unit := range units {
		if !strings.HasSuffix(value, unit.suffix) {
			continue
		}
		number := strings.TrimSuffix(value, unit.suffix)
		if number == "" {
			break
		}
		parsed, err := strconv.ParseInt(number, 10, 64)
		if err != nil || parsed < 0 || parsed > (1<<63-1)/unit.factor {
			return 0, fmt.Errorf("invalid file size %q", value)
		}
		return parsed * unit.factor, nil
	}
	return 0, fmt.Errorf("invalid file size %q", value)
}
