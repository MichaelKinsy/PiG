package ai

import (
	"math"
	"testing"
	"time"
)

// Node's setTimeout coerces its delay in lib/internal/timers.js Timeout: `after *= 1; if (!(after >= 1 && after <= TIMEOUT_MAX)) after = 1`, with TIMEOUT_MAX 2^31-1. PollOAuthDeviceCodeFlow hands Sleep the raw float delays of device-code.ts (NaN from Math.min with a NaN interval, Infinity, fractions), so every branch is reachable.
func TestNodeTimerDurationMatchesNodeCoercion(t *testing.T) {
	for _, tc := range []struct {
		ms   float64
		want time.Duration
	}{
		{math.NaN(), time.Millisecond},
		{math.Inf(1), time.Millisecond},
		{math.Inf(-1), time.Millisecond},
		{-5, time.Millisecond},
		{0, time.Millisecond},
		{0.999, time.Millisecond},
		{1, time.Millisecond},
		{1.9, time.Millisecond},
		{1500.7, 1500 * time.Millisecond},
		{2147483647, 2147483647 * time.Millisecond},
		{2147483647.5, time.Millisecond},
		{2147483648, time.Millisecond},
		{1e300, time.Millisecond},
	} {
		if got := nodeTimerDuration(tc.ms); got != tc.want {
			t.Errorf("nodeTimerDuration(%v) = %v, want %v", tc.ms, got, tc.want)
		}
	}
}
