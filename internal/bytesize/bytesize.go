// Package bytesize parses the byte sizes PiG commands accept, such as 5GiB.
package bytesize

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse reads a non-negative byte count with an optional unit: B, KB, MB, GB
// (powers of 1000) or KiB, MiB, GiB (powers of 1024), in any letter case. A bare
// number is bytes.
func Parse(value string) (int64, error) {
	trimmed := strings.TrimSpace(strings.ToUpper(value))
	multipliers := []struct {
		suffix string
		value  int64
	}{{"GIB", 1 << 30}, {"GB", 1_000_000_000}, {"MIB", 1 << 20}, {"MB", 1_000_000}, {"KIB", 1 << 10}, {"KB", 1_000}, {"B", 1}}
	multiplier := int64(1)
	for _, candidate := range multipliers {
		if before, ok := strings.CutSuffix(trimmed, candidate.suffix); ok {
			trimmed = strings.TrimSpace(before)
			multiplier = candidate.value
			break
		}
	}
	number, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || number < 0 || multiplier > 0 && number > (1<<63-1)/multiplier {
		return 0, fmt.Errorf("invalid byte size %q", value)
	}
	return number * multiplier, nil
}
