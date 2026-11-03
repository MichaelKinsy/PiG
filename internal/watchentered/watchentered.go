// Package watchentered tells a watch delivery that its listener went on to a wait the host knows.
//
// Pi runs a listener's synchronous part, up to its first await, before the code that awaited a commit resumes. A Go listener
// is a blocking call: the delivery scheduler (chord.DeliveryScheduler.AwaitEntered) orders what runs before its first wait by
// having the waits of the host say so. The delivery registers its goroutine here; a wait that the host provides (the Session's
// Park, a gate or line wait, the host loop's park) calls Entered, which marks the delivery on the calling goroutine entered.
// The mapping is by goroutine ID, so a listener passes nothing, and reads no goroutine state beyond the caller's own ID.
package watchentered

import (
	"sync"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Delivery is what a registered delivery gives: it marks itself entered.
type Delivery interface{ MarkEntered() }

var running sync.Map // uint64 goroutine ID -> Delivery

// Register records that the calling goroutine runs delivery; the returned function ends it.
func Register(delivery Delivery) (done func()) {
	id := goroutineid.Current()
	running.Store(id, delivery)
	return func() { running.Delete(id) }
}

// Current is the delivery the calling goroutine runs, if any.
func Current() (Delivery, bool) {
	found, ok := running.Load(goroutineid.Current())
	if !ok {
		return nil, false
	}
	return found.(Delivery), true
}

// Entered marks the delivery the calling goroutine runs, if any, entered: it is about to wait for something the host knows.
func Entered() {
	if delivery, ok := Current(); ok {
		delivery.MarkEntered()
	}
}
