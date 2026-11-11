//go:build !race

package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// One acknowledgement that releases n waiting slots sends each slot once: Pi's #onAck deletes the front of a Map, which costs nothing per slot. A map that copies its key list on every delete allocates bytes that grow with the square of the slots.
func TestScalingFlushWaitingSlots(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "flush of waiting slots", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		connection := waitingConnection(1 << 20)
		fillWaiting(connection, n)
		connection.unackedBytes = 0
		return func() {
			connection.flush()
			if connection.pending.Len() != 0 {
				panic("slots still waiting")
			}
		}
	}})
}

// Holds that end one after another while slots wait forget their slot in place, as Pi's dropHoldState walks its Map and deletes: the map allocates only the smaller storage it moves to as it drains (halving, so linear in all), never a snapshot of its keys per hold (which allocates bytes that grow with the square of the slots).
func TestScalingDropHoldStateOfWaitingSlots(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "dropHoldState of every waiting slot", Size: 500, Bound: testscaling.Linear, Prepare: func(n int) func() {
		connection := waitingConnection(1 << 20)
		fillWaiting(connection, n)
		return func() {
			for hold := range n {
				connection.dropHoldState(hold)
			}
			if connection.pending.Len() != 0 {
				panic("slots still waiting")
			}
		}
	}})
}
