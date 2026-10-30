package host

import (
	"slices"
	"testing"
	"time"
)

// Ports packages/chord/test/next/host/backoff.test.ts.

// backoffWindow is the length of the window `failed` opens for identity, measured until `due` runs (the test's `window`).
func backoffWindow(loop *fakeLoop, backoff *backoff, dues *[]string, identity string) int64 {
	calls := len(*dues)
	start := loop.Now()
	backoff.failed(identity)
	for len(*dues) == calls {
		loop.Advance(100)
	}
	return loop.Now() - start
}

func newTestBackoff(loop *fakeLoop) (*backoff, *[]string) {
	dues := &[]string{}
	return newBackoff(loop, func(identity string) { *dues = append(*dues, identity) }), dues
}

func untilOf(backoff *backoff, identity string) (int64, bool) { return backoff.until(identity) }

func TestBackoffOpensAWindowOf1sAfterTheFirstFailureDoublingUpTo60sAndCallsDueAtItsEnd(t *testing.T) {
	loop := newFakeLoop()
	backoff, dues := newTestBackoff(loop)
	var windows []int64
	for range 9 {
		windows = append(windows, backoffWindow(loop, backoff, dues, "a"))
	}
	if want := []int64{1000, 2000, 4000, 8000, 16000, 32000, 60000, 60000, 60000}; !slices.Equal(windows, want) {
		t.Fatalf("windows = %v, want %v", windows, want)
	}
	for _, identity := range *dues {
		if identity != "a" {
			t.Fatalf("due called with %q", identity)
		}
	}
}

func TestBackoffTellsWhenARunningWindowEndsAFailedReferencesRetryAt(t *testing.T) {
	loop := newFakeLoop()
	backoff := newBackoff(loop, func(string) {})
	loop.Advance(5_000)
	backoff.failed("a")
	a, aOK := untilOf(backoff, "a")
	_, bOK := untilOf(backoff, "b")
	if a != 6_000 || !aOK || bOK {
		t.Fatalf("until = %d %v / %v, want 6000 true / false", a, aOK, bOK)
	}
	loop.Advance(1_000)
	if _, ok := untilOf(backoff, "a"); ok {
		t.Fatal("until of a passed window is defined")
	}
	backoff.failed("a")
	if a, ok := untilOf(backoff, "a"); a != 8_000 || !ok {
		t.Fatalf("until = %d %v, want 8000", a, ok)
	}
	backoff.skip("a")
	if _, ok := untilOf(backoff, "a"); ok {
		t.Fatal("until after skip is defined")
	}
}

func TestBackoffIsWaitingOnlyInsideAWindow(t *testing.T) {
	loop := newFakeLoop()
	backoff := newBackoff(loop, func(string) {})
	backoff.failed("a")
	if !backoff.waiting("a") || backoff.waiting("b") {
		t.Fatalf("waiting = %v %v, want true false", backoff.waiting("a"), backoff.waiting("b"))
	}
	loop.Advance(1000)
	if backoff.waiting("a") {
		t.Fatal("waiting after the window")
	}
}

func TestBackoffSkipEndsAWindowWithoutDueAndKeepsItsDelayDeleteStartsOver(t *testing.T) {
	loop := newFakeLoop()
	backoff, dues := newTestBackoff(loop)
	backoff.failed("a")
	backoff.skip("a")
	loop.Advance(5000)
	if backoff.waiting("a") || len(*dues) != 0 {
		t.Fatalf("waiting %v, dues %d, want false 0", backoff.waiting("a"), len(*dues))
	}
	if got := backoffWindow(loop, backoff, dues, "a"); got != 2000 {
		t.Fatalf("window after skip = %d, want 2000", got)
	}
	backoff.delete("a")
	if got := backoffWindow(loop, backoff, dues, "a"); got != 1000 {
		t.Fatalf("window after delete = %d, want 1000", got)
	}
}

func TestBackoffEndedKeepsARunningWindowAndDropsAPassedOne(t *testing.T) {
	loop := newFakeLoop()
	backoff, dues := newTestBackoff(loop)
	backoff.failed("a")
	backoff.ended("a")
	if !backoff.waiting("a") {
		t.Fatal("ended dropped a running window")
	}
	loop.Advance(1000)
	backoff.ended("a")
	if got := backoffWindow(loop, backoff, dues, "a"); got != 1000 {
		t.Fatalf("window after ended of a passed one = %d, want 1000", got)
	}
}

func TestBackoffAFailureInsideARunningWindowReplacesItOneDueAfterTheDoubledDelay(t *testing.T) {
	loop := newFakeLoop()
	backoff, dues := newTestBackoff(loop)
	backoff.failed("a")
	backoff.failed("a")
	loop.Advance(1999)
	if len(*dues) != 0 || loop.TimerCount() != 1 {
		t.Fatalf("dues %d timers %d, want 0 1", len(*dues), loop.TimerCount())
	}
	loop.Advance(1)
	if len(*dues) != 1 {
		t.Fatalf("dues %d, want 1", len(*dues))
	}
}

func TestBackoffClearForgetsEveryIdentityWithItsTimer(t *testing.T) {
	loop := newFakeLoop()
	backoff := newBackoff(loop, func(string) {})
	backoff.failed("a")
	backoff.failed("b")
	backoff.clear()
	if backoff.waiting("a") || backoff.waiting("b") || loop.TimerCount() != 0 {
		t.Fatalf("waiting %v %v timers %d, want false false 0", backoff.waiting("a"), backoff.waiting("b"), loop.TimerCount())
	}
}

func TestNextDelay(t *testing.T) {
	delay := time.Duration(0)
	var got []time.Duration
	for range 9 {
		delay = nextDelay(delay)
		got = append(got, delay)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second}
	if !slices.Equal(got, want) {
		t.Fatalf("nextDelay chain = %v, want %v", got, want)
	}
}
