package main

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi 0.87.1 packages/agent/src/agent-loop.ts:870-894 keeps isError beside result,
// and on the persisted toolResult message, never injected into result.
func TestRPCToolExecutionResultExactWire(t *testing.T) {
	for _, failed := range []bool{false, true} {
		events, err := rpcAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "call", ToolName: "read", Result: agent.AgentToolResult{Content: "result", IsError: failed}})
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
