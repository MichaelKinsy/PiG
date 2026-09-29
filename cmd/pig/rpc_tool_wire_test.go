package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi 0.87.1 coding-agent/src/core/tools/bash.ts:301-302 emits content:[] before execution.
// Later output updates use text blocks (bash.ts:270-276); read.ts:185-188 retains an explicit empty text result.
func TestRPCShellInitialUpdateHasEmptyContent(t *testing.T) {
	for _, command := range []string{"true", "printf hello", `printf '\357\273\277'`} {
		t.Run(command, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"command": command})
			if err != nil {
				t.Fatal(err)
			}
			var updates []any
			result, err := (&tools.BashTool{CWD: t.TempDir()}).Execute(t.Context(), "call", args, func(content string, details any) {
				events, err := rpcAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "call", ToolName: "bash", Content: content, Details: details, Args: args})
				if err != nil {
					t.Error(err)
					return
				}
				updates = append(updates, decodeRPCEvent(t, events[0]))
			})
			if err != nil || result.IsError {
				t.Fatalf("execute: %+v, %v", result, err)
			}
			want := map[string]any{
				"type": "tool_execution_update", "toolCallId": "call", "toolName": "bash",
				"args": map[string]any{"command": command}, "partialResult": map[string]any{"content": []any{}},
			}
			if len(updates) == 0 || !reflect.DeepEqual(updates[0], want) {
				t.Fatalf("initial update = %#v, want %#v", updates, want)
			}
			if command != "true" {
				text := "hello"
				if command != "printf hello" {
					text = "" // A BOM-only chunk decodes to an explicit empty output update, not the initial snapshot.
				}
				want["partialResult"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "details": map[string]any{}}
				if len(updates) < 2 || !reflect.DeepEqual(updates[len(updates)-1], want) {
					t.Fatalf("output update = %#v, want %#v", updates, want)
				}
			}
		})
	}
	// An empty completed read is text, not an empty partial snapshot.
	events, err := rpcAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "read", ToolName: "read", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"content": []any{map[string]any{"type": "text", "text": ""}}}
	if got := decodeRPCEvent(t, events[0])["result"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty read result = %#v, want %#v", got, want)
	}
}

// Pi 0.87.1 packages/agent/src/agent-loop.ts:870-894 keeps isError beside result,
// and on the persisted toolResult message, never injected into result.
func TestRPCToolExecutionResultExactWire(t *testing.T) {
	for _, failed := range []bool{false, true} {
		events, err := rpcAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "call", ToolName: "read", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result"}}, IsError: failed}})
		if err != nil {
			t.Fatal(err)
		}
		got := decodeRPCEvent(t, events[0])
		want := map[string]any{"type": "tool_execution_end", "toolCallId": "call", "toolName": "read", "isError": failed, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "result"}}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("event = %#v, want %#v", got, want)
		}
		message, err := rpcToolResultMessage(agent.ToolResultMessage{ToolCallID: "call", ToolName: "read", IsError: failed})
		if err != nil {
			t.Fatal(err)
		}
		if decodeRPCEvent(t, message)["isError"] != failed {
			t.Fatal("persisted toolResult lost isError")
		}
	}
	events, err := rpcAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "call", ToolName: "bash", Content: "partial", Args: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	partial := decodeRPCEvent(t, events[0])["partialResult"]
	want := map[string]any{"content": []any{map[string]any{"type": "text", "text": "partial"}}}
	if !reflect.DeepEqual(partial, want) {
		t.Fatalf("partialResult = %#v, want %#v", partial, want)
	}
}
