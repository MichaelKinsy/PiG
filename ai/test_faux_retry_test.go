package ai

import "testing"

// test/parity/testdata/test-faux-provider.ts:527-538 (the Pi twin of this provider) sets stopReason "error" and errorMessage on its output, then pushes `start` and `error` for the first "Trigger: retryable provider error" request. Pi's agent loop copies that start partial into message_start (packages/agent/src/agent-loop.ts:416-421) and records thinkingLevel only on the final result (:409), so the RPC message_start carries no thinkingLevel. Without the start event pig's agent emits message_start from the final message (agent.go consumeStream !started path), which carries thinkingLevel.
func TestTestFauxRetryableErrorStartsWithTheErrorState(t *testing.T) {
	testFauxRetryState.Delete("retryable-provider-error")
	t.Cleanup(func() { testFauxRetryState.Delete("retryable-provider-error") })
	p := &TestFauxProvider{}
	stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Trigger: retryable provider error")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
	}
	if len(events) != 2 {
		t.Fatalf("events=%#v, want start then error", events)
	}
	start, ok := events[0].(StartEvent)
	if !ok {
		t.Fatalf("first event = %#v, want start", events[0])
	}
	if got := start.Partial.Observe(); got.StopReason != StopReasonError || got.ErrorMessage != "please retry your request" {
		t.Fatalf("start partial stopReason=%q errorMessage=%q, want error / please retry your request", got.StopReason, got.ErrorMessage)
	}
	failure, ok := events[1].(ErrorEvent)
	if !ok || failure.Error.StopReason != StopReasonError || failure.Error.ErrorMessage != "please retry your request" {
		t.Fatalf("second event = %#v, want the error", events[1])
	}
}
