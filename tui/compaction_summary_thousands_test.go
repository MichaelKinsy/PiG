package tui

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// compaction-summary-message.ts:40 tokensBefore.toLocaleString() against pinned Pi: separators every three digits and a minus sign in front, for every size from 0 to the int64 range.
func TestFormatThousandsMatchesPi(t *testing.T) {
	values := []int{0, 1, -1, 12, -12, 123, -123, 999, -999, 1000, -1000, 1234, -1234, 12345, -12345, 123456, -123456, 1234567, -1234567, 87432, 1000000, -1000000, math.MaxInt32, math.MinInt32, 1 << 40, -(1 << 40), 9007199254740991, -9007199254740991}
	var want []string
	pioracle.Run(t, `emit(input.map((n) => n.toLocaleString("en-US")));`, values, &want)
	for i, n := range values {
		if got := formatThousands(n); got != want[i] {
			t.Errorf("formatThousands(%d) = %q, Pi %q", n, got, want[i])
		}
	}
}
