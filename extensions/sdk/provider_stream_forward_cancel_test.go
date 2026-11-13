package sdk

import (
	"context"
	"runtime"
	"testing"
)

// The forwarder reads the request's context, takes the delivery lock and then reads the queue. A cancel, and a push by an author that saw it, can both land after that read of
// the context and before the read of the queue. The test holds the queue's lock and waits until the forwarder holds the delivery lock: the forwarder is then past its read of
// the context and has not read the queue. The event that enters the queue after the cancel is not sent.
func TestProviderStreamEventPushedAfterTheCancelIsNotForwarded(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := CreateAssistantMessageEventStream()
	stream.mu.Lock()
	sent, stopped := 0, make(chan error, 1)
	go func() {
		stopped <- stream.forward(requestContext, func(any) error {
			sent++
			return nil
		})
	}()
	for stream.delivery.TryLock() {
		stream.delivery.Unlock()
		runtime.Gosched()
	}
	cancel()
	// What Push does for an author that saw the cancel; appended here because the test holds the queue's lock.
	stream.queue = append(stream.queue, modelStreamItem{event: map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x"}})
	stream.mu.Unlock()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Errorf("the forwarder sent %d events, want none: the one in the queue was pushed after the cancel", sent)
	}
}
