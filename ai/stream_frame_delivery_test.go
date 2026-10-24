package ai

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitForEventWaiter blocks until a consumer is registered for the next event of stream.
func waitForEventWaiter(t *testing.T, stream *AssistantMessageEventStream) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		waiting := len(stream.waiters) > 0
		stream.mu.Unlock()
		if waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no consumer waits for an event")
}

// A consumer that waits for an event reads the partial at its own tick, as Pi's listener reads the one shared object: the state after every push of the producer's turn, not the state at the push of its event. A partial it kept does not change behind it.
func TestWaitingConsumerReadsPartialsAtItsTick(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "test", "model")
	var mu sync.Mutex
	var seen []string
	var retained []*AssistantMessage
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range builder.stream.Events(t.Context()) {
			partial := eventPartial(event)
			switch typed := event.(type) {
			case StartEvent:
				partial = typed.Partial
			case DoneEvent:
				partial = typed.Message
			}
			text := ""
			if len(partial.Content) > 0 {
				text = partial.Content[0].(TextContent).Text
			}
			mu.Lock()
			seen = append(seen, string(event.EventType())+":"+text)
			retained = append(retained, partial)
			mu.Unlock()
		}
	}()
	for _, segment := range []func(){
		builder.start,
		func() { builder.textDelta("a") },
		func() { builder.textDelta("b") },
		func() { builder.done(StopReasonStop, nil, "") },
	} {
		waitForEventWaiter(t, builder.stream)
		runManagedBuilder(builder, segment)
	}
	<-done
	want := []string{"start:", "text_start:a", "text_delta:a", "text_delta:ab", "text_end:ab", "done:ab"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("partials read at delivery = %q, want %q", seen, want)
	}
	for i, partial := range retained {
		text := ""
		if len(partial.Content) > 0 {
			text = partial.Content[0].(TextContent).Text
		}
		if want := strings.TrimPrefix(want[i][strings.IndexByte(want[i], ':'):], ":"); text != want {
			t.Errorf("event %d: the retained partial reads %q after delivery, want the delivered %q", i, text, want)
		}
	}
}

// A refresh of an event whose partial was built for the consumer that takes it returns the event itself; a refresh after the producer published again builds the current state.
func TestRefreshEventKeepsACurrentPartial(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "test", "model")
	var first AssistantMessageEvent
	got := make(chan struct{})
	go func() {
		defer close(got)
		for event := range builder.stream.Events(t.Context()) {
			if first == nil {
				first = event
			}
		}
	}()
	waitForEventWaiter(t, builder.stream)
	runManagedBuilder(builder, builder.start)
	waitForEventWaiter(t, builder.stream)
	runManagedBuilder(builder, func() { builder.done(StopReasonStop, nil, "") })
	<-got
	start := first.(StartEvent)
	if again := RefreshEvent(start).(StartEvent); again.Partial == start.Partial {
		t.Fatalf("a partial of the pending stream was kept after the producer published the final message: %#v", again.Partial)
	}
	current := RefreshEvent(start).(StartEvent)
	if next := RefreshEvent(current).(StartEvent); next.Partial != current.Partial {
		t.Fatal("refreshing a current partial built a second one")
	}
}

// Usage counters are not shared between the handles of different deliveries or the producer's snapshots: a write through one handle reaches no other reader.
func TestUsageCountersAreNotSharedBetweenDeliveries(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIAnthropicMessages, "test", "model")
	cacheWrite, reasoning := 7, 11
	runManagedBuilder(builder, func() {
		builder.partial.Usage.CacheWrite1h, builder.partial.Usage.Reasoning = &cacheWrite, &reasoning
		builder.textDelta("a")
		builder.textDelta("b")
	})
	var handles []*AssistantMessage
	for event := range builder.stream.Events(t.Context()) {
		if delta, ok := event.(TextDeltaEvent); ok {
			handles = append(handles, delta.Partial)
		}
		if len(handles) == 2 {
			break
		}
	}
	if len(handles) != 2 {
		t.Fatalf("%d deltas", len(handles))
	}
	later := RefreshEvent(TextDeltaEvent{Partial: handles[0]}).(TextDeltaEvent).Partial
	for _, handle := range []*AssistantMessage{handles[0], handles[1], later, builder.partial} {
		if handle.Usage.CacheWrite1h == nil || *handle.Usage.CacheWrite1h != 7 || *handle.Usage.Reasoning != 11 {
			t.Fatalf("usage = %#v", handle.Usage)
		}
	}
	*handles[0].Usage.CacheWrite1h, *handles[0].Usage.Reasoning = 99, 99
	for name, usage := range map[string]Usage{"other delivery": handles[1].Usage, "observation": handles[0].Observe().Usage, "producer": builder.partial.Usage, "observed usage": handles[1].ObserveUsage()} {
		if *usage.CacheWrite1h != 7 || *usage.Reasoning != 11 {
			t.Errorf("%s: a write through a delivered handle changed %d, %d", name, *usage.CacheWrite1h, *usage.Reasoning)
		}
	}
}

// An observation that a host callback obtained from its context is not handed to a later callback of the same Events loop; one that nothing obtained is scoped again.
func TestEventsDoesNotReuseAnObservationACallbackObtained(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "test", "model")
	runManagedBuilder(builder, func() {
		builder.textDelta("a")
		builder.textDelta("b")
		builder.textDelta("c")
	})
	var obtained []*StreamObservation
	for range builder.stream.Events(t.Context()) {
		obtained = append(obtained, StreamObservationFromContext(builder.stream.ObservationContext(t.Context())))
		if len(obtained) == 4 {
			break
		}
	}
	for i := range obtained {
		for j := range i {
			if obtained[i] != nil && obtained[i] == obtained[j] {
				t.Fatalf("callbacks %d and %d ran under one observation that the first obtained", j, i)
			}
		}
	}
}
