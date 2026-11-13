package subprocess

import (
	"runtime"
	"sync/atomic"
	"testing"
)

// pig additive (D614): the push worker ends when nothing is pending, so a change can arrive while it is ending. That
// change is never lost: once the bridge is idle again, the last push read the last flag and nothing is left pending.
// Each round makes two changes, the second at a different distance from the first, so it meets the worker pushing,
// ending and ended.
func TestStatePushWorkerLosesNoChangeWhileEnding(t *testing.T) {
	b := NewUIBridge(func() {})
	var pushed atomic.Bool
	var pushes atomic.Int64
	b.OnStateChanged = func() {
		pushed.Store(b.Snapshot(nil, 0, false).Frontend)
		pushes.Add(1)
	}
	w := &b.frontendPush
	idle := func() (pending bool) {
		for {
			w.mu.Lock()
			started, pending := w.started, w.pending
			w.mu.Unlock()
			if !started {
				return pending
			}
			runtime.Gosched()
		}
	}
	const rounds = 20000
	for round := range rounds {
		b.SetFrontend(true)
		for range round % 48 {
			runtime.Gosched()
		}
		b.SetFrontend(false)
		if idle() {
			t.Fatalf("round %d: the worker ended and left a change pending", round)
		}
		if pushed.Load() {
			t.Fatalf("round %d: the last push read frontend true after it was set false", round)
		}
	}
	if n := pushes.Load(); n < rounds || n > 2*rounds {
		t.Fatalf("%d rounds of two changes pushed the state %d times, want %d to %d", rounds, n, rounds, 2*rounds)
	}
	b.StopFrontendPushes()
}
