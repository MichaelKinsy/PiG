package ai

import (
	"context"
	"testing"
)

// Upstream event-stream.ts #time assigns durationMs to the shared message object when the provider pushes the final event, so a consumer's `start` partial of the same response observes it (parity scenarios 17-rpc-abort-retry and 33-rpc-real-provider-records-mistral compare Pi's message_start with durationMs).
func TestAssistantStartPartialObservesTheFinalDuration(t *testing.T) {
	provider := &TestFauxProvider{}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "Trigger: retryable provider error"}}, Timestamp: 1}}})
	stream, err := provider.Stream(context.Background(), transcript, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonError || result.DurationMs == nil {
		t.Fatalf("result = stop %s, durationMs %v", result.StopReason, result.DurationMs)
	}
	seen := false
	for event := range stream.Events(context.Background()) {
		if start, ok := event.(StartEvent); ok {
			seen = true
			if got := start.Partial.Observe().DurationMs; got == nil || *got != *result.DurationMs {
				t.Fatalf("start partial durationMs = %v, want %d", got, *result.DurationMs)
			}
		}
	}
	if !seen {
		t.Fatal("no start event")
	}
}
