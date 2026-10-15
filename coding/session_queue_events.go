package coding

import (
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
)

// A callback running on the event executor, or on a producer that executes it inline while the executor waits for execMu, cannot wait for the executor to consume its publications. Overflow retains a reentrant publication burst beyond the raw-event mailbox, and every event published after it, until the executor drains it once the events sent before it executed.
type queueEventPublication struct {
	mu        sync.Mutex
	notifying bool
	overflow  []agent.AgentEvent
	// pending is set while overflow holds events, so producers and the executor read it without mu. It changes under mu.
	pending atomic.Bool
}

//pig:hotpath
func (s *Session) notifyOnEventExecutor(event agent.AgentEvent) {
	s.queueEvents.mu.Lock()
	s.queueEvents.notifying = true
	s.queueEvents.mu.Unlock()
	defer func() {
		s.queueEvents.mu.Lock()
		s.queueEvents.notifying = false
		s.queueEvents.mu.Unlock()
	}()
	s.notifyAgentEventListeners(event)
}

func (s *Session) publishQueueEvent(event agent.QueueUpdateEvent) {
	published := publishedSessionEvent{event}
	s.queueEvents.mu.Lock()
	select {
	case <-s.eventsDone:
		s.queueEvents.mu.Unlock()
		return
	default:
	}
	if len(s.queueEvents.overflow) > 0 {
		s.queueEvents.overflow = append(s.queueEvents.overflow, published)
		s.queueEvents.mu.Unlock()
		return
	}
	if s.queueEvents.notifying {
		s.queued.Add(1)
		select {
		// upstream: packages/ai/src/utils/event-stream.ts:push
		case s.rawEvents <- published:
		default:
			s.queued.Add(-1)
			// The sole receiver is executing the callback, or waits for the producer that executes it. Retain the full value after the events already sent.
			s.queueEvents.overflow = append(s.queueEvents.overflow, published)
			s.queueEvents.pending.Store(true)
		}
		s.queueEvents.mu.Unlock()
		return
	}
	s.queueEvents.mu.Unlock()
	s.enqueueEvent(published, nil)
}

// appendQueueOverflow appends ev to the overflow and reports whether one was pending.
func (s *Session) appendQueueOverflow(ev agent.AgentEvent) bool {
	s.queueEvents.mu.Lock()
	defer s.queueEvents.mu.Unlock()
	if len(s.queueEvents.overflow) == 0 {
		return false
	}
	s.queueEvents.overflow = append(s.queueEvents.overflow, ev)
	return true
}

// nextQueueOverflowEvent removes the first event of the pending overflow. Only the executor removes overflow events.
func (s *Session) nextQueueOverflowEvent() agent.AgentEvent {
	s.queueEvents.mu.Lock()
	defer s.queueEvents.mu.Unlock()
	event := s.queueEvents.overflow[0]
	s.queueEvents.overflow[0] = nil
	s.queueEvents.overflow = s.queueEvents.overflow[1:]
	if len(s.queueEvents.overflow) == 0 {
		s.queueEvents.overflow = nil
		s.queueEvents.pending.Store(false)
	}
	return event
}
