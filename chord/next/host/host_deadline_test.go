package host

import (
	"testing"
	"time"
)

// Ports packages/chord/test/next/host/deadline.test.ts: an activation's deadline (slice 6a §9) expires once after its
// budget; paused time does not count; a cleared deadline never expires.

func TestDeadlineExpiresOnceAfterItsBudget(t *testing.T) {
	loop := newFakeLoop()
	expired := 0
	NewDeadline(loop, 100*time.Millisecond, func() { expired++ })
	loop.Advance(99)
	if expired != 0 {
		t.Fatal("expired before its budget")
	}
	loop.Advance(1_000)
	if expired != 1 {
		t.Fatalf("expired %d times, want 1", expired)
	}
}

func TestDeadlinePausedTimeDoesNotCountResumedWithTheBudgetItHadLeft(t *testing.T) {
	loop := newFakeLoop()
	expired := 0
	deadline := NewDeadline(loop, 100*time.Millisecond, func() { expired++ })
	loop.Advance(60)
	deadline.Pause()
	loop.Advance(1_000)
	deadline.Resume()
	loop.Advance(39)
	if expired != 0 {
		t.Fatal("expired with budget left")
	}
	loop.Advance(1)
	if expired != 1 {
		t.Fatalf("expired %d times, want 1", expired)
	}
}

func TestDeadlineAClearedDeadlineNeverExpiresAlsoWhenResumed(t *testing.T) {
	loop := newFakeLoop()
	expired := 0
	deadline := NewDeadline(loop, 100*time.Millisecond, func() { expired++ })
	deadline.Pause()
	deadline.Clear()
	deadline.Resume()
	loop.Advance(1_000)
	if expired != 0 {
		t.Fatalf("expired %d times, want 0", expired)
	}
}
