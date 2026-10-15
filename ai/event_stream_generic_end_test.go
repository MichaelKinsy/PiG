package ai

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
)

// utils/event-stream.ts end(): ending twice is harmless, also while a consumer waited at the first end (the first end released it; the second finds nobody).
func TestEventStreamEndTwiceWithAWaitingConsumerDoesNotPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := newIntStream(func(int) bool { return false })
		released := make(chan struct{})
		go func() {
			drainEvents(stream)
			close(released)
		}()
		synctest.Wait()
		stream.mu.Lock()
		waiting := len(stream.waiting)
		stream.mu.Unlock()
		if waiting != 1 {
			t.Fatalf("%d consumers waiting, want 1", waiting)
		}
		end := func(stream *EventStream[int, int]) { stream.End() }
		end(stream)
		<-released
		end(stream)
	})
}

// event-stream.ts [Symbol.asyncIterator]: a consumer that stops after one buffered event leaves the rest readable by the next iteration.
func TestEventStreamStoppingAConsumerKeepsTheRestBuffered(t *testing.T) {
	stream := newIntStream(func(event int) bool { return event == 3 })
	for _, event := range []int{1, 2, 3} {
		stream.Push(event)
	}
	for event := range stream.Events() {
		if event != 1 {
			t.Fatalf("first event %d", event)
		}
		break
	}
	if got := drainEvents(stream); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("events after the stop = %v, want [2 3]", got)
	}
}

// A consumer that stops while it was waiting hands nothing back: the pushed event it received is its own.
func TestEventStreamStoppingAWaitingConsumerStopsAfterItsEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := newIntStream(func(int) bool { return false })
		got := make(chan int, 4)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for event := range stream.Events() {
				got <- event
				return
			}
		}()
		synctest.Wait()
		stream.mu.Lock()
		waiting := len(stream.waiting)
		stream.mu.Unlock()
		if waiting != 1 {
			t.Fatalf("%d consumers waiting, want 1", waiting)
		}
		stream.Push(7)
		<-finished
		if event := <-got; event != 7 {
			t.Fatalf("event %d", event)
		}
		stream.Push(8)
		stream.End()
		if rest := drainEvents(stream); !slices.Equal(rest, []int{8}) {
			t.Fatalf("a later consumer reads %v, want [8]", rest)
		}
	})
}

// ResultContext reports the context's cause, not the plain context error.
func TestEventStreamResultContextReportsTheCause(t *testing.T) {
	stream := newIntStream(func(int) bool { return false })
	if _, ok := any(stream).(*EventStream[int, int]); !ok {
		t.Fatalf("newIntStream returned %T", stream)
	}
	cause := errors.New("stopped waiting")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if _, err := stream.ResultContext(ctx); !errors.Is(err, cause) {
		t.Fatalf("ResultContext error = %v, want the cause", err)
	}
}
