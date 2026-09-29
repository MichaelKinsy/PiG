package ai

import (
	"encoding/json"
	"testing"
)

// Pi providers construct type first, followed by the event payload; json-event.ts removes partial without reordering the retained fields.
func TestAssistantEventFieldOrder(t *testing.T) {
	cases := []struct {
		event AssistantMessageEvent
		want  string
	}{
		{StartEvent{}, `{"type":"start","partial":null}`},
		{TextStartEvent{}, `{"type":"text_start","contentIndex":0,"partial":null}`},
		{TextDeltaEvent{Delta: "x"}, `{"type":"text_delta","contentIndex":0,"delta":"x","partial":null}`},
		{TextEndEvent{Content: "x"}, `{"type":"text_end","contentIndex":0,"content":"x","partial":null}`},
		{ThinkingStartEvent{}, `{"type":"thinking_start","contentIndex":0,"partial":null}`},
		{ThinkingDeltaEvent{Delta: "x"}, `{"type":"thinking_delta","contentIndex":0,"delta":"x","partial":null}`},
		{ThinkingEndEvent{Content: "x"}, `{"type":"thinking_end","contentIndex":0,"content":"x","partial":null}`},
		{ToolCallStartEvent{}, `{"type":"toolcall_start","contentIndex":0,"partial":null}`},
		{ToolCallDeltaEvent{Delta: "{}"}, `{"type":"toolcall_delta","contentIndex":0,"delta":"{}","partial":null}`},
		{ToolCallEndEvent{ToolCall: ToolCall{ID: "c", Name: "tool", Arguments: JsonObject{}}}, `{"type":"toolcall_end","contentIndex":0,"toolCall":{"type":"toolCall","id":"c","name":"tool","arguments":{}},"partial":null}`},
		{DoneEvent{Reason: StopReasonStop}, `{"type":"done","reason":"stop","message":null}`},
		{ErrorEvent{Reason: StopReasonError}, `{"type":"error","reason":"error","error":null}`},
	}
	for _, test := range cases {
		t.Run(string(test.event.EventType()), func(t *testing.T) {
			got, err := json.Marshal(test.event)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}
