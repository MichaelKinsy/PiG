package ai

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"
)

type eventDelivery struct {
	event AssistantMessageEvent
	done  bool
}

type eventWaiter struct {
	ready chan eventDelivery
}

// AssistantMessageEventStream is an ordered in-process event stream. Result
// completes independently of event iteration; unread events remain available
// until consumed. Subprocess transports impose their own bounded backpressure.
type AssistantMessageEventStream struct {
	mu      sync.Mutex
	queue   []AssistantMessageEvent
	waiters []*eventWaiter
	done    chan struct{}

	started  bool
	terminal bool
	result   *AssistantMessage
}

// NewAssistantMessageEventStream creates an open stream.
func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	return &AssistantMessageEventStream{done: make(chan struct{})}
}

// Push appends an event. Pushes after termination are ignored, matching Pi's
// EventStream. Invalid pre-terminal event sequences return an invariant error.
func (s *AssistantMessageEventStream) Push(event AssistantMessageEvent) error {
	if event == nil {
		return errors.New("assistant message stream event is nil")
	}
	event = snapshotAssistantEvent(event)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return nil
	}
	if err := s.validateLocked(event); err != nil {
		return err
	}

	terminal := false
	switch event := event.(type) {
	case DoneEvent:
		s.terminal = true
		s.result = event.Message
		terminal = true
	case ErrorEvent:
		s.terminal = true
		s.result = event.Error
		terminal = true
	}

	if len(s.waiters) == 0 {
		s.queue = append(s.queue, event)
	} else {
		waiter := s.waiters[0]
		s.waiters[0] = nil
		s.waiters = s.waiters[1:]
		waiter.ready <- eventDelivery{event: event}
	}

	if terminal {
		close(s.done)
		for _, waiter := range s.waiters {
			waiter.ready <- eventDelivery{done: true}
		}
		s.waiters = nil
	}
	return nil
}

func (s *AssistantMessageEventStream) validateLocked(event AssistantMessageEvent) error {
	switch value := event.(type) {
	case StartEvent:
		if s.started {
			return errors.New("assistant message stream emitted start more than once")
		}
		if value.Partial == nil {
			return errors.New("assistant message stream start is missing partial")
		}
		s.started = true
	case DoneEvent:
		if !s.started {
			return errors.New("assistant message stream emitted done before start")
		}
		if value.Message == nil {
			return errors.New("assistant message stream done is missing message")
		}
		if value.Reason != StopReasonStop && value.Reason != StopReasonLength && value.Reason != StopReasonToolUse && value.Reason != StopReasonDeferred {
			return fmt.Errorf("assistant message stream done has invalid reason %q", value.Reason)
		}
	case ErrorEvent:
		if value.Error == nil {
			return errors.New("assistant message stream error is missing assistant message")
		}
		if value.Reason != StopReasonError && value.Reason != StopReasonAborted {
			return fmt.Errorf("assistant message stream error has invalid reason %q", value.Reason)
		}
	default:
		if !s.started {
			return fmt.Errorf("assistant message stream emitted %s before start", event.EventType())
		}
		if partial := eventPartial(event); partial == nil {
			return fmt.Errorf("assistant message stream %s is missing partial", event.EventType())
		}
	}
	return nil
}

// Events returns a single-pass sequence over the stream's shared FIFO. Multiple
// iterators divide events in waiter-registration order, matching Pi's queue.
// Canceling ctx removes an outstanding waiter without a delivery goroutine.
func (s *AssistantMessageEventStream) Events(ctx context.Context) iter.Seq[AssistantMessageEvent] {
	if ctx == nil {
		panic("assistant message event stream: nil iterator context")
	}
	return func(yield func(AssistantMessageEvent) bool) {
		for {
			event, ok := s.next(ctx)
			if !ok || !yield(event) {
				return
			}
		}
	}
}

func (s *AssistantMessageEventStream) next(ctx context.Context) (AssistantMessageEvent, bool) {
	s.mu.Lock()
	if len(s.queue) > 0 {
		event := s.queue[0]
		s.queue[0] = nil
		s.queue = s.queue[1:]
		if len(s.queue) == 0 {
			s.queue = nil
		}
		s.mu.Unlock()
		return event, true
	}
	if s.terminal {
		s.mu.Unlock()
		return nil, false
	}
	waiter := &eventWaiter{ready: make(chan eventDelivery, 1)}
	s.waiters = append(s.waiters, waiter)
	s.mu.Unlock()

	select {
	case delivery := <-waiter.ready:
		return delivery.event, !delivery.done
	case <-ctx.Done():
		delivery, assigned := s.cancelWaiter(waiter)
		if !assigned {
			return nil, false
		}
		return delivery.event, !delivery.done
	}
}

func (s *AssistantMessageEventStream) cancelWaiter(waiter *eventWaiter) (eventDelivery, bool) {
	s.mu.Lock()
	for index, registered := range s.waiters {
		if registered == waiter {
			s.waiters[index] = nil
			s.waiters = append(s.waiters[:index], s.waiters[index+1:]...)
			s.mu.Unlock()
			return eventDelivery{}, false
		}
	}
	s.mu.Unlock()

	// Assignment already won under the stream lock. It is the delivery point
	// in the shared FIFO and must not move behind a later event.
	return <-waiter.ready, true
}

// Result waits for termination and returns the exact pointer carried by the
// terminal DoneEvent or ErrorEvent.
func (s *AssistantMessageEventStream) Result() *AssistantMessage {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

func eventPartial(event AssistantMessageEvent) *AssistantMessage {
	switch value := event.(type) {
	case TextStartEvent:
		return value.Partial
	case TextDeltaEvent:
		return value.Partial
	case TextEndEvent:
		return value.Partial
	case ThinkingStartEvent:
		return value.Partial
	case ThinkingDeltaEvent:
		return value.Partial
	case ThinkingEndEvent:
		return value.Partial
	case ToolCallStartEvent:
		return value.Partial
	case ToolCallDeltaEvent:
		return value.Partial
	case ToolCallEndEvent:
		return value.Partial
	default:
		return nil
	}
}
