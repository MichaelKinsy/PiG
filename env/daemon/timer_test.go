// SPDX-License-Identifier: MIT

package daemon

import (
	"runtime"
	"testing"
	"time"
)

// A wait loop that re-arms one timer allocates nothing per pass; time.After allocated a timer and a channel per pass.
func TestWaitTimerRearmAllocatesNothing(t *testing.T) {
	var reused waitTimer
	defer reused.stop()
	<-reused.arm(time.Nanosecond)
	got := testing.AllocsPerRun(200, func() { <-reused.arm(time.Nanosecond) })
	if got != 0 {
		t.Fatalf("a re-armed waitTimer allocates %.1f per pass, want 0", got)
	}
	perAfter := testing.AllocsPerRun(200, func() { <-time.After(time.Nanosecond) })
	t.Logf("allocations per pass: re-armed waitTimer %.0f, time.After %.0f", got, perAfter)
	if perAfter < 1 {
		t.Fatalf("the per-pass time.After this replaces allocates %.1f, want at least 1: the comparison is void", perAfter)
	}
}

// Re-arming after an early stop never delivers the earlier arming's tick: the next wait lasts its own duration.
func TestWaitTimerRearmDropsTheEarlierTick(t *testing.T) {
	var timer waitTimer
	defer timer.stop()
	first := timer.arm(time.Millisecond)
	for start := time.Now(); time.Since(start) < 20*time.Millisecond; { // the first arming expires and its tick stays unread
		runtime.Gosched()
	}
	second := timer.arm(time.Hour)
	if first != second {
		t.Fatal("the channel changed between armings")
	}
	select {
	case <-second:
		t.Fatal("an unread tick from the earlier arming was delivered after the timer was re-armed")
	case <-time.After(30 * time.Millisecond):
	}
	timer.stop()
	var never waitTimer
	never.stop()
}
