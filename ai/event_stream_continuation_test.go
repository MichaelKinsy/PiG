package ai

import (
	"context"
	"reflect"
	"testing"
)

// Pi event-stream.ts:78-91 awaits a pending waiter before yielding its value. The exact Node probe in continuation-node-iterator.log distinguishes that await from a buffered yield using two nested queueMicrotask reactions, without timers.
func TestStreamContinuationIteratorMatchesPiYieldOrdering(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		name := "waiting"
		if buffered {
			name = "buffered"
		}
		t.Run(name, func(t *testing.T) {
			executor := &continuationExecutor{}
			stream := NewAssistantMessageEventStream()
			iterator := continuationEventIterator{stream: stream, executor: executor}
			var order []string
			executor.run(func(*continuationTurn) {
				if buffered {
					pushTestStart(t, stream)
				}
				next := iterator.next(t.Context())
				next.onResolved(func(delivery eventDelivery) {
					if delivery.done || delivery.event.EventType() != EventStart {
						t.Errorf("next delivery = %#v", delivery)
					}
					order = append(order, "observation")
				})
				if !buffered {
					pushTestStart(t, stream)
				}
				executor.post(func() {
					order = append(order, "producer continuation 1")
					executor.post(func() { order = append(order, "producer continuation 2") })
				})
				order = append(order, "producer segment")
			})
			want := []string{"producer segment", "producer continuation 1", "producer continuation 2", "observation"}
			if buffered {
				want = []string{"producer segment", "producer continuation 1", "observation", "producer continuation 2"}
			}
			if !reflect.DeepEqual(order, want) {
				t.Fatalf("continuation order = %v, want %v", order, want)
			}
			stream.End()
		})
	}
}

func TestStreamContinuationIteratorCancellationRemovesWaiter(t *testing.T) {
	executor := &continuationExecutor{}
	stream := NewAssistantMessageEventStream()
	iterator := continuationEventIterator{stream: stream, executor: executor}
	executor.run(func(turn *continuationTurn) {
		ctx, cancel := context.WithCancel(t.Context())
		next := iterator.next(ctx)
		cancel()
		if delivery := awaitContinuation(turn, next); !delivery.done {
			t.Errorf("canceled delivery = %#v", delivery)
		}
	})
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.waiters) != 0 {
		t.Fatal("canceled iterator retained a waiter")
	}
}

func TestStreamContinuationIteratorAssignedEventWinsCancellation(t *testing.T) {
	executor := &continuationExecutor{}
	stream := NewAssistantMessageEventStream()
	iterator := continuationEventIterator{stream: stream, executor: executor}
	executor.run(func(turn *continuationTurn) {
		ctx, cancel := context.WithCancel(t.Context())
		next := iterator.next(ctx)
		pushTestStart(t, stream)
		cancel()
		if delivery := awaitContinuation(turn, next); delivery.done || delivery.event.EventType() != EventStart {
			t.Errorf("assigned event lost to cancellation: %#v", delivery)
		}
	})
	stream.End()
}

func TestStreamContinuationResultAfterEndWaitsForExplicitResult(t *testing.T) {
	executor := &continuationExecutor{}
	stream := NewAssistantMessageEventStream()
	final := testAssistant(StopReasonStop)
	executor.run(func(turn *continuationTurn) {
		stream.End()
		result := stream.resultContinuation(executor)
		executor.post(func() { stream.End(final) })
		if got := awaitContinuation(turn, result); got != final {
			t.Errorf("result = %p, want %p", got, final)
		}
	})
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.resultWaiters != nil {
		t.Fatal("resolved result retained reactions")
	}
}

func TestStreamContinuationIteratorRetainsFIFOAndEndResult(t *testing.T) {
	executor := &continuationExecutor{}
	stream := NewAssistantMessageEventStream()
	iterator := continuationEventIterator{stream: stream, executor: executor}
	executor.run(func(turn *continuationTurn) {
		pushTestStart(t, stream)
		final := pushTestDone(t, stream)
		if stream.Result() != final {
			t.Fatal("result identity changed or depended on iteration")
		}
		for _, want := range []AssistantEventType{EventStart, EventDone} {
			delivery := awaitContinuation(turn, iterator.next(t.Context()))
			if delivery.done || delivery.event.EventType() != want {
				t.Fatalf("delivery = %#v, want %s", delivery, want)
			}
		}
		if delivery := awaitContinuation(turn, iterator.next(t.Context())); !delivery.done {
			t.Errorf("closed iterator returned %#v", delivery)
		}
	})
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.queue != nil || len(stream.waiters) != 0 {
		t.Fatal("drained iterator retained queue entries or waiters")
	}
}
