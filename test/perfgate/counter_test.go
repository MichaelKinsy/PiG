// SPDX-License-Identifier: MIT

package perfgate

import "testing"

// counterDelta returns later-earlier for two cumulative runtime counters of separate runs as a signed value: a later run that allocated less gives a negative difference instead of wrapping to ~1.8e19.
func counterDelta(later, earlier uint64) float64 {
	return float64(int64(later - earlier))
}

func TestCounterDeltaIsSigned(t *testing.T) {
	for _, c := range []struct {
		later, earlier uint64
		want           float64
	}{
		{later: 1500, earlier: 1000, want: 500},
		{later: 1000, earlier: 1000, want: 0},
		{later: 1000, earlier: 1500, want: -500},
		{later: 0, earlier: 1 << 40, want: -(1 << 40)},
	} {
		if got := counterDelta(c.later, c.earlier); got != c.want {
			t.Errorf("counterDelta(%d, %d) = %v, want %v", c.later, c.earlier, got, c.want)
		}
	}
}
