package coding

import (
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
)

// A callback running on the event executor cannot wait for that executor to consume its publications. Overflow retains a reentrant publication burst and subsequent queue updates until the executor drains them, after the already-enqueued prefix and before later Agent events.
type queueEventPublication struct {
	mu        sync.Mutex
	notifying bool
	prefix    int
	overflow  []agent.AgentEvent
}

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
	case <-s.closeDone:
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
		select {
		// upstream: packages/ai/src/utils/event-stream.ts:push
		case s.rawEvents <- published:
		default:
			// The sole receiver is executing the callback. Retain the full value and drain the already-enqueued prefix before this overflow.
			s.queueEvents.prefix = len(s.rawEvents)
			s.queueEvents.overflow = append(s.queueEvents.overflow, published)
		}
		s.queueEvents.mu.Unlock()
		return
	}
	s.queueEvents.mu.Unlock()
	select {
	case <-s.closeDone:
	case s.rawEvents <- published:
	}
}

func (s *Session) nextQueueOverflowEvent() agent.AgentEvent {
	s.queueEvents.mu.Lock()
	defer s.queueEvents.mu.Unlock()
	if s.queueEvents.prefix > 0 || len(s.queueEvents.overflow) == 0 {
		return nil
	}
	event := s.queueEvents.overflow[0]
	s.queueEvents.overflow[0] = nil
	s.queueEvents.overflow = s.queueEvents.overflow[1:]
	if len(s.queueEvents.overflow) == 0 {
		s.queueEvents.overflow = nil
	}
	return event
}
