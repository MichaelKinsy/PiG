package tui

import (
	"sync"
	"sync/atomic"
	"testing"
)

// Work runs in the order it was queued, one at a time, whichever goroutine queued it.
func TestQueuedCallsRunInOrderOneAtATime(t *testing.T) {
	for round := 0; round < 200; round++ {
		var queue callQueue
		const calls = 64
		var busy atomic.Bool
		var ran []int
		var wg sync.WaitGroup
		for i := 0; i < calls; i++ {
			wg.Add(1)
			queue.push(func() {
				defer wg.Done()
				if !busy.CompareAndSwap(false, true) {
					t.Errorf("round %d: two calls at once", round)
				}
				ran = append(ran, i)
				busy.Store(false)
			})
		}
		wg.Wait()
		for at, i := range ran {
			if at != i {
				t.Fatalf("round %d: calls ran in the order %v", round, ran)
			}
		}
	}
}

// The goroutine ends with the work and the next push starts another.
func TestTheQueueRestartsAfterItDrains(t *testing.T) {
	var queue callQueue
	for i := 0; i < 3; i++ {
		done := make(chan struct{})
		queue.push(func() { close(done) })
		<-done
	}
}
