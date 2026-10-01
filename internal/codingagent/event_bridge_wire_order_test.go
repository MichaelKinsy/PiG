package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type wireOrderDetails struct {
	Zeta  string `json:"zeta"`
	Alpha int    `json:"alpha"`
}

// A subprocess extension receives json.Marshal of the event the runner dispatches (host.go makeEventHandler). agent-loop.ts:541-547,779-786,912-919 hands Pi's handlers the model's parsed arguments and the tool's own AgentToolResult, so JSON.stringify(event.args) and JSON.stringify(event.result) keep the order the model and the tool wrote: nothing sorts them.
func TestToolExecutionEventsKeepInsertionOrderOnTheWire(t *testing.T) {
	got := map[string]any{}
	handlers := map[string][]extension.HandlerFn{}
	for _, name := range []string{EventToolExecutionStart, EventToolExecutionUpdate, EventToolExecutionEnd} {
		handlers[name] = []extension.HandlerFn{func(args ...any) (any, error) {
			got[name] = args[0]
			return nil, nil
		}}
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/tmp/order.ts", Handlers: handlers}}, ".")
	args := json.RawMessage(`{"z":1,"a":{"y":[{"q":1,"b":2}],"b":null},"m":"x"}`)
	result := agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{
			ai.TextContent{Text: "signed", TextSignature: "sig"},
			ai.ImageContent{Data: "aW1n", MimeType: "image/png"},
		},
		Details:           wireOrderDetails{Zeta: "z", Alpha: 1},
		StructuredContent: json.RawMessage(`{"z":1,"a":2}`),
		IsError:           true,
		Terminate:         true,
	}
	for _, event := range []agent.AgentEvent{
		agent.ToolExecutionStartEvent{ToolCallID: "c", ToolName: "t", Args: args},
		agent.ToolExecutionUpdateEvent{ToolCallID: "c", ToolName: "t", Args: args, PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: wireOrderDetails{Zeta: "z", Alpha: 1}}},
		agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: "t", Result: result, IsError: true},
	} {
		DispatchAgentLoopEvent(runner, event, nil)
	}
	for name, want := range map[string]string{
		EventToolExecutionStart:  `{"type":"tool_execution_start","toolCallId":"c","toolName":"t","args":{"z":1,"a":{"y":[{"q":1,"b":2}],"b":null},"m":"x"}}`,
		EventToolExecutionUpdate: `{"type":"tool_execution_update","toolCallId":"c","toolName":"t","args":{"z":1,"a":{"y":[{"q":1,"b":2}],"b":null},"m":"x"},"partialResult":{"content":[{"type":"text","text":"partial"}],"details":{"zeta":"z","alpha":1}}}`,
		EventToolExecutionEnd:    `{"type":"tool_execution_end","toolCallId":"c","toolName":"t","result":{"content":[{"type":"text","text":"signed","textSignature":"sig"},{"type":"image","data":"aW1n","mimeType":"image/png"}],"details":{"zeta":"z","alpha":1},"structuredContent":{"z":1,"a":2},"isError":true,"terminate":true},"isError":true}`,
	} {
		data, err := json.Marshal(got[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Errorf("%s wire =\n%s\nwant\n%s", name, data, want)
		}
	}
}

