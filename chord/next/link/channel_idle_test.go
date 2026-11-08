package link_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Not upstream's (Chord v1's DeliveryAwaiter): Idle is the channel's half of the wait a host that takes a link offers for
// it, since Pi's hosts share one event loop and a link to another process has none.

func idleWithin(t *testing.T, channel *link.Channel, wait time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, wait)
	defer cancel()
	return channel.Idle(ctx)
}

func TestIdleReturnsAtOnceWhenNothingWaitsBehindAnAsynchronousHandling(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		h := newHolding(t, codec)
		if err := h.setup.channel.Idle(background); err != nil {
			t.Fatalf("Idle = %v, want nil", err)
		}
		h.setup.peer.send(&link.ReleaseMessage{Hold: 1})
		h.settle()
		if err := h.setup.channel.Idle(background); err != nil {
			t.Fatalf("Idle after a message handled at once = %v, want nil", err)
		}
	})
}

func TestIdleWaitsForTheHeldMessageAndForTheOnesBehindIt(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		h := newHolding(t, codec)
		h.setup.peer.send(&link.ReleaseMessage{Hold: 0})
		h.setup.peer.send(&link.ReleaseMessage{Hold: 1})
		h.settle()
		stop := errors.New("stopped waiting")
		ctx, cancel := context.WithCancelCause(background)
		cancel(stop)
		if err := h.setup.channel.Idle(ctx); !errors.Is(err, stop) {
			t.Fatalf("Idle with a message held and ctx ended = %v, want its cause", err)
		}
		idle := make(chan error, 1)
		go func() { idle <- h.setup.channel.Idle(background) }()
		select {
		case err := <-idle:
			t.Fatalf("Idle returned %v while a message is held", err)
		case <-time.After(50 * time.Millisecond):
		}
		h.finish(0).Resolve()
		select {
		case err := <-idle:
			if err != nil {
				t.Fatalf("Idle = %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("Idle did not return after the held message was handled")
		}
		if want := []string{"release 0", "release 1"}; len(h.setup.handler.names()) != len(want) {
			t.Fatalf("handled = %v, want %v by the time Idle returned", h.setup.handler.names(), want)
		}
	})
}

// A message that passed the closed check, was held after the link closed and then failed is never released: Idle returns
// because the channel is closed (its doc), not when ctx ends.
func TestIdleReturnsWhenTheChannelClosedWithAMessageHeld(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		h := newHolding(t, codec)
		channel := h.setup.channel
		h.setup.handler.message = func(link.Message) (*link.Handling, error) {
			channel.Close()
			handling := link.NewHandling()
			handling.Reject(errors.New("inflate failed"))
			return handling, nil
		}
		h.setup.peer.send(&link.ReleaseMessage{Hold: 0})
		h.settle()
		if !channel.Closed() {
			t.Fatal("the channel did not close")
		}
		if err := idleWithin(t, channel, 5*time.Second); err != nil {
			t.Fatalf("Idle on a closed channel = %v, want nil", err)
		}
	})
}
