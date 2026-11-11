// SPDX-License-Identifier: MIT

package sqlhost

import (
	"math"
	"testing"
	"time"
)

func TestTimerDelayClampsWhereAConversionWouldNot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		at, now float64
		want    time.Duration
	}{
		{"future", 1500, 1000, 500 * time.Millisecond},
		{"due", 1000, 1000, 0},
		{"past", 900, 1000, 0},
		{"NaN", math.NaN(), 1000, 0},
		{"infinite", math.Inf(1), 1000, time.Duration(math.MaxInt64)},
		{"beyond a Duration", 1e300, 1000, time.Duration(math.MaxInt64)},
	} {
		if got := timerDelay(tc.at, tc.now); got != tc.want {
			t.Errorf("%s: timerDelay(%v, %v) = %v, want %v", tc.name, tc.at, tc.now, got, tc.want)
		}
	}
}
