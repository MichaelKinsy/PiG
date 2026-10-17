package ai

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func drainEvents[T, R any](stream *EventStream[T, R]) []T {
	var events []T
	for event := range stream.Events() {
		events = append(events, event)
	}
	return events
}

func newIntStream(complete func(int) bool) *EventStream[int, int] {
	return NewEventStream(complete, func(event int) int { return event })
}

// Pi packages/ai/test/event-stream.test.ts "drains buffered events in order and ignores events pushed after completion".
// mutation-checked: zeroing the results of EventStream.Events fails it
// Pi: packages/ai/src/utils/event-stream.ts:72 ([Symbol.asyncIterator])
// Pi packages/ai/test/event-stream.test.ts:6 "drains buffered events in order and ignores events pushed after completion".
func TestEventStreamDrainsBufferedEventsAndIgnoresPushAfterCompletion(t *testing.T) {
	stream := newIntStream(func(event int) bool { return event == 3 })
	for _, event := range []int{1, 2, 3, 4} {
		stream.Push(event)
	}
	if got := stream.Result(); got != 3 {
		t.Fatalf("result %d", got)
	}
	if got := drainEvents(stream); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("events %v", got)
	}
}

// Async iteration: packages/ai/src/utils/event-stream.ts:72 `[Symbol.asyncIterator]` is Events.
// mutation-checked: zeroing the results of EventStream.Events fails it
// Pi: packages/ai/src/utils/event-stream.ts:72 ([Symbol.asyncIterator])
// Pi packages/ai/test/event-stream.test.ts:25 "preserves order when events arrive after buffered draining starts".
// packages/ai/src/utils/event-stream.ts:72 EventStream[Symbol.asyncIterator]: iterating yields events in push order, including events pushed while draining (Go: Events()).
func TestEventStreamPreservesOrderWhenEventsArriveWhileDraining(t *testing.T) {
	stream := newIntStream(func(int) bool { return false })
	stream.Push(1)
	stream.Push(2)
	var got []int
	for event := range stream.Events() {
		got = append(got, event)
		if event == 1 {
			stream.Push(3)
			go func() { time.Sleep(10 * time.Millisecond); stream.End(3) }()
		}
	}
	if !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("events %v", got)
	}
}

// "delivers events to waiting consumers in registration order".
// mutation-checked: zeroing the results of EventStream.Events fails it
// Pi packages/ai/test/event-stream.test.ts:44 "delivers events to waiting consumers in registration order".
func TestEventStreamDeliversToWaitingConsumersInRegistrationOrder(t *testing.T) {
	stream := newIntStream(func(int) bool { return false })
	first, second := make(chan int, 1), make(chan int, 1)
	start := func(out chan int) {
		go func() {
			for event := range stream.Events() {
				out <- event
				return
			}
			close(out)
		}()
	}
	start(first)
	for deadline := time.Now().Add(time.Second); ; {
		stream.mu.Lock()
		waiting := len(stream.waiting)
		stream.mu.Unlock()
		if waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first consumer never waited")
		}
		time.Sleep(time.Millisecond)
	}
	start(second)
	for deadline := time.Now().Add(time.Second); ; {
		stream.mu.Lock()
		waiting := len(stream.waiting)
		stream.mu.Unlock()
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second consumer never waited")
		}
		time.Sleep(time.Millisecond)
	}
	stream.Push(1)
	stream.Push(2)
	if got := <-first; got != 1 {
		t.Fatalf("first consumer got %d", got)
	}
	if got := <-second; got != 2 {
		t.Fatalf("second consumer got %d", got)
	}
}

// "drains buffered events after end and resolves the explicit result".
// Pi: packages/ai/src/api/anthropic-messages.ts:421 (push)
// Pi: packages/ai/src/api/bedrock-converse-stream.ts:955 (result)
// Pi packages/ai/test/event-stream.test.ts:61 "drains buffered events after end and resolves the explicit result".
func TestEventStreamDrainsBufferedEventsAfterEndWithResult(t *testing.T) {
	stream := NewEventStream(func(int) bool { return false }, func(event int) string { return "" })
	stream.Push(1)
	stream.Push(2)
	stream.End("complete")
	if got := stream.Result(); got != "complete" {
		t.Fatalf("result %q", got)
	}
	if got := drainEvents(stream); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("events %v", got)
	}
}

// "wakes all waiting consumers when ended without a result"; Result then stays unresolved (upstream end() without an argument).
// Pi: packages/ai/src/api/anthropic-messages.ts:886 (end)
// Pi packages/ai/test/event-stream.test.ts:79 "wakes all waiting consumers when ended without a result"; Result then stays unresolved (upstream end() without an argument).
func TestEventStreamEndWithoutResultWakesWaitersAndLeavesResultPending(t *testing.T) {
	stream := newIntStream(func(int) bool { return false })
	finished := make(chan []int, 2)
	for range 2 {
		go func() { finished <- drainEvents(stream) }()
	}
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		stream.mu.Lock()
		waiting := len(stream.waiting)
		stream.mu.Unlock()
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("consumers never waited")
		}
	}
	stream.End()
	for range 2 {
		select {
		case got := <-finished:
			if len(got) != 0 {
				t.Fatalf("events %v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("waiting consumer was not released")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := stream.ResultContext(ctx); err == nil {
		t.Fatal("Result resolved after End without a result")
	}
}

// utils/event-stream.ts resolveFinalResult: only the first result counts. A completing event settles the result and a later end(result) neither replaces it nor fails.
func TestEventStreamKeepsTheFirstResultWhenEndedWithAnotherOne(t *testing.T) {
	stream := NewEventStream(func(event int) bool { return event >= 10 }, func(event int) string { return "from-event" })
	stream.Push(1)
	stream.Push(10)
	stream.End("from-end")
	if got := stream.Result(); got != "from-event" {
		t.Fatalf("result = %q, want from-event", got)
	}
	if got := drainEvents(stream); !slices.Equal(got, []int{1, 10}) {
		t.Fatalf("events = %v, want [1 10]", got)
	}
}

// utils/event-stream.ts result(): the final result promise resolves with the completing event's result; ResultContext waits for it but gives up when its context ends, with the context's cause.
func TestEventStreamResultContextWaitsForTheResultAndHonoursItsContext(t *testing.T) {
	waitFor := func(stream *EventStream[int, int], ctx context.Context) (int, error) {
		return stream.ResultContext(ctx)
	}
	stream := newIntStream(func(event int) bool { return event == 7 })
	cause := errors.New("stop waiting")
	cancelled, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if _, err := waitFor(stream, cancelled); !errors.Is(err, cause) {
		t.Fatalf("ResultContext on a cancelled context = %v, want its cause", err)
	}
	// The result arrives while a caller waits.
	got := make(chan int, 1)
	go func() {
		result, err := waitFor(stream, context.Background())
		if err != nil {
			t.Error(err)
		}
		got <- result
	}()
	select {
	case result := <-got:
		t.Fatalf("ResultContext returned %d before the stream completed", result)
	case <-time.After(20 * time.Millisecond):
	}
	stream.Push(3)
	stream.Push(7)
	if result := <-got; result != 7 {
		t.Fatalf("ResultContext = %d, want 7", result)
	}
	if result := stream.Result(); result != 7 {
		t.Fatalf("Result = %d, want 7", result)
	}
}
