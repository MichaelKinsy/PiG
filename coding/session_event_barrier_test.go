package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func TestSessionFlushEventsWaitsForConsumerAcknowledgement(t *testing.T) {
	s, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	s.Steer("queued", nil)
	done := make(chan error, 1)
	go func() { done <- s.FlushEvents(t.Context()) }()
	first := <-s.Events()
	if update, ok := first.(agent.QueueUpdateEvent); !ok || len(update.Steering) != 1 || update.Steering[0] != "queued" {
		t.Fatalf("first event %#v", first)
	}
	marker := <-s.Events()
	select {
	case err := <-done:
		t.Fatalf("flush returned before writer acknowledged: %v", err)
	default:
	}
	for range 2 {
		if !AcknowledgeEvent(marker) {
			t.Fatal("marker not acknowledged idempotently")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if AcknowledgeEvent(first) {
		t.Fatal("queue event was swallowed")
	}
}

func TestSessionFlushEventsReleasesWithoutConsumer(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		s, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- s.FlushEvents(ctx) }()
		if closeSession {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			cancel()
		}
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("flush error %v", err)
		}
		cancel()
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
