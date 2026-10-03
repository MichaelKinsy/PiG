package ai

// Ports packages/ai/src/utils/event-stream.ts

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
	ready   chan eventDelivery
	onReady func(eventDelivery) func()
}

func (waiter *eventWaiter) deliver(delivery eventDelivery) func() {
	waiter.ready <- delivery
	if waiter.onReady != nil {
		return waiter.onReady(delivery)
	}
	return nil
}

// AssistantMessageEventStream is an ordered in-process event stream. Result
// completes independently of event iteration; unread events remain available
// until consumed. Subprocess transports impose their own bounded backpressure.
type AssistantMessageEventStream struct {
	mu           sync.Mutex
	queue        []AssistantMessageEvent
	waiters      []*eventWaiter
	done         chan struct{}
	executor     *continuationExecutor
	publications assistantPublications
	// producer is the executor turn that owns pushes, if any. Only a producer under the executor publishes live partial views.
	producer *continuationTurn

	terminal      bool
	resolved      bool
	result        *AssistantMessage
	resultWaiters []*continuationPromise[*AssistantMessage]
}

// NewAssistantMessageEventStream creates an open stream.
func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	return &AssistantMessageEventStream{done: make(chan struct{})}
}

// Push appends an event. Pushes after termination are ignored, matching Pi's
// EventStream. Done and error may terminate without a start event. Missing data and invalid closed-union values return a payload error.
func (s *AssistantMessageEventStream) Push(event AssistantMessageEvent) error {
	return s.push(event, assistantMessageReplacements{})
}

// push is Push for a producer that assigned new nested objects to its partial since the last push. Earlier shallow copies keep the objects they retained.
func (s *AssistantMessageEventStream) push(event AssistantMessageEvent, replacements assistantMessageReplacements) error {
	if event == nil {
		return errors.New("assistant message stream event is nil")
	}
	s.mu.Lock()
	if executor := s.executor; executor != nil && executor.trace != nil && executor.trace.push != nil {
		trace := executor.trace
		s.mu.Unlock()
		trace.push(s, event)
		s.mu.Lock()
	}
	var continuations []func()
	defer func() {
		s.mu.Unlock()
		for _, continuation := range continuations {
			continuation()
		}
	}()
	if s.terminal {
		return nil
	}
	if err := s.validateLocked(event); err != nil {
		return err
	}
	event = mapAssistantEventPartial(event, func(message *AssistantMessage) *AssistantMessage {
		return s.publishPartialLocked(message, replacements)
	})

	terminal := false
	switch event := event.(type) {
	case DoneEvent:
		s.publishTerminalLocked(event.Message, replacements)
		s.terminal = true
		s.result = event.Message
		terminal = true
	case ErrorEvent:
		s.publishTerminalLocked(event.Error, replacements)
		s.terminal = true
		s.result = event.Error
		terminal = true
	}

	if terminal {
		continuations = append(continuations, s.resolveResultLocked()...)
	}
	if len(s.waiters) == 0 {
		s.queue = append(s.queue, event)
	} else {
		waiter := s.waiters[0]
		s.waiters[0] = nil
		s.waiters = s.waiters[1:]
		if continuation := waiter.deliver(eventDelivery{event: event}); continuation != nil {
			continuations = append(continuations, continuation)
		}
	}

	if terminal {
		for _, waiter := range s.waiters {
			if continuation := waiter.deliver(eventDelivery{done: true}); continuation != nil {
				continuations = append(continuations, continuation)
			}
		}
		s.waiters = nil
	}
	return nil
}

func (s *AssistantMessageEventStream) validateLocked(event AssistantMessageEvent) error {
	switch value := event.(type) {
	case StartEvent:
		if value.Partial == nil {
			return errors.New("assistant message stream start is missing partial")
		}
	case DoneEvent:
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
	return s.events(ctx, true)
}

// events is Events with optional delivery-time materialization. A forwarder passes false so a hop between streams neither copies nor observes the partial.
func (s *AssistantMessageEventStream) events(ctx context.Context, materialize bool) iter.Seq[AssistantMessageEvent] {
	if ctx == nil {
		panic("assistant message event stream: nil iterator context")
	}
	return func(yield func(AssistantMessageEvent) bool) {
		if turn, ok := ctx.Value(continuationTurnKey{}).(*continuationTurn); ok && turn != nil {
			iterator := continuationEventIterator{stream: s, executor: turn.executor}
			for {
				delivery := awaitContinuation(turn, iterator.next(ctx))
				if delivery.done || !yield(s.deliver(delivery.event, materialize)) {
					return
				}
			}
		}
		for _, event := range s.observeEvents(ctx, materialize) {
			if !yield(event) {
				return
			}
		}
	}
}

// deliver sets the event's partial fields to the stream state at the consumer's tick.
func (s *AssistantMessageEventStream) deliver(event AssistantMessageEvent, materialize bool) AssistantMessageEvent {
	if !materialize {
		return event
	}
	return RefreshEvent(event)
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

// End closes iteration without synthesizing an event. An optional result resolves Result once; ending without a result leaves it pending, as Pi's EventStream.end does.
func (s *AssistantMessageEventStream) End(result ...*AssistantMessage) {
	s.mu.Lock()
	var continuations []func()
	defer func() {
		s.mu.Unlock()
		for _, continuation := range continuations {
			continuation()
		}
	}()
	s.terminal = true
	if len(result) > 0 && !s.resolved {
		s.publishTerminalLocked(result[0], assistantMessageReplacements{})
		s.result = result[0]
		continuations = append(continuations, s.resolveResultLocked()...)
	}
	for _, waiter := range s.waiters {
		if continuation := waiter.deliver(eventDelivery{done: true}); continuation != nil {
			continuations = append(continuations, continuation)
		}
	}
	s.waiters = nil
}

// Result waits for a terminal event or End result and returns the first resolved message pointer.
func (s *AssistantMessageEventStream) Result() *AssistantMessage {
	result, _ := s.ResultContext(context.Background())
	return result
}

// ResultContext waits for the final result or the caller's cancellation without changing stream ownership. The goroutine that runs a consumer callback awaits the result as Pi's `await stream.result()` inside that callback does: it releases the callback's turn and resumes in FIFO order. Any other goroutine only waits, so it never yields a callback that is running.
func (s *AssistantMessageEventStream) ResultContext(ctx context.Context) (*AssistantMessage, error) {
	s.mu.Lock()
	executor := s.executor
	s.mu.Unlock()
	if executor != nil {
		executor.mu.Lock()
		observation := executor.observation
		executor.mu.Unlock()
		if observation != nil && observation.ownedByCaller() {
			resume := observation.suspend()
			defer func() { resume(ctx.Err() == nil) }()
		}
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, nil
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
