package host

import (
	"math"
	"testing"
)

// A held change's deadline is Date.now() + withinMs, a float in Pi: `deadline > now` stays true for a fractional
// withinMs until now reaches the next whole millisecond, so the integer deadline rounds up.
func TestAddMillisRoundsAFractionalWithinUp(t *testing.T) {
	cases := []struct {
		now    int64
		within float64
		want   int64
	}{
		{10, 0, 10},
		{10, 0.5, 11},
		{10, 2, 12},
		{10, 2.25, 13},
		{10, math.Inf(1), noDeadline},
		{10, math.NaN(), noDeadline},
		{10, 1e300, noDeadline},
	}
	for _, c := range cases {
		if got := addMillis(c.now, c.within); got != c.want {
			t.Errorf("addMillis(%d, %v) = %d, want %d", c.now, c.within, got, c.want)
		}
	}
}
