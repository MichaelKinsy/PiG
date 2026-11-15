package codingagent

import "time"

// TickStatus runs one status tick on the owner loop with the given clock reading.
func (h *TestHarness) TickStatus(now time.Time) {
	h.Do(func() { h.m.tickStatusIndicators(now) })
}

// StatusClockBase is the clock reading the live status indicator was shown at, which dates the compaction line's elapsed time.
func (h *TestHarness) StatusClockBase() (base time.Time) {
	h.Do(func() { base = h.m.statusLastFrame })
	return base
}
