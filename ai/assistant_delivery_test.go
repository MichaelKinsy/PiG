package ai

import (
	"reflect"
	"testing"
)

// Pi queues references, so a consumer reading event.partial at its own tick sees the state at that tick. Exported fields of a delivered partial must therefore equal its Observe() at delivery; a handle frozen at the first publication would read empty content here (D82 stale-view trap).
func TestDeliveredPartialFieldsEqualObservationAtDelivery(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "test", "model")
	runManagedBuilder(builder, func() {
		builder.start()
		builder.textDelta("one")
		builder.textDelta(" two")
		builder.done(StopReasonStop, nil, "")
	})
	count := 0
	for event := range builder.stream.Events(t.Context()) {
		partial := eventPartial(event)
		if start, ok := event.(StartEvent); ok {
			partial = start.Partial
		}
		if partial == nil {
			continue
		}
		count++
		if got, want := partial.Content, partial.Observe().Content; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: exported Content = %#v, observation = %#v", event.EventType(), got, want)
		}
		if partial.StopReason != StopReasonStop {
			t.Fatalf("%s: exported StopReason = %q, want stop (queued events observe the finished producer)", event.EventType(), partial.StopReason)
		}
		if len(partial.Content) != 1 || partial.Content[0].(TextContent).Text != "one two" {
			t.Fatalf("%s: exported Content = %#v", event.EventType(), partial.Content)
		}
	}
	if count != 5 {
		t.Fatalf("saw %d partial events, want 5", count)
	}
}

// A retained delivered partial keeps its delivery-time fields (Go value semantics) and RefreshEvent produces the current state on demand.
func TestRefreshEventAdvancesRetainedPartial(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "test", "model")
	runManagedBuilder(builder, builder.start)
	var retained AssistantMessageEvent
	for event := range builder.stream.Events(t.Context()) {
		retained = event
		break
	}
	if got := retained.(StartEvent).Partial; len(got.Content) != 0 || got.StopReason != StopReasonPending {
		t.Fatalf("delivered start = %#v", got)
	}
	runManagedBuilder(builder, func() {
		builder.textDelta("later")
		builder.done(StopReasonStop, nil, "")
	})
	if got := retained.(StartEvent).Partial; len(got.Content) != 0 {
		t.Fatalf("delivered fields changed behind the reader: %#v", got.Content)
	}
	refreshed := RefreshEvent(retained).(StartEvent).Partial
	if len(refreshed.Content) != 1 || refreshed.StopReason != StopReasonStop {
		t.Fatalf("refreshed = %#v", refreshed)
	}
}
