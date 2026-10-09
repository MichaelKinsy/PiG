package ai

// Ports packages/ai/src/utils/event-stream.ts:EventStream.

import (
	"context"
	"iter"
	"sync"
)

// EventStream delivers pushed events in order to its consumers and resolves one result. An event for which isComplete is true ends the stream and supplies the result through extractResult. Events pushed after the stream ended are dropped. Events are buffered without bound until read. Waiting consumers receive events in the order they began waiting.
type EventStream[T, R any] struct {
	isComplete    func(T) bool
	extractResult func(T) R

	mu      sync.Mutex
	queue   []T
	waiting []chan T
	done    bool
	result  R
	settled chan struct{}
	closed  bool
}

// NewEventStream creates a stream that ends when isComplete reports an event and then resolves with extractResult of that event.
func NewEventStream[T, R any](isComplete func(T) bool, extractResult func(T) R) *EventStream[T, R] {
	return &EventStream[T, R]{isComplete: isComplete, extractResult: extractResult, settled: make(chan struct{})}
}

func (s *EventStream[T, R]) settle(result R) {
	if s.closed {
		return
	}
	s.closed = true
	s.result = result
	//portlint:allow doubleclose settle returns early once closed is set, and every caller holds s.mu
	close(s.settled)
}

// Push delivers an event to the longest-waiting consumer, or buffers it. It ignores events after the stream ended.
func (s *EventStream[T, R]) Push(event T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	if s.isComplete(event) {
		s.done = true
		s.settle(s.extractResult(event))
	}
	if len(s.waiting) > 0 {
		waiter := s.waiting[0]
		s.waiting = s.waiting[1:]
		waiter <- event
		return
	}
	s.queue = append(s.queue, event)
}

// End ends the stream and releases every waiting consumer; buffered events remain readable. With a result it resolves Result unless Result is already resolved; without one Result stays unresolved, as upstream end() without an argument leaves it. Only the first result counts.
func (s *EventStream[T, R]) End(result ...R) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	if len(result) > 0 {
		s.settle(result[0])
	}
	s.finish()
}

func (s *EventStream[T, R]) finish() {
	s.done = true
	for _, waiter := range s.waiting {
		close(waiter)
	}
	s.waiting = nil
}

// Events yields buffered and future events in order and returns once the stream ended and its buffer is empty.
func (s *EventStream[T, R]) Events() iter.Seq[T] {
	return func(yield func(T) bool) {
		for {
			s.mu.Lock()
			if len(s.queue) > 0 {
				event := s.queue[0]
				var zero T
				s.queue[0] = zero
				s.queue = s.queue[1:]
				s.mu.Unlock()
				if !yield(event) {
					return
				}
				continue
			}
			if s.done {
				s.mu.Unlock()
				return
			}
			waiter := make(chan T, 1)
			s.waiting = append(s.waiting, waiter)
			s.mu.Unlock()
			event, ok := <-waiter
			if !ok || !yield(event) {
				return
			}
		}
	}
}

// Result blocks until the stream resolves its result.
func (s *EventStream[T, R]) Result() R {
	<-s.settled
	return s.result
}

// ResultContext is Result that gives up when ctx ends.
func (s *EventStream[T, R]) ResultContext(ctx context.Context) (R, error) {
	select {
	case <-s.settled:
		return s.result, nil
	case <-ctx.Done():
		var zero R
		return zero, context.Cause(ctx)
	}
}
