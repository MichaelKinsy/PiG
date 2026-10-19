package host

// pi: packages/chord/src/publish.ts
// publish.ts has no test file of its own: its contract is its header comment (one FIFO queue; a re-entrant publication
// does not overtake the one being delivered; holdPublications delivers nothing until fn returns, then everything in
// order) and the observable case of state.test.ts:222 "re-entrant writes publish in adoption order", which
// cell_upstream_test.go runs through a cell. The contract holds for the shared queue (no loop) and for a loop's queue.

import (
	"slices"
	"testing"
)

// eachPublicationQueue runs test with the shared queue and with the queue of a loop.
func eachPublicationQueue(t *testing.T, test func(t *testing.T, queue *loop)) {
	t.Helper()
	t.Run("shared", func(t *testing.T) { test(t, nil) })
	t.Run("loop", func(t *testing.T) { test(t, &loop{}) })
}

// publish.ts:19-22 enqueue: a task enqueued while another delivers runs after the running one and after everything
// enqueued before it (Pi's example: subscriber A re-enters, subscriber B still sees 1 before 2).
func TestEnqueueRunsAReentrantTaskAfterTheOneBeingDelivered(t *testing.T) {
	eachPublicationQueue(t, func(t *testing.T, queue *loop) {
		var seen []string
		enqueue(queue, func() {
			enqueue(queue, func() { seen = append(seen, "reentrant") })
			seen = append(seen, "first")
		})
		enqueue(queue, func() { seen = append(seen, "second") })
		if want := []string{"first", "reentrant", "second"}; !slices.Equal(seen, want) {
			t.Fatalf("delivery order %v, want %v", seen, want)
		}
	})
}

// publish.ts:24-31 holdPublications: nothing is delivered until fn returns, then everything enqueued, in order; it
// returns fn's value; holds nest.
func TestHoldPublicationsDeliversAfterFnReturnsInOrderAndNests(t *testing.T) {
	eachPublicationQueue(t, func(t *testing.T, queue *loop) {
		var seen []string
		got := holdPublications(queue, func() string {
			enqueue(queue, func() { seen = append(seen, "a") })
			holdPublications(queue, func() struct{} {
				enqueue(queue, func() { seen = append(seen, "b") })
				return struct{}{}
			})
			if len(seen) != 0 {
				t.Fatalf("delivered %v while held", seen)
			}
			enqueue(queue, func() { seen = append(seen, "c") })
			return "value"
		})
		if got != "value" {
			t.Fatalf("holdPublications returned %q", got)
		}
		if want := []string{"a", "b", "c"}; !slices.Equal(seen, want) {
			t.Fatalf("delivery order %v, want %v", seen, want)
		}
	})
}
