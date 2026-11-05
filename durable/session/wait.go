package session

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
)

// The Session's waits.
//
// A wait that only the Session's own goroutines can end is a wait of the Session: the code that goes on to it has entered. Pi's
// listener runs up to its first await, and what that await waits for does not stop the publication of the next part of a call; a Go
// listener is a blocking call, so the delivery says it entered (chord.DeliveryEntered) before it blocks, and a Continuation it
// carries (WithContinuation) has entered too. Every blocking point a listener can reach, in the Session and in the Harness that
// reads through it, goes through enter: block, await and Wait for a channel, Park for the wait of a Parked.

// enter says the calling code is about to block in a wait of the Session: a watch delivery it runs has run the synchronous part of
// its listener, and the continuation it belongs to, if any, has entered. It is safe on a nil Continuation and off a delivery.
func enter(continuation *Continuation) {
	continuation.Entered()
	chord.DeliveryEntered()
}

// block waits until done is closed as a wait of the Session: it returns at once when done is closed already, and otherwise enters
// first. A ctx that ends the wait before done is closed returns its cause; nil never does (the Session's own waits: a gate always
// opens, a line job always settles).
func block(continuation *Continuation, done <-chan struct{}, ctx context.Context) error {
	select {
	case <-done:
		return nil
	default:
	}
	enter(continuation)
	if ctx == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// Both may be ready: a channel that closed before the ctx ended is the settled value, as an awaited promise that resolved.
		select {
		case <-done:
			return nil
		default:
			return context.Cause(ctx)
		}
	}
}

// Enter is enter for the Harness's own waits that are not on one channel (a select of several): the delivery the calling goroutine
// runs, and the continuation ctx carries, have entered. Call it once the wait is known to block.
func Enter(ctx context.Context) { enter(continuationOf(ctx)) }

// await blocks until done is closed, for the Session's own goroutines' waits that nothing can cancel.
func await(done <-chan struct{}) { hold(nil, done) }

// hold is await for a wait of a Continuation: the continuation, and the delivery, enter before it blocks.
func hold(continuation *Continuation, done <-chan struct{}) { _ = block(continuation, done, nil) }

// Wait blocks until done is closed, or ctx ends: its cause is returned. It is the wait of the Harness on what the Session
// publishes (a gate, a drain): the delivery or continuation that calls it has entered, so the publication it waits for is not held
// by the hop that waits for it.
func Wait(ctx context.Context, done <-chan struct{}) error {
	return block(continuationOf(ctx), done, ctx)
}
