package coding

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// sessionEventBarrier is an internal FIFO marker, not an agent or wire event.
// The embedded interface keeps it on the same channel without adding a
// serialized event kind to the upstream protocol.
type sessionEventBarrier struct {
	agent.AgentEvent
	done chan struct{}
	once sync.Once
	// region is eventRegionNone for a FlushEvents barrier; a marker that bounds an event region (sessionEventRegions) says whether it opens or closes one, and has no done channel.
	region eventRegionState
}

// eventRegionState says whether a sessionEventBarrier is a FlushEvents barrier or bounds an event region.
type eventRegionState int8

const (
	eventRegionNone eventRegionState = iota
	eventRegionOpen
	eventRegionClosed
)

// EventRegion reports whether b bounds an event region, and if so whether it opens one. The owner loop of the interactive mode reads it through an interface it declares itself.
func (b *sessionEventBarrier) EventRegion() (open, bounds bool) {
	return b.region == eventRegionOpen, b.region != eventRegionNone
}

// sessionEventRegions puts the bounds of the tasks of Pi's event loop that a run executes (jsloop.Task) on Events, in order with the run's events, for a consumer that must not act between two events of one task. The interactive mode is that consumer: its frame is a timer callback in Pi (packages/tui/src/tui.ts requestRender), which cannot run inside a task, so it applies the events of a region together when the closing marker arrives.
//
// Only a run started through RunAgentPrompt with a context that asks for its tasks (jsloop.WithTasks) reports them: every other run publishes exactly the events it published before. Listeners and extensions are not involved: they receive each event when it happens.
type sessionEventRegions struct {
	task *jsloop.Task
	// opened and closed are the two markers, reused: a marker of a region carries no state of its own.
	opened, closed *sessionEventBarrier
}

func (r *sessionEventRegions) init(s *Session) {
	r.opened = &sessionEventBarrier{region: eventRegionOpen}
	r.closed = &sessionEventBarrier{region: eventRegionClosed}
	r.task = jsloop.NewTask(s.publishEventRegion)
}

// runContext returns ctx carrying the Session's Task when ctx asks for the run's tasks.
func (r *sessionEventRegions) runContext(ctx context.Context) context.Context {
	if !jsloop.TasksWanted(ctx) {
		return ctx
	}
	return jsloop.WithTask(ctx, r.task)
}

// endEventTask ends the task of Pi's loop that the run executes, if any, before the Session awaits an extension handler: Pi's loop turns while a handler awaits, so what precedes the handler is drawn while it runs.
func (s *Session) endEventTask() { s.eventRegions.task.End() }

// publishEventRegion puts the marker that opens or closes an event region on Events, behind every event already published and ahead of the next. As emitPublishedEvent does, the caller sends it itself when nothing is queued ahead and Events has room, and queues it otherwise: it may hold the stream turn, so it never waits for the consumer here. Nothing is sent while no consumer called Events.
func (s *Session) publishEventRegion(open bool) {
	if !s.eventsSubscribed.Load() {
		return
	}
	marker := s.eventRegions.closed
	if open {
		marker = s.eventRegions.opened
	}
	if s.execMu.TryLock() {
		if s.inlineReadyLocked() && cap(s.events) > len(s.events) {
			s.sendEvent(marker)
			s.execMu.Unlock()
			return
		}
		s.execMu.Unlock()
	}
	s.enqueueEvent(marker, nil)
}

// FlushEvents waits until an opted-in Events consumer acknowledges all preceding
// events. The consumer calls AcknowledgeEvent before event conversion; it must
// not call FlushEvents from its own event callback. Cancellation and Close
// release the wait even when no consumer is running. After Dispose the Session
// has no listeners, so there is nothing to wait for. Before the first
// Events call and after DiscardEvents the barrier holds once every preceding
// event executed.
func (s *Session) FlushEvents(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.eventsSubscribed.Load() && s.execMu.TryLock() {
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
	if b.done == nil {
		// A marker that bounds an event region has no waiter.
		return
	}
	b.once.Do(func() { close(b.done) })
}
