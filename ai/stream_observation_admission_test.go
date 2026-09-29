package ai

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestStreamObservationContextSharesCallerExecutor(t *testing.T) {
	t.Parallel()
	ctx := WithStreamContinuations(t.Context())
	stream := NewAssistantMessageEventStream()
	forwarding := stream.ObservationContext(ctx)
	if got, want := forwarding.Value(continuationExecutorKey{}), ctx.Value(continuationExecutorKey{}); got != want {
		t.Fatal("forwarder replaced the caller's reaction queue")
	}
}

func TestStreamContinuationCallerReleasesOwnershipOnError(t *testing.T) {
	t.Parallel()
	ctx := WithStreamContinuations(t.Context())
	sentinel := errors.New("setup failure")
	if err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
		if got := StreamObservationFromContext(ctx); got != observation {
			t.Error("stable signal context lost the active observation")
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("caller error = %v, want %v", err, sentinel)
	}
	executor := ctx.Value(continuationExecutorKey{}).(*continuationExecutor)
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.active != nil || executor.observation != nil {
		t.Error("failed stream setup retained execution or its observation")
	}
}

// upstream: packages/agent/src/agent-loop.ts:402-413 awaits streamFunction before attaching its iterator. Promise reactions cannot interrupt that synchronous caller prefix.
func TestStreamObservationCallerPrefixPrecedesProducerReactions(t *testing.T) {
	executor := &continuationExecutor{}
	stream := NewAssistantMessageEventStream()
	stream.executor = executor
	var order []string
	executor.run(func(turn *continuationTurn) {
		ctx := context.WithValue(t.Context(), continuationTurnKey{}, turn)
		executor.post(func() {
			order = append(order, "producer")
			pushTestStart(t, stream)
			pushTestDone(t, stream)
		})
		order = append(order, "caller prefix")
		for observation, event := range stream.ObserveEvents(ctx) {
			if observation.turn != turn {
				t.Error("iterator replaced the caller continuation")
			}
			order = append(order, string(event.EventType()))
		}
	})
	want := []string{"caller prefix", "producer", "start", "done"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}