// Pi emits the partial result exactly as the tool passed it to onUpdate (agent-loop.ts:778-786): an AgentToolResult whose `content` is an array of blocks. Bash's first update has no blocks and no details (bash.ts:321-323), its output snapshots one text block and details (bash.ts:270-290), codemode no blocks and details (codemode/execute.ts:235), and an extension tool any blocks, images included. An extension handler sees that object in both the in-process and the subprocess representation.
func TestToolExecutionUpdatePartialResultIsTheObjectTheToolPassed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		partial agent.AgentToolResult
		wire    string
		process map[string]any
	}{
		{"shell startup", agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}}, `{"content":[]}`, map[string]any{"content": []any{}}},
		{"output snapshot", agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "42\n"}}, Details: map[string]any{}}, `{"content":[{"type":"text","text":"42\n"}],"details":{}}`, map[string]any{"content": []any{map[string]any{"type": "text", "text": "42\n"}}, "details": map[string]any{}}},
		{"empty text block", agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}, Details: map[string]any{}}, `{"content":[{"type":"text","text":""}],"details":{}}`, map[string]any{"content": []any{map[string]any{"type": "text", "text": ""}}, "details": map[string]any{}}},
		{"no blocks with details", agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: json.RawMessage(`{"calls":[]}`)}, `{"content":[],"details":{"calls":[]}}`, map[string]any{"content": []any{}, "details": json.RawMessage(`{"calls":[]}`)}},
		{"several blocks", agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}, ai.ImageContent{Data: "aW1n", MimeType: "image/png"}, ai.TextContent{Text: "b"}}}, `{"content":[{"type":"text","text":"a"},{"type":"image","data":"aW1n","mimeType":"image/png"},{"type":"text","text":"b"}]}`, map[string]any{"content": []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "image", "data": "aW1n", "mimeType": "image/png"}, map[string]any{"type": "text", "text": "b"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got extension.ToolExecutionUpdateEvent
			runner := inproc.NewRunner([]extension.Extension{{Path: "/tmp/partial.ts", Handlers: map[string][]extension.HandlerFn{
				EventToolExecutionUpdate: {func(args ...any) (any, error) { got = args[0].(extension.ToolExecutionUpdateEvent); return nil, nil }},
			}}}, ".")
			DispatchAgentLoopEvent(runner, agent.ToolExecutionUpdateEvent{ToolCallID: "c", ToolName: "t", Args: json.RawMessage(`{}`), PartialResult: tc.partial}, nil)
			if !reflect.DeepEqual(got.PartialResult, tc.process) {
				t.Errorf("in-process partialResult = %#v, want %#v", got.PartialResult, tc.process)
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"type":"tool_execution_update","toolCallId":"c","toolName":"t","args":{},"partialResult":` + tc.wire + `}`
			if string(data) != want {
				t.Errorf("wire =\n%s\nwant\n%s", data, want)
			}
		})
	}
}

// A tool's result is the object the tool built, so a subprocess extension's tool_execution_update and tool_execution_end handlers receive its members in the order the tool wrote them (agent-loop.ts:778-786, 912-919; probed with real Pi 0.99.1: a tool returning {details, isError, content} is observed as {"details":...,"isError":true,"content":[...]}). A result without a recorded order keeps the declared one.
func TestToolExecutionEventsKeepTheToolsMemberOrderOnTheWire(t *testing.T) {
	got := map[string]any{}
	handlers := map[string][]extension.HandlerFn{}
	for _, name := range []string{EventToolExecutionUpdate, EventToolExecutionEnd} {
		handlers[name] = []extension.HandlerFn{func(args ...any) (any, error) {
			got[name] = args[0]
			return nil, nil
		}}
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/tmp/tool-order.ts", Handlers: handlers}}, ".")
	text := []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}
	for _, event := range []agent.AgentEvent{
		agent.ToolExecutionUpdateEvent{ToolCallID: "c", ToolName: "t", PartialResult: agent.AgentToolResult{MemberOrder: []string{"details", "content"}, Content: text, Details: json.RawMessage(`{"k":1}`)}},
		agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: "t", IsError: true, Result: agent.AgentToolResult{MemberOrder: []string{"details", "isError", "content"}, Content: text, Details: json.RawMessage(`{"k":1}`), IsError: true}},
	} {
		DispatchAgentLoopEvent(runner, event, nil)
	}
	for name, want := range map[string]string{
		EventToolExecutionUpdate: `{"type":"tool_execution_update","toolCallId":"c","toolName":"t","args":null,"partialResult":{"details":{"k":1},"content":[{"type":"text","text":"x"}]}}`,
		EventToolExecutionEnd:    `{"type":"tool_execution_end","toolCallId":"c","toolName":"t","result":{"details":{"k":1},"isError":true,"content":[{"type":"text","text":"x"}]},"isError":true}`,
	} {
		data, err := json.Marshal(got[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Errorf("%s wire =\n%s\nwant\n%s", name, data, want)
		}
	}
}
