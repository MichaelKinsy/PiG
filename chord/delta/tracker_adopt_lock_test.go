package delta

import (
	"sync"
	"testing"
	"time"
)

// Adopt must not hold the winning change's lock while it locks the other open changes: a change that is placing one
// of the winner's handles holds its own lock while it waits for the winner's, so the two would wait on each other.
func TestAdoptDoesNotDeadlockWithAPlacementOfTheWinnersHandle(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2000 {
			tracker := Track(JsonObjectOf("a", JsonObjectOf("x", 1.0)))
			winner := tracker.BeginChange()
			winnerHandle := winner.State().Object("a")
			other := tracker.BeginChange().State()
			if err := winner.State().Set("y", 2.0); err != nil {
				panic(err)
			}
			prepared, err := winner.Prepare()
			if err != nil {
				panic(err)
			}
			var wg sync.WaitGroup
			wg.Go(func() {
				defer func() { _ = recover() }() // the winner's handle is revoked once it prepared
				_ = other.Set("k", winnerHandle)
			})
			wg.Go(func() {
				if err := tracker.Adopt(prepared); err != nil {
					panic(err)
				}
			})
			wg.Wait()
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Adopt and a placement of the winner's handle deadlocked")
	}
}
