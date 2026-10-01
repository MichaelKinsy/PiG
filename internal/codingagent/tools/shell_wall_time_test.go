package tools

import (
	"testing"
	"time"
)

// bash.ts:392: Math.round(ms / 100) / 10 rounds halves up, like JavaScript's Math.round.
func TestRoundedWallTimeSeconds(t *testing.T) {
	for _, tc := range []struct {
		elapsed time.Duration
		want    float64
	}{
		{0, 0}, {49 * time.Millisecond, 0}, {50 * time.Millisecond, 0.1}, {149 * time.Millisecond, 0.1},
		{150 * time.Millisecond, 0.2}, {1234 * time.Millisecond, 1.2}, {2950 * time.Millisecond, 3},
	} {
		if got := roundedWallTimeSeconds(tc.elapsed); got != tc.want {
			t.Errorf("roundedWallTimeSeconds(%v) = %v, want %v", tc.elapsed, got, tc.want)
		}
	}
}
