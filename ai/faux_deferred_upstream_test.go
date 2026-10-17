package ai

import (
	"reflect"
	"testing"
)

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:747 — submission portion; the complete submit/poll/redeem path uses the Models collection below.
// mutation-checked: the mutant "the faux provider ignores StreamOptions.Deferred" (ai/faux.go, the deferred branch of the stream) fails it.
func TestFauxDeferredSubmissionUpstream(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{})
	faux.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("ready")}, StopReason: "stop"})})
	stream, err := faux.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{Deferred: &DeferredOption{Object: true, Window: "1h"}})
	if err != nil {
		t.Fatal(err)
	}
	types := []AssistantEventType{}
	for event := range stream.Events(t.Context()) {
		types = append(types, event.EventType())
	}
	result := stream.Result()
	if !reflect.DeepEqual(types, []AssistantEventType{EventStart, EventDone}) || result.StopReason != StopReasonDeferred || len(result.Content) != 0 || result.Deferred == nil {
		t.Fatalf("events=%v result=%#v", types, result)
	}
}
