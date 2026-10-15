package ai

import (
	"context"
	"runtime"
	"testing"
)

// iteratorAllocsPerEvent is the heap allocations per event a continuation-turn iterator spends delivering already-pushed events, as a forwarder reads them: a context that is never canceled and no materialization.
func iteratorAllocsPerEvent(t *testing.T, events int) float64 {
	t.Helper()
	executor := &continuationExecutor{}
	ctx := context.WithValue(context.WithoutCancel(t.Context()), continuationExecutorKey{}, executor)
	stream := NewAssistantMessageEventStream()
	partial := lazyProbeMessage(StopReasonPending)
	for range events {
		_ = stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial})
	}
	_ = stream.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
	stream.End()
	var before, after runtime.MemStats
	delivered := 0
	runtime.GC()
	runtime.ReadMemStats(&before)
	executor.run(func(turn *continuationTurn) {
		for range stream.events(context.WithValue(ctx, continuationTurnKey{}, turn), false) {
			delivered++
		}
	})
	runtime.ReadMemStats(&after)
	if delivered != events+1 {
		t.Fatalf("delivered %d events, want %d", delivered, events+1)
	}
	return float64(after.Mallocs-before.Mallocs) / float64(delivered)
}

// A forwarder's next() on a never-canceled context arms no cancellation and keeps its promise in the waiter, and yield posts one reaction instead of settling a second promise: two allocations per event, where the path that built a waiting promise, a ready channel and an onReady closure spent three.
func TestContinuationIteratorDeliveryAllocations(t *testing.T) {
	const events = 4096
	if got := iteratorAllocsPerEvent(t, events); got > 2.5 {
		t.Fatalf("continuation iterator spends %.2f allocations per event, want at most 2.5", got)
	}
}

// An event whose partial maps to itself is the same value, so it is returned without boxing a copy; a changed partial still yields a new event.
func TestMapAssistantEventPartialKeepsUnchangedEvent(t *testing.T) {
	partial := lazyProbeMessage(StopReasonPending)
	var event AssistantMessageEvent = TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial}
	identity := func(message *AssistantMessage) *AssistantMessage { return message }
	if allocs := testing.AllocsPerRun(100, func() { _ = mapAssistantEventPartial(event, identity) }); allocs != 0 {
		t.Fatalf("an unchanged partial cost %.0f allocations, want 0", allocs)
	}
	replacement := lazyProbeMessage(StopReasonPending)
	mapped, ok := mapAssistantEventPartial(event, func(*AssistantMessage) *AssistantMessage { return replacement }).(TextDeltaEvent)
	if !ok || mapped.Partial != replacement || mapped.Delta != "d" || event.(TextDeltaEvent).Partial != partial {
		t.Fatalf("mapped = %#v, original = %#v", mapped, event)
	}
}

// waitingIteratorAllocsPerEvent is the heap allocations per event of a continuation-turn iterator that is already waiting when each event is pushed, as the agent loop reads a live provider stream.
func waitingIteratorAllocsPerEvent(t *testing.T, ctx context.Context, events int) float64 {
	t.Helper()
	executor := &continuationExecutor{}
	ctx = context.WithValue(ctx, continuationExecutorKey{}, executor)
	stream := NewAssistantMessageEventStream()
	partial := lazyProbeMessage(StopReasonPending)
	pushed := make([]AssistantMessageEvent, events)
	for index := range pushed {
		pushed[index] = TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial}
	}
	final := AssistantMessageEvent(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
	waitForWaiter := func() {
		for {
			stream.mu.Lock()
			waiting := len(stream.waiters)
			stream.mu.Unlock()
			if waiting > 0 {
				return
			}
			runtime.Gosched()
		}
	}
	var before, after runtime.MemStats
	delivered := 0
	runtime.GC()
	runtime.ReadMemStats(&before)
	producer := make(chan struct{})
	go func() {
		defer close(producer)
		for _, event := range pushed {
			waitForWaiter()
			_ = stream.Push(event)
		}
		waitForWaiter()
		_ = stream.Push(final)
		stream.End()
	}()
	executor.run(func(turn *continuationTurn) {
		for range stream.events(context.WithValue(ctx, continuationTurnKey{}, turn), false) {
			delivered++
		}
	})
	<-producer
	runtime.ReadMemStats(&after)
	if delivered != events+1 {
		t.Fatalf("delivered %d events, want %d", delivered, events+1)
	}
	return float64(after.Mallocs-before.Mallocs) / float64(delivered)
}

// A waiting next() on a cancelable context (the agent loop's) shares one cancellation registration per iteration instead of arming context.AfterFunc and a ready channel per event: it costs no more allocations per event than a never-canceled context.
func TestCancelableIteratorWaitingAllocations(t *testing.T) {
	const events = 2048
	cancelable, cancel := context.WithCancel(t.Context())
	defer cancel()
	base := waitingIteratorAllocsPerEvent(t, context.WithoutCancel(t.Context()), events)
	got := waitingIteratorAllocsPerEvent(t, cancelable, events)
	if got-base > 0.5 {
		t.Fatalf("a cancelable iterator spends %.2f allocations per event, %.2f more than a never-canceled one (%.2f), want at most 0.5 more", got, got-base, base)
	}
}
