package ai

import (
	"runtime"
	"testing"
)

// continuationHandOffAllocs is the heap allocations of streaming events from a provider turn that awaits once after each push to a consumer iterating on another goroutine.
func continuationHandOffAllocs(t *testing.T, events int) uint64 {
	t.Helper()
	executor := &continuationExecutor{}
	stream := NewAssistantMessageEventStream()
	stream.executor = executor
	partial := testAssistant(StopReasonPending)
	deltas := make([]AssistantMessageEvent, events)
	for index := range deltas {
		deltas[index] = TextDeltaEvent{Delta: "x", Partial: partial}
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	producer := executor.newTurn()
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		producer.run(func(turn *continuationTurn) {
			if err := stream.Push(StartEvent{Partial: partial}); err != nil {
				t.Error(err)
			}
			for _, delta := range deltas {
				if err := stream.Push(delta); err != nil {
					t.Error(err)
					return
				}
				suspendContinuation(turn)
			}
			pushTestDone(t, stream)
		})
	}()
	delivered := 0
	for range stream.events(t.Context(), false) {
		delivered++
	}
	<-produced
	runtime.ReadMemStats(&after)
	if want := events + 2; delivered != want {
		t.Fatalf("consumer saw %d events, want %d", delivered, want)
	}
	return after.Mallocs - before.Mallocs
}

// The executor's per-event bookkeeping allocates nothing: the iterator reuses its promises and waiter and registers its cancellation hook once, an acquire is a queued value, and an await of a resolved promise uses the executor's shared one or resumes at once. At 4cfaf24153 an event cost 7 allocations on this path; the 3 left are the stream's own (the pushed event's partial view and its boxing, and the consumer's per-event observation).
func TestContinuationHandOffAllocsPerEvent(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const n = 1_000
	small := continuationHandOffAllocs(t, n)
	large := continuationHandOffAllocs(t, 2*n)
	perEvent := float64(large-small) / n
	t.Logf("allocations: %d at %d events, %d at %d events (%.2f per event)", small, n, large, 2*n, perEvent)
	if perEvent > continuationHandOffAllocsCeiling {
		t.Fatalf("%.2f allocations per streamed event, want at most %v", perEvent, continuationHandOffAllocsCeiling)
	}
}

// continuationHandOffAllocsCeiling is the 3 allocations per event with room for a few allocations that do not scale with the events.
const continuationHandOffAllocsCeiling = 3.25
