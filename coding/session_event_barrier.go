package coding

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
)

// sessionEventBarrier is an internal FIFO marker, not an agent or wire event.
// The embedded interface keeps it on the same channel without adding a
// serialized event kind to the upstream protocol.
type sessionEventBarrier struct {
	agent.AgentEvent
	done chan struct{}
	once sync.Once
}

// FlushEvents waits until an opted-in Events consumer acknowledges all preceding
// events. The consumer calls AcknowledgeEvent before event conversion; it must
// not call FlushEvents from its own event callback. Cancellation and Close
// release the wait even when no consumer is running. After Dispose the Session
// has no listeners, so there is nothing to wait for. After DiscardEvents the
// barrier holds once every preceding event executed.
func (s *Session) FlushEvents(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.discardEvents.Load() && s.execMu.TryLock() {
		// Nothing queued ahead and nothing executing: every preceding event executed.
		idle := s.inlineReadyLocked()
		s.execMu.Unlock()
		if idle {
			return nil
		}
	}
	marker := &sessionEventBarrier{done: make(chan struct{})}
	if !s.enqueueEvent(marker, ctx.Done()) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.eventsEndedErr()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.eventsDone:
		return s.eventsEndedErr()
	case <-marker.done:
		return nil
	}
}

// eventsEndedErr is FlushEvents' result once the event executor has ended:
// context.Canceled after Close, nil after Dispose.
func (s *Session) eventsEndedErr() error {
	select {
	case <-s.closeDone:
		return context.Canceled
	default:
		return nil
	}
}

// AcknowledgeEvent handles the internal marker used by FlushEvents. An Events
// consumer calls this after writing earlier events and does not serialize a
// marker when this returns true.
func AcknowledgeEvent(event agent.AgentEvent) bool {
	marker, ok := event.(*sessionEventBarrier)
	if !ok {
		return false
	}
	marker.AcknowledgeEvent()
	return true
}

// AcknowledgeEvent releases the FlushEvents call waiting on this marker. A
// consumer that cannot import this package (the interactive owner loop)
// acknowledges through this method.
func (b *sessionEventBarrier) AcknowledgeEvent() {
	b.once.Do(func() { close(b.done) })
}
