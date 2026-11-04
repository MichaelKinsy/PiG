package chordctx

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
)

// handlerCtx is the ctx of a method handler as the host builds it: a context that answers the call's parking signal.
type handlerCtx struct {
	context.Context
	signal *parking.Signal
}

func (c handlerCtx) Value(key any) any {
	if key == parking.Key {
		return c.signal
	}
	return c.Context.Value(key)
}

// Suspend is the handler's first suspension: it raises the signal its ctx (or a context derived from it) carries, and does
// nothing for a context that carries none.
func TestSuspendRaisesTheSignalOfTheHandlersCtx(t *testing.T) {
	Suspend(Background())
	signal := parking.New()
	ctx, cancel := context.WithCancel(handlerCtx{Context: Background(), signal: signal})
	defer cancel()
	derived := WithValue(ctx, NewKey[int]("a value"), 1)
	Suspend(derived)
	if !signal.Raised() {
		t.Fatal("Suspend of a context derived from the handler's did not raise its signal")
	}
}

// Await and Receive suspend the handler before they block, honor the abort of ctx and leave a nil ctx and an aborted one
// alone (an aborted ctx returns at once, as `awaitWithContext` rejects at once).
func TestAwaitAndReceiveSuspendTheHandlerBeforeTheyBlock(t *testing.T) {
	t.Run("Receive", func(t *testing.T) {
		signal := parking.New()
		ctx := handlerCtx{Context: Background(), signal: signal}
		work := make(chan int)
		got := make(chan int, 1)
		go func() {
			value, err := Receive(ctx, work)
			if err != nil {
				t.Error(err)
			}
			got <- value
		}()
		signal.Wait()
		work <- 7
		if value := <-got; value != 7 {
			t.Fatalf("Receive = %d, want 7", value)
		}
	})
	t.Run("Await", func(t *testing.T) {
		signal := parking.New()
		ctx := handlerCtx{Context: Background(), signal: signal}
		work := make(chan Settled[string])
		got := make(chan Settled[string], 1)
		go func() {
			value, err := Await(ctx, work)
			got <- Settled[string]{Value: value, Err: err}
		}()
		signal.Wait()
		boom := errors.New("boom")
		work <- Settled[string]{Err: boom}
		if settled := <-got; !errors.Is(settled.Err, boom) {
			t.Fatalf("Await error = %v, want boom", settled.Err)
		}
	})
	t.Run("an abort ends the wait", func(t *testing.T) {
		signal := parking.New()
		inner, cancel := WithCancel(handlerCtx{Context: Background(), signal: signal})
		got := make(chan error, 1)
		go func() {
			_, err := Receive(inner, make(chan int))
			got <- err
		}()
		signal.Wait()
		reason := errors.New("gone")
		cancel(reason)
		if err := <-got; !errors.Is(err, reason) {
			t.Fatalf("Receive error = %v, want the abort reason", err)
		}
	})
	t.Run("an aborted ctx returns before it suspends", func(t *testing.T) {
		signal := parking.New()
		inner, cancel := WithCancel(handlerCtx{Context: Background(), signal: signal})
		reason := errors.New("gone")
		cancel(reason)
		if _, err := Receive(inner, make(chan int)); !errors.Is(err, reason) {
			t.Fatalf("Receive error = %v, want the abort reason", err)
		}
		if signal.Raised() {
			t.Fatal("a wait that returned at once suspended the handler: its code after the wait would race the section")
		}
	})
	t.Run("a nil ctx waits for the work alone", func(t *testing.T) {
		work := make(chan int, 1)
		work <- 3
		var none context.Context
		if value, err := Receive(none, work); err != nil || value != 3 {
			t.Fatalf("Receive(nil) = %d, %v; want 3", value, err)
		}
	})
}
