package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/session"
)

func TestWait(t *testing.T) {
	cancelled, cancel := context.WithCancelCause(ctx)
	cause := errors.New("the caller gave up")
	cancel(cause)
	t.Run("returns nil for a closed channel even when the context ended", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		if err := session.Wait(cancelled, done); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("returns the cause of a context that ended before the channel closed", func(t *testing.T) {
		if err := session.Wait(cancelled, make(chan struct{})); !errors.Is(err, cause) {
			t.Fatalf("Wait = %v, want %v", err, cause)
		}
	})
}

// An awaited promise that resolved yields its value however the awaiting code's signal ended since. Wait never returns the cause
// of a ctx that ended after done closed: the channel closes before the ctx is cancelled in every round, so an error is the wrong pick
// of a select whose cases were both ready.
func TestWaitReturnsNilWhenDoneClosedBeforeTheContextEnded(t *testing.T) {
	for round := range 20000 {
		cancelled, cancel := context.WithCancelCause(ctx)
		done := make(chan struct{})
		go func() {
			close(done)
			cancel(errors.New("the caller gave up"))
		}()
		if err := session.Wait(cancelled, done); err != nil {
			select {
			case <-done:
				t.Fatalf("round %d: Wait = %v, want nil: done was closed before the ctx ended", round, err)
			default:
			}
		}
	}
}
