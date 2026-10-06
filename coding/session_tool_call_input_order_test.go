package coding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi hands the tool_call handlers the model's arguments object and runs the tool with it (agent-loop.ts prepareToolCall, agent-session.ts:_beforeToolCall), so the members reach the handlers, the tool and the tool_result event in the order the model wrote them, and an edit keeps a retained member in its place and appends a new one. A Go map sorts its keys, so the Session carries the order beside the map.
func TestToolCallHookKeepsMemberOrder(t *testing.T) {
	const modelArgs = `{"timeout":5,"command":"git status","nested":{"z":1,"a":2}}`
	cases := []struct {
		name     string
		edit     func(extension.CustomToolCallEvent)
		wantArgs string // empty: the call keeps the model's arguments
	}{
		{name: "no edit", wantArgs: ""},
		{
			name: "map edit by an in-process handler",
			edit: func(event extension.CustomToolCallEvent) {
				event.Input["command"] = "echo rewritten"
				event.Input["aaa"] = 1.0
			},
			wantArgs: `{"timeout":5,"command":"echo rewritten","nested":{"z":1,"a":2},"aaa":1}`,
		},
		{
			name: "removed member",
			edit: func(event extension.CustomToolCallEvent) {
				delete(event.Input, "timeout")
			},
			wantArgs: `{"command":"git status","nested":{"z":1,"a":2}}`,
		},
		{
			// A subprocess extension's reply replaces the map's members and the bytes the event points at, as applyToolCallInput does.
			name: "wire edit by a subprocess handler",
			edit: func(event extension.CustomToolCallEvent) {
				for key := range event.Input {
					delete(event.Input, key)
				}
				event.Input["command"] = "echo rewritten"
				event.Input["aaa"] = 1.0
				*event.WireInput = json.RawMessage(`{"command":"echo rewritten","aaa":1}`)
			},
			wantArgs: `{"command":"echo rewritten","aaa":1}`,
		},
		{
			// The same values in another order are an edit in Pi: the tool receives the object as the handler left it.
			name: "reorder only",
			edit: func(event extension.CustomToolCallEvent) {
				*event.WireInput = json.RawMessage(`{"command":"git status","timeout":5,"nested":{"z":1,"a":2}}`)
			},
			wantArgs: `{"command":"git status","timeout":5,"nested":{"z":1,"a":2}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var received string
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"tool_call": {func(args ...any) (any, error) {
				event := args[0].(extension.CustomToolCallEvent)
				encoded, err := json.Marshal(event)
				if err != nil {
					return nil, err
				}
				received = string(encoded)
				if tc.edit != nil {
					tc.edit(event)
				}
				return nil, nil
			}}}}
			session, _ := newOrchestrationSession(t, ext)
			hook := session.extensionToolCallHook(t.Context(), "call", "bash", json.RawMessage(modelArgs))
			if want := `{"type":"tool_call","toolName":"bash","toolCallId":"call","input":` + modelArgs + `}`; received != want {
				t.Errorf("handler received\n%s\nwant\n%s", received, want)
			}
			if string(hook.Args) != tc.wantArgs {
				t.Errorf("call arguments after the handler = %s, want %s", hook.Args, tc.wantArgs)
			}
		})
	}
}

// The issue's probe through a whole Session: the model calls a tool, a tool_call handler sets `command` and adds `aaa`, and the tool and the tool_result handler both see Pi's `{"command":...,"timeout":5,"aaa":1}`, where a sorted map gave `{"aaa":1,"command":...}`. The assistant message in the transcript, and so the session file, keeps the arguments the model wrote.
func TestSessionRunsAnEditedToolCallInInsertionOrder(t *testing.T) {
	var ran, resultEvent string
	probe := orchestrationTool("probe", "Records its arguments.", extension.ToolDefinition{
		Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"},"aaa":{"type":"number"}}}`),
		Execute: func(_ context.Context, _ string, params json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			ran = string(params)
			return textResult("ran"), nil
		},
	})
	ext := extension.Extension{
		Tools:     map[string]extension.RegisteredTool{"probe": probe},
		ToolOrder: []string{"probe"},
		Handlers: map[string][]extension.HandlerFn{
			"tool_call": {func(args ...any) (any, error) {
				event := args[0].(extension.CustomToolCallEvent)
				event.Input["command"] = "echo rewritten"
				event.Input["aaa"] = 1.0
				return nil, nil
			}},
			"tool_result": {func(args ...any) (any, error) {
				encoded, err := json.Marshal(args[0])
				resultEvent = string(encoded)
				return nil, err
			}},
		},
	}
	session, faux := newOrchestrationSession(t, ext)
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("probe", map[string]any{"command": "echo base", "timeout": 5.0}, "call-1")}, StopReason: "toolUse"}),
		fauxText("done"),
	})
	if _, err := session.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	const edited = `{"command":"echo rewritten","timeout":5,"aaa":1}`
	if ran != edited {
		t.Errorf("the tool ran with %s, want %s", ran, edited)
	}
	if want := `{"type":"tool_result","toolName":"probe","toolCallId":"call-1","input":` + edited + `,"content":[{"type":"text","text":"ran"}],"details":{},"isError":false}`; resultEvent != want {
		t.Errorf("tool_result event =\n%s\nwant\n%s", resultEvent, want)
	}
	for _, message := range session.Messages() {
		if message.Assistant == nil {
			continue
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(encoded); strings.Contains(got, "rewritten") || strings.Contains(got, `"aaa"`) {
			t.Errorf("the transcript kept the edit: %s", got)
		}
	}
}
