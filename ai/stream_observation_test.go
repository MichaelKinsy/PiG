package ai

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestStreamObservationAwaitReleasesAndReacquires(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	sentinel := errors.New("wait failure")
	for observation, event := range stream.ObserveEvents(t.Context()) {
		if event.EventType() != EventStart {
			t.Fatalf("first event = %s", event.EventType())
		}
		err := observation.Await(func() error {
			executor := observation.turn.executor
			executor.mu.Lock()
			active := executor.active
			executor.mu.Unlock()
			if active == observation.turn {
				t.Error("blocking wait retained the synchronous observation")
				return sentinel
			}
			executor.run(func(*continuationTurn) { pushTestDone(t, stream) })
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("wait error = %v", err)
		}
		observation.turn.executor.mu.Lock()
		active := observation.turn.executor.active
		observation.turn.executor.mu.Unlock()
		if active != observation.turn {
			t.Error("wait returned without reacquiring observation ownership")
		}
		break
	}
}

// agent-loop.ts:419-420 awaits a sink without preventing the provider from settling response.result(). The retained message is observed again after this wait by the view tests.
func TestStreamObservationResultProgressesFromHeldConsumer(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	executor := &continuationExecutor{}
	stream.executor = executor
	pushTestStart(t, stream)
	entered := make(chan struct{})
	var workers sync.WaitGroup
	workers.Go(func() {
		<-entered
		executor.run(func(*continuationTurn) { pushTestDone(t, stream) })
	})
	for _, event := range stream.ObserveEvents(t.Context()) {
		if event.EventType() == EventStart {
			close(entered)
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Errorf("result = %#v", result)
			}
		}
	}
	workers.Wait()
}

func TestStreamObservationCanceledResultResumesConsumer(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	for observation, event := range stream.ObserveEvents(t.Context()) {
		if event.EventType() != EventStart {
			continue
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if result, err := stream.ResultContext(ctx); result != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("canceled result = %#v, %v", result, err)
		}
		if err := observation.Await(func() error {
			observation.turn.executor.run(func(*continuationTurn) { pushTestDone(t, stream) })
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStreamObservationParallelResultWaitersDoNotOwnParentAwait(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	for observation, event := range stream.ObserveEvents(t.Context()) {
		if event.EventType() != EventStart {
			continue
		}
		if err := observation.Await(func() error {
			var workers sync.WaitGroup
			for range 16 {
				workers.Go(func() {
					if result := stream.Result(); result.StopReason != StopReasonStop {
						t.Errorf("result = %#v", result)
					}
				})
			}
			observation.turn.executor.run(func(*continuationTurn) { pushTestDone(t, stream) })
			workers.Wait()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// event-stream.ts:88-90 result() only returns a promise. A goroutine that is not the consumer waits for it without yielding the callback that is running, so a producer reaction never runs in the middle of that callback.
func TestStreamObservationForeignResultDoesNotReleaseRunningConsumer(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	for observation, event := range stream.ObserveEvents(t.Context()) {
		if event.EventType() != EventStart {
			continue
		}
		reached := make(chan struct{})
		ctx := &doneProbeContext{Context: t.Context(), reached: reached}
		var foreign sync.WaitGroup
		foreign.Go(func() {
			_, _ = stream.ResultContext(ctx)
		})
		<-reached
		executor := observation.turn.executor
		executor.mu.Lock()
		active := executor.active
		executor.mu.Unlock()
		if active != observation.turn {
			t.Error("foreign ResultContext released a running consumer without its await")
		}
		pushTestDone(t, stream)
		foreign.Wait()
	}
}

// doneProbeContext reports the first Done call, which ResultContext makes after any turn handling.
type doneProbeContext struct {
	context.Context
	reached chan struct{}
	once    sync.Once
}

func (c *doneProbeContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Done()
}
