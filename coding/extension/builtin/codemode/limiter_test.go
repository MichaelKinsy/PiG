package codemode

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// execute.ts createLimiter: at most MAX_CONCURRENT_MODEL_CALLS calls run at once. A caller that arrives while a finishing call wakes its waiter must queue, not take the freed slot as well.
func TestLimiterNeverExceedsItsLimit(t *testing.T) {
	l := &limiter{limit: maxConcurrentModelCalls}
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for range 5000 {
		wg.Go(func() {
			l.run(func() {
				running := active.Add(1)
				for {
					seen := peak.Load()
					if running <= seen || peak.CompareAndSwap(seen, running) {
						break
					}
				}
				runtime.Gosched()
				active.Add(-1)
			})
		})
	}
	wg.Wait()
	if got := peak.Load(); got > maxConcurrentModelCalls {
		t.Fatalf("%d calls ran at once, limit %d", got, maxConcurrentModelCalls)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != 0 || len(l.waiting) != 0 {
		t.Fatalf("idle limiter holds active %d, waiting %d", l.active, len(l.waiting))
	}
}
