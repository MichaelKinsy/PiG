package env

import (
	"errors"
	"testing"
)

// TestLineScannerSafeIntegerBounds ports line-scan.ts:36-40: startLine must be a non-negative safe integer and endLine, when given, a safe
// integer above startLine. The largest safe integer is accepted and the next integer is not, for both ends.
func TestLineScannerSafeIntegerBounds(t *testing.T) {
	tooBig := int64(maxSafeInteger + 1)
	largest := int64(maxSafeInteger)
	for _, tc := range []struct {
		name  string
		start int64
		end   *int64
		ok    bool
	}{
		{"start at the largest safe integer", maxSafeInteger, nil, true},
		{"start above the largest safe integer", maxSafeInteger + 1, nil, false},
		{"end at the largest safe integer", 0, &largest, true},
		{"end above the largest safe integer", 0, &tooBig, false},
		{"end one above start", 0, new(int64(1)), true},
		{"end equal to start", 3, new(int64(3)), false},
		{"negative start", -1, nil, false},
	} {
		scanner, err := NewLineScanner(tc.start, tc.end)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrInvalidLineRange)) || (err == nil && scanner == nil) {
			t.Errorf("%s: NewLineScanner(%d, %v) = %v, %v; want ok=%v", tc.name, tc.start, tc.end, scanner, err, tc.ok)
		}
	}
}
