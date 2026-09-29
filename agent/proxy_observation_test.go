package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/src/proxy.ts:343-398 reconstructs one live partial while tool JSON advances. The proxy reader does not yet run under the JavaScript-order executor, so each delivered partial is its emission-time snapshot (D82) and stays unchanged after later pushes.
func TestProxyPartialObservationsAreEmissionSnapshots(t *testing.T) {
	converter := &proxyEventConverter{partial: newProxyPartial(proxyTestModel()), toolJSON: map[int]string{}}
	stream := ai.NewAssistantMessageEventStream()
	push := func(event ProxyAssistantMessageEvent) {
		t.Helper()
		converted, err := converter.process(event)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Push(converted); err != nil {
			t.Fatal(err)
		}
	}
	push(ProxyAssistantMessageEvent{Type: "start"})
	push(ProxyAssistantMessageEvent{Type: "toolcall_start", ContentIndex: 0, ID: "call", ToolName: "lookup"})
	push(ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: `{"value":"hel`})
	var retained, snapshot *ai.AssistantMessage
	for event := range stream.Events(t.Context()) {
		if delta, ok := event.(ai.ToolCallDeltaEvent); ok {
			retained = delta.Partial
			snapshot = retained.Observe()
			break
		}
	}
	if got := snapshot.Content[0].(ai.ToolCall).Arguments["value"]; got != "hel" {
		t.Fatalf("delta observation = %v, want hel", got)
	}
	push(ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: `lo"}`})
	push(ProxyAssistantMessageEvent{Type: "done", Reason: ai.StopReasonToolUse})
	stream.Result()
	if got := retained.Observe().Content[0].(ai.ToolCall).Arguments["value"]; got != "hel" {
		t.Fatalf("retained emission snapshot = %v, want hel", got)
	}
	if got := stream.Result().Content[0].(ai.ToolCall).Arguments["value"]; got != "hello" {
		t.Fatalf("result tool argument = %v, want hello", got)
	}
	if got := snapshot.Content[0].(ai.ToolCall).Arguments["value"]; got != "hel" {
		t.Fatalf("owned delta observation changed: %v", got)
	}
}
