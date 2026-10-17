package agent

import (
	"testing"
	"time"
)

// lazy.ts:50-51 and event-stream.ts #time: a request-setup error is stamped with the first clock read and timed from the
// stream's start, the second read; a millisecond that ticks between the two reads leaves it untimed.
func TestSetupErrorDurationFollowsPisTwoClockReads(t *testing.T) {
	base := time.UnixMilli(1_700_000_000_000)
	sameMillisecond := base.Add(900 * time.Microsecond)
	nextMillisecond := base.Add(1100 * time.Microsecond)

	if got := setupErrorDuration(base.Add(100*time.Microsecond), nextMillisecond, nextMillisecond.Add(7*time.Millisecond)); got != nil {
		t.Fatalf("a tick between the reads: durationMs = %d, want absent", *got)
	}
	got := setupErrorDuration(base.Add(100*time.Microsecond), sameMillisecond, sameMillisecond.Add(7400*time.Microsecond))
	if got == nil || *got != 7 {
		t.Fatalf("both reads in one millisecond: durationMs = %v, want 7 (from the stream's start, rounded)", got)
	}
	if got := setupErrorDuration(base, base, base.Add(2500*time.Microsecond)); got == nil || *got != 3 {
		t.Fatalf("durationMs = %v, want 3 (Math.round of 2.5)", got)
	}
}
