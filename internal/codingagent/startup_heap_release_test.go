package codingagent

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// countHeapReturns replaces the heap return with a counter for the test.
func countHeapReturns(t *testing.T) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	previous := returnHeapToOS
	returnHeapToOS = func() { calls.Add(1) }
	t.Cleanup(func() { returnHeapToOS = previous })
	return &calls
}

// The start's heap return waits idleCollectAfter, like the idle collection (D172): a session that is prompted at once keeps the free heap
// for its run, and one left alone returns it once.
func TestStartupHeapReleaseWaitsForAnIdleStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := countHeapReturns(t)
		m := &InteractiveMode{}
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.releaseStartupHeapWhenIdle(t.Context())
		}()
		time.Sleep(idleCollectAfter - time.Millisecond)
		synctest.Wait()
		if got := calls.Load(); got != 0 {
			t.Fatalf("the start returned its heap %d times before idleCollectAfter, want 0", got)
		}
		<-done
		if got := calls.Load(); got != 1 {
			t.Fatalf("an idle start returned its heap %d times, want 1", got)
		}
	})
}

// A run that began before the wait ended leaves the heap return to its own idle collection, and a mode that stopped returns nothing.
func TestStartupHeapReleaseYieldsToARunAndToAStop(t *testing.T) {
	for name, prepare := range map[string]func(*InteractiveMode, context.CancelFunc){
		"a run is active":            func(m *InteractiveMode, _ context.CancelFunc) { m.turnActive.Store(true) },
		"a run armed the collection": func(m *InteractiveMode, _ context.CancelFunc) { m.idleCollectArmed.Store(true) },
		"the idle collection ran":    func(m *InteractiveMode, _ context.CancelFunc) { m.idleCollector.collects.Add(1) },
		"the mode stopped":           func(_ *InteractiveMode, stop context.CancelFunc) { stop() },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := countHeapReturns(t)
				m := &InteractiveMode{}
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				prepare(m, stop)
				m.releaseStartupHeapWhenIdle(ctx)
				if got := calls.Load(); got != 0 {
					t.Fatalf("the start returned its heap %d times, want 0", got)
				}
			})
		})
	}
}
