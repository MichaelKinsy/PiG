package coding

import (
	"context"
	"sync"
	"sync/atomic"

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
// A task that publishes one message_update and nothing else has no bounds to keep: the event goes on Events as it is, with no marker. That is the task of a provider that delivers one chunk at a time, and two markers for each chunk would wake the consumer three times where it woke once. To know that the update stays alone, the task keeps it back until it publishes another event, which sends the opening marker and the update ahead of that event, or ends, which sends the update alone. A task that begins with any other event is marked from that event on, so only a message_update ever waits, and only for the rest of the turn of the loop that published it.
//
// Only a run started through RunAgentPrompt with a context that asks for its tasks (jsloop.WithTasks) reports them: every other run publishes exactly the events it published before. Listeners and extensions are not involved: they receive each event when it happens.
type sessionEventRegions struct {
	task *jsloop.Task
	// opened and closed are the two markers, reused: a marker of a region carries no state of its own.
	opened, closed *sessionEventBarrier
	// pending is set from the beginning of a task until everything its end puts on Events is there. While it is clear an event of the run is published without a look at the rest.
	pending atomic.Bool
	// mu orders what a task puts on Events: its events, from the run (publishAgentEvent), and what its end sends, from whichever goroutine ends it (publishEventRegion).
	mu sync.Mutex
	// running is what the Task last reported.
	running bool
	// held is the message_update a running task published first and keeps back; marked says the task's opening marker is on Events.
	held   agent.AgentEvent
	marked bool
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

// publishAgentEvent publishes an event of the Agent on Events (emitPublishedEvent), in its place among the bounds of the task that runs, if one does (sessionEventRegions).
//
// The message of a message_end or turn_end event is the object the transcript and the saved entry hold, which a later handler (turn_end's, agent_end's) may replace in place while a consumer of Events, reading on its own goroutine, still holds the event. Pi's listeners run at the event, so a listener sees the message as it is then: the event carries its EventView, taken here on the goroutine that publishes, before anything can replace the message. Nothing is copied while no consumer called Events.
func (s *Session) publishAgentEvent(ev agent.AgentEvent) {
	if s.eventsSubscribed.Load() {
		switch event := ev.(type) {
		case agent.MessageEndEvent:
			event.Message = event.Message.EventView()
			ev = event
		case agent.TurnEndEvent:
			event.Message = event.Message.EventView()
			ev = event
		}
	}
	r := &s.eventRegions
	if !r.pending.Load() {
		s.emitPublishedEvent(ev)
		return
	}
	r.mu.Lock()
	if r.running && !r.marked {
		if _, update := ev.(agent.MessageUpdateEvent); update && r.held == nil {
			r.held = ev
			r.mu.Unlock()
			return
		}
		r.marked = true
		s.sendEventRegionMarker(r.opened)
		if held := r.held; held != nil {
			r.held = nil
			s.emitPublishedEvent(held)
		}
	}
	s.emitPublishedEvent(ev)
	r.mu.Unlock()
}

// publishEventRegion is the Task's observer. The beginning of a task sends nothing: its first event decides (publishAgentEvent). The end sends the message_update the task kept back, alone, or the closing marker of a task that was marked.
func (s *Session) publishEventRegion(running bool) {
	r := &s.eventRegions
	r.mu.Lock()
	r.running = running
	if running {
		r.pending.Store(true)
		r.mu.Unlock()
		return
	}
	if held := r.held; held != nil {
		r.held = nil
		s.emitPublishedEvent(held)
	} else if r.marked {
		r.marked = false
		s.sendEventRegionMarker(r.closed)
	}
	r.pending.Store(false)
	r.mu.Unlock()
}

// sendEventRegionMarker puts a marker on Events, behind every event already published and ahead of the next. As emitPublishedEvent does, the caller sends it itself when nothing is queued ahead and Events has room, and queues it otherwise: it may hold the stream turn, so it never waits for the consumer here. Nothing is sent while no consumer called Events.
func (s *Session) sendEventRegionMarker(marker *sessionEventBarrier) {
	if !s.eventsSubscribed.Load() {
		return
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
