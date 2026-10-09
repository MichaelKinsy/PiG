package extension

import (
	"encoding/json"
	"testing"
)

// agent-session.ts:_beforeToolCall builds the tool_call event as {type, toolName, toolCallId, parentToolCallId?, input}, and a subprocess extension receives JSON.stringify of it: `input` holds the members in the order the model wrote them, then the order an earlier handler left them in. Go's struct order would write the embedded base first, and a map would sort `input`.
func TestCustomToolCallEventIsWrittenInPiMemberOrder(t *testing.T) {
	wire := func(raw string) *json.RawMessage {
		message := json.RawMessage(raw)
		return &message
	}
	cases := []struct {
		name  string
		event CustomToolCallEvent
		want  string
	}{
		{
			name:  "model order",
			event: CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call", WireInput: wire(`{"timeout":5,"command":"ls","nested":{"z":1,"a":2}}`)}, ToolName: "bash", Input: map[string]any{"command": "ls", "timeout": 5.0, "nested": map[string]any{"a": 2.0, "z": 1.0}}},
			want:  `{"type":"tool_call","toolName":"bash","toolCallId":"call","input":{"timeout":5,"command":"ls","nested":{"z":1,"a":2}}}`,
		},
		{
			name:  "parent call id",
			event: CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call/1", ParentToolCallID: "call", WireInput: wire(`{"b":1,"a":2}`)}, ToolName: "t", Input: map[string]any{"a": 2.0, "b": 1.0}},
			want:  `{"type":"tool_call","toolName":"t","toolCallId":"call/1","parentToolCallId":"call","input":{"b":1,"a":2}}`,
		},
		{
			name:  "no model order is sorted",
			event: CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call"}, ToolName: "t", Input: map[string]any{"b": 1.0, "a": 2.0}},
			want:  `{"type":"tool_call","toolName":"t","toolCallId":"call","input":{"a":2,"b":1}}`,
		},
		{
			// An in-process handler edited the map after the wire bytes were taken: retained members keep their place, new members follow sorted, a removed member is gone, and the value is the map's.
			name:  "map edited after the wire bytes",
			event: CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call", WireInput: wire(`{"timeout":5,"command":"ls","drop":1}`)}, ToolName: "bash", Input: map[string]any{"command": "pwd", "timeout": 5.0, "zeta": true, "alpha": 1.0}},
			want:  `{"type":"tool_call","toolName":"bash","toolCallId":"call","input":{"timeout":5,"command":"pwd","alpha":1,"zeta":true}}`,
		},
		{
			// A model that wrote sorted members is no different: Pi appends a new member whatever it sorts as.
			name:  "map edited after sorted wire bytes",
			event: CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call", WireInput: wire(`{"command":"ls","timeout":5}`)}, ToolName: "bash", Input: map[string]any{"command": "pwd", "timeout": 5.0, "aaa": 1.0}},
			want:  `{"type":"tool_call","toolName":"bash","toolCallId":"call","input":{"command":"pwd","timeout":5,"aaa":1}}`,
		},
		{
			name:  "invalid wire bytes fall back to the map",
			event: CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call", WireInput: wire(`{"b":`)}, ToolName: "t", Input: map[string]any{"b": 1.0, "a": 2.0}},
			want:  `{"type":"tool_call","toolName":"t","toolCallId":"call","input":{"a":2,"b":1}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.want {
				t.Errorf("event =\n%s\nwant\n%s", encoded, tc.want)
			}
		})
	}
}

// The wire form is a way to write the event, not part of its data: a decoded event carries no wire bytes.
func TestCustomToolCallEventRoundTripKeepsNoWireInput(t *testing.T) {
	var custom CustomToolCallEvent
	if err := json.Unmarshal([]byte(`{"type":"tool_call","toolName":"t","toolCallId":"c","input":{"b":1,"a":2}}`), &custom); err != nil {
		t.Fatal(err)
	}
	if custom.WireInput != nil || custom.ToolCallID != "c" || len(custom.Input) != 2 {
		t.Fatalf("decoded = %#v", custom)
	}
}

// InputJSON, the arguments the tool runs with, is the `input` the event is written with: the members in WireInput's order, a member it does not hold (one an in-process handler added) after them in sorted order, and a member the map lost left out.
func TestCustomToolCallEventInputJSONIsTheInputItWrites(t *testing.T) {
	wire := func(text string) *json.RawMessage {
		raw := json.RawMessage(text)
		return &raw
	}
	for name, tc := range map[string]struct {
		event CustomToolCallEvent
		want  string
	}{
		"wire order": {
			CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call", WireInput: wire(`{"command":"ls","aaa":1,"nested":{"y":2,"b":3}}`)}, ToolName: "bash", Input: map[string]any{"command": "ls", "aaa": 1.0, "nested": map[string]any{"y": 2.0, "b": 3.0}}},
			`{"type":"tool_call","toolName":"bash","toolCallId":"call","input":{"command":"ls","aaa":1,"nested":{"y":2,"b":3}}}`,
		},
		"parent": {
			CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "parent/1", ParentToolCallID: "parent", WireInput: wire(`{"z":true,"a":false}`)}, ToolName: "bash", Input: map[string]any{"z": true, "a": false}},
			`{"type":"tool_call","toolName":"bash","toolCallId":"parent/1","parentToolCallId":"parent","input":{"z":true,"a":false}}`,
		},
		"in-process edit": {
			CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call", WireInput: wire(`{"command":"ls","zz":1,"dropped":2}`)}, ToolName: "bash", Input: map[string]any{"command": "rewritten", "zz": true, "bb": false}},
			`{"type":"tool_call","toolName":"bash","toolCallId":"call","input":{"command":"rewritten","zz":true,"bb":false}}`,
		},
		"no wire": {
			CustomToolCallEvent{ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "call"}, ToolName: "probe", Input: map[string]any{"b": 1.0, "a": "x"}},
			`{"type":"tool_call","toolName":"probe","toolCallId":"call","input":{"a":"x","b":1}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.want {
				t.Errorf("event =\n%s\nwant\n%s", encoded, tc.want)
			}
			if input, err := tc.event.InputJSON(); err != nil {
				t.Fatal(err)
			} else {
				var event struct {
					Input json.RawMessage `json:"input"`
				}
				if err := json.Unmarshal([]byte(tc.want), &event); err != nil {
					t.Fatal(err)
				}
				if string(input) != string(event.Input) {
					t.Errorf("InputJSON = %s, want %s", input, event.Input)
				}
			}
		})
	}
}
