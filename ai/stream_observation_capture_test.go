package ai

import (
	"reflect"
	"testing"
)

// A capture taken at an update's tick builds, after every later publication of the stream, exactly what ShallowCopy
// and ShallowCopy().Observe() returned at that tick; the copy it builds reads its retained nested objects as the copy
// taken then does.
func TestShallowCaptureBuildsTheCopyOfItsTick(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{TokenSize: &FauxTokenSize{Min: new(1), Max: new(2)}})
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{
		FauxThinking("thinking it over in a few chunks"),
		FauxText("an answer streamed in several chunks"),
		FauxToolCall("write", map[string]any{"path": "a.txt", "content": "some content to stream"}, nil),
	}})})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("go")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	type tick struct {
		capture  ShallowCapture
		copy     *AssistantMessage
		observed *AssistantMessage
	}
	var ticks []tick
	for observation, event := range stream.ObservePublishedEvents(t.Context()) {
		partial := eventPartial(event)
		if partial == nil {
			continue
		}
		var capture ShallowCapture
		capture.Capture(partial)
		copied := RefreshEvent(event)
		view := eventPartial(copied).ShallowCopy()
		ticks = append(ticks, tick{capture: capture, copy: view, observed: view.Observe()})
		// Let the producer run ahead, so later publications land between the capture and its use.
		observation.Yield()
	}
	if len(ticks) < 10 {
		t.Fatalf("%d partial events, want a streamed response", len(ticks))
	}
	for i := range ticks {
		tick := &ticks[i]
		if got := tick.capture.ShallowCopy(); !reflect.DeepEqual(got, tick.copy) {
			t.Fatalf("tick %d: ShallowCopy from the capture differs from the copy taken at the tick:\n got %+v\nwant %+v", i, got, tick.copy)
		}
		if got := tick.capture.Observe(); !reflect.DeepEqual(got, tick.observed) {
			t.Fatalf("tick %d: Observe from the capture differs from the observation at the tick:\n got %+v\nwant %+v", i, got, tick.observed)
		}
		if got, want := tick.capture.ShallowCopy().Observe(), tick.copy.Observe(); !reflect.DeepEqual(got, want) {
			t.Fatalf("tick %d: the copies observe different nested state now:\n got %+v\nwant %+v", i, got, want)
		}
	}
	var none ShallowCapture
	none.Capture(nil)
	if none.ShallowCopy() != nil || none.Observe() != nil {
		t.Fatal("a capture of nil builds a message")
	}
}
