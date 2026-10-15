package agent

import (
	"encoding/json"
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

// proxy.ts ProxyAssistantMessageEvent `type` is the closed union of twelve literals; the Go field is the named
// ProxyAssistantMessageEventType whose constants spell exactly those literals, and each one round-trips through the wire codec.
func TestProxyEventTypeIsTheClosedPiUnion(t *testing.T) {
	want := map[ProxyAssistantMessageEventType]string{
		ProxyEventStart: "start", ProxyEventTextStart: "text_start", ProxyEventTextDelta: "text_delta", ProxyEventTextEnd: "text_end",
		ProxyEventThinkingStart: "thinking_start", ProxyEventThinkingDelta: "thinking_delta", ProxyEventThinkingEnd: "thinking_end",
		ProxyEventToolcallStart: "toolcall_start", ProxyEventToolcallDelta: "toolcall_delta", ProxyEventToolcallEnd: "toolcall_end",
		ProxyEventDone: "done", ProxyEventError: "error",
	}
	if len(want) != 12 {
		t.Fatalf("%d constants, want 12", len(want))
	}
	for eventType, literal := range want {
		if string(eventType) != literal {
			t.Errorf("constant %q, want %q", eventType, literal)
		}
		data, err := json.Marshal(ProxyAssistantMessageEvent{Type: eventType})
		if err != nil {
			t.Errorf("%s: %v", literal, err)
			continue
		}
		var back ProxyAssistantMessageEvent
		if err := json.Unmarshal(data, &back); err != nil || back.Type != eventType {
			t.Errorf("%s round trip = %+v (%v) from %s", literal, back, err, data)
		}
	}
}
