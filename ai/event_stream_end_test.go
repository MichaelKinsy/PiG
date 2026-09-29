package ai

import (
	"context"
	"errors"
	"testing"
)

// Pi utils/event-stream.ts:59-70 resolves an end(result) without fabricating a terminal event. The SDK stream-options fixture uses precisely this producer shape.
func TestAssistantStreamEndResolvesWithoutEvents(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	result := &AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop}
	stream.End(result)
	for event := range stream.Events(t.Context()) {
		t.Fatalf("end fabricated %T", event)
	}
	if got := stream.Result(); got != result {
		t.Fatalf("result identity=%p want %p", got, result)
	}
	stream.End(&AssistantMessage{StopReason: StopReasonError})
	if got := stream.Result(); got != result {
		t.Fatal("second end replaced the settled result")
	}
	if err := stream.Push(StartEvent{Partial: result}); err != nil {
		t.Fatal(err)
	}
	for event := range stream.Events(t.Context()) {
		t.Fatalf("push after end emitted %T", event)
	}
}

func TestAssistantStreamEndWithoutResultClosesOnlyIteration(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	stream.End()
	for event := range stream.Events(t.Context()) {
		t.Fatalf("end fabricated %T", event)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := stream.ResultContext(ctx); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	result := &AssistantMessage{StopReason: StopReasonStop}
	stream.End(result)
	if got := stream.Result(); got != result {
		t.Fatal("late explicit result lost")
	}
}
