package ai

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestAssistantMessageEventJSONMatchesPiVariants(t *testing.T) {
	partial := testAssistant(StopReasonPending)
	toolCall := ToolCall{ID: "call", Name: "read", Arguments: JsonObject{"path": "x.go"}}
	final := testAssistant(StopReasonStop)
	tests := []struct {
		name      string
		event     AssistantMessageEvent
		eventType AssistantEventType
		keys      []string
	}{
		{"start", StartEvent{Partial: partial}, EventStart, []string{"partial", "type"}},
		{"text start", TextStartEvent{ContentIndex: 1, Partial: partial}, EventTextStart, []string{"contentIndex", "partial", "type"}},
		{"text delta", TextDeltaEvent{ContentIndex: 1, Delta: "x", Partial: partial}, EventTextDelta, []string{"contentIndex", "delta", "partial", "type"}},
		{"text end", TextEndEvent{ContentIndex: 1, Content: "x", Partial: partial}, EventTextEnd, []string{"content", "contentIndex", "partial", "type"}},
		{"thinking start", ThinkingStartEvent{ContentIndex: 1, Partial: partial}, EventThinkingStart, []string{"contentIndex", "partial", "type"}},
		{"thinking delta", ThinkingDeltaEvent{ContentIndex: 1, Delta: "x", Partial: partial}, EventThinkingDelta, []string{"contentIndex", "delta", "partial", "type"}},
		{"thinking end", ThinkingEndEvent{ContentIndex: 1, Content: "x", Partial: partial}, EventThinkingEnd, []string{"content", "contentIndex", "partial", "type"}},
		{"tool call start", ToolCallStartEvent{ContentIndex: 1, Partial: partial}, EventToolCallStart, []string{"contentIndex", "partial", "type"}},
		{"tool call delta", ToolCallDeltaEvent{ContentIndex: 1, Delta: "{}", Partial: partial}, EventToolCallDelta, []string{"contentIndex", "delta", "partial", "type"}},
		{"tool call end", ToolCallEndEvent{ContentIndex: 1, ToolCall: toolCall, Partial: partial}, EventToolCallEnd, []string{"contentIndex", "partial", "toolCall", "type"}},
		{"done", DoneEvent{Reason: StopReasonStop, Message: final}, EventDone, []string{"message", "reason", "type"}},
		{"error", ErrorEvent{Reason: StopReasonError, Error: testAssistant(StopReasonError)}, EventError, []string{"error", "reason", "type"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.event)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			var gotKeys []string
			for key := range fields {
				gotKeys = append(gotKeys, key)
			}
			slices.Sort(gotKeys)
			if !slices.Equal(gotKeys, test.keys) {
				t.Fatalf("event keys = %v, want %v: %s", gotKeys, test.keys, encoded)
			}
			var gotType AssistantEventType
			if err := json.Unmarshal(fields["type"], &gotType); err != nil {
				t.Fatal(err)
			}
			if gotType != test.eventType {
				t.Fatalf("event type = %q, want %q", gotType, test.eventType)
			}
		})
	}
}
