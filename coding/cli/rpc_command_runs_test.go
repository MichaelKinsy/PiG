package cli

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi queues the runs a command's handler sends on its one event loop in send order (agent-session.ts:2293 sendCustomMessage), so
// the starts rpcCommandRuns holds proceed in the order they were held, also when the transport's report releases them.
func TestRPCCommandRunsReleaseHeldStartsInOrder(t *testing.T) {
	wait := testbudget.Wait(t)
	for range 20 {
		runs := &rpcCommandRuns{}
		flight := &rpcCommandFlight{runs: runs}
		flight.Resumed()
		const held = 64
		var (
			mu    sync.Mutex
			order []int
			done  = make(chan struct{})
		)
		for i := range held {
			runs.whenNoneRuns(func() {
				mu.Lock()
				order = append(order, i)
				if len(order) == held {
					close(done)
				}
				mu.Unlock()
			})
		}
		flight.Suspended()
		select {
		case <-done:
		case <-time.After(wait):
			t.Fatal("held starts did not all run")
		}
		mu.Lock()
		sorted := slices.IsSorted(order)
		got := slices.Clone(order)
		mu.Unlock()
		if !sorted {
			t.Fatalf("held starts ran in order %v, want the order they were held", got)
		}
	}
}
