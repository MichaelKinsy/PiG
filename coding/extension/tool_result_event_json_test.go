package extension

import (
	"encoding/json"
	"testing"
)

// agent-session.ts:649-660 builds the tool_result event as {type, toolName, toolCallId, parentToolCallId?, input, content, details, structuredContent?, isError, usage}, and a subprocess extension receives JSON.stringify of it: `input` in the order the model wrote the arguments, each content block as {type, text} or {type, data, mimeType}, `details` as the tool wrote it. Every built-in variant and the generic one are written the same way.
func TestToolResultEventsAreWrittenInPiMemberOrder(t *testing.T) {
	base := ToolResultEventBase{
		Type: "tool_result", ToolCallID: "call", ParentToolCallID: "parent",
		Input:             map[string]any{"b": 1.0, "a": 2.0},
		WireInput:         json.RawMessage(`{"zz":1,"aa":{"y":2,"b":3}}`),
		Content:           []any{map[string]any{"text": "a", "type": "text", "textSignature": "s"}, map[string]any{"mimeType": "image/png", "data": "aW1n", "type": "image"}},
		StructuredContent: json.RawMessage(`{"q":1,"a":2}`),
		IsError:           true,
		Usage:             map[string]any{"output": 7.0},
	}
	const common = `"toolCallId":"call","parentToolCallId":"parent","input":{"zz":1,"aa":{"y":2,"b":3}},"content":[{"type":"text","text":"a","textSignature":"s"},{"type":"image","data":"aW1n","mimeType":"image/png"}]`
	const tail = `"structuredContent":{"q":1,"a":2},"isError":true,"usage":{"output":7}}`
	for name, tc := range map[string]struct {
		event ToolResultEvent
		want  string
	}{
		"custom":     {CustomToolResultEvent{ToolResultEventBase: base, ToolName: "probe", Details: json.RawMessage(`{"zeta":1,"alpha":2}`)}, `{"type":"tool_result","toolName":"probe",` + common + `,"details":{"zeta":1,"alpha":2},` + tail},
		"bash":       {BashToolResultEvent{ToolResultEventBase: base, ToolName: "bash", Details: &BashToolDetails{FullOutputPath: "/tmp/o"}}, `{"type":"tool_result","toolName":"bash",` + common + `,"details":{"fullOutputPath":"/tmp/o"},` + tail},
		"read":       {ReadToolResultEvent{ToolResultEventBase: base, ToolName: "read"}, `{"type":"tool_result","toolName":"read",` + common + `,` + tail},
		"write":      {WriteToolResultEvent{ToolResultEventBase: base, ToolName: "write"}, `{"type":"tool_result","toolName":"write",` + common + `,` + tail},
		"edit":       {EditToolResultEvent{ToolResultEventBase: base, ToolName: "edit", Details: &EditToolDetails{Diff: "d"}}, `{"type":"tool_result","toolName":"edit",` + common + `,"details":{"diff":"d","patch":""},` + tail},
		"grep":       {GrepToolResultEvent{ToolResultEventBase: base, ToolName: "grep"}, `{"type":"tool_result","toolName":"grep",` + common + `,` + tail},
		"find":       {FindToolResultEvent{ToolResultEventBase: base, ToolName: "find"}, `{"type":"tool_result","toolName":"find",` + common + `,` + tail},
		"ls":         {LsToolResultEvent{ToolResultEventBase: base, ToolName: "ls"}, `{"type":"tool_result","toolName":"ls",` + common + `,` + tail},
		"powershell": {PowerShellToolResultEvent{ToolResultEventBase: base, ToolName: "powershell"}, `{"type":"tool_result","toolName":"powershell",` + common + `,` + tail},
	} {
		t.Run(name, func(t *testing.T) {
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

// Without the model's raw arguments the input is the map, sorted; an absent optional member is left out as undefined is.
func TestToolResultEventWithoutRawInputOmitsAbsentMembers(t *testing.T) {
	encoded, err := json.Marshal(CustomToolResultEvent{ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "c", Input: map[string]any{"b": 1.0, "a": 2.0}}, ToolName: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"tool_result","toolName":"t","toolCallId":"c","input":{"a":2,"b":1},"content":[],"isError":false}`; string(encoded) != want {
		t.Fatalf("event = %s, want %s", encoded, want)
	}
}
