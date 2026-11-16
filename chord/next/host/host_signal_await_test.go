package host

import (
	"context"
	"runtime"
	"testing"
	"time"
	"weak"
)

// Go-only (no Pi case): Pi's `await gate.promise` has no timer, so nothing of the call stays behind once it settles. A Go test's
// wait is bounded by a timer (hangGuard), and a stopped time.Timer stays in the runtime's timer heap, with its callback, until
// that heap drops it: from the head of the heap, or once the stopped timers are more than a quarter of it, and only when the
// P that owns the heap schedules (runtime/time.go, stop and adjust). A bound made with context.WithTimeout over the handler's
// context kept that context, and with it what the call carries (the attempt of a reload cycle's old code), reachable for as
// long as the runtime left the timer there: TestReloadCycleAnOpenersGateWaitLeavesNoWaitEdgeOnceItEndedNorKeepsTheOldAttempt
// failed in about one run of a thousand with `referent 0 was not collected` (a heap dump of a failing run: timer heap, stopped
// timer, context.WithDeadlineCause's callback, the timer context, the handler's context, the attempt). The host's own timers
// avoid it (loopTimer, dropTimer drop the callback at Stop); the test's wait does the same.
//
// Which waits leave a stopped timer behind is the runtime's: of 1000 waits, one after the other, the old wait left the value
// reachable after ten collections 12 to 32 times in each of 20 runs with -race and 13 to 25 in each of 20 plain runs; in a
// stand-alone program of 40 waits on 2 to 16 Ps, 0 to 2 times. So the test makes awaitTrials of them and fails when any is held:
// the old wait passes it with a chance below one in a million, and the wait that keeps nothing never fails it, as no callback
// holds the context.

// awaitCarried is what the call's context carries, as the attempt of an object does.
type awaitCarried struct{ _ [64]byte }

type awaitCarriedKey struct{}

// awaitCarrying waits, as a handler does, with a context that carries a value, for a signal that fired already, and returns a
// weak pointer to the value: nothing of the call is left on this goroutine's stack.
func awaitCarrying(t *testing.T) weak.Pointer[awaitCarried] {
	carried := &awaitCarried{}
	ctx := context.WithValue(background, awaitCarriedKey{}, carried)
	fired := newSignal()
	fired.fire()
	fired.await(t, ctx)
	return weak.Make(carried)
}

// awaitTrials is how many waits the test makes; see above.
const awaitTrials = 1000

// awaitCollected is whether the value ref points to is collected within ten collections, as nexttest.ExpectCollected waits.
func awaitCollected(ref weak.Pointer[awaitCarried]) bool {
	for range 10 {
		runtime.GC()
		if ref.Value() == nil {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// TestSignalAwaitKeepsNothingOfTheCallContextInTheTimerHeapOnceItReturned: after a wait returned, the value its context carried
// is collected. Several Ps, so that a stopped timer can stay in the heap of one that does not schedule.
func TestSignalAwaitKeepsNothingOfTheCallContextInTheTimerHeapOnceItReturned(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	for range 8 {
		defer time.AfterFunc(hangGuard/2, func() {}).Stop()
	}
	held := 0
	for range awaitTrials {
		if !awaitCollected(awaitCarrying(t)) {
			held++
		}
	}
	if held > 0 {
		t.Errorf("%d of %d waits left the value their context carried reachable once they returned", held, awaitTrials)
	}
}
