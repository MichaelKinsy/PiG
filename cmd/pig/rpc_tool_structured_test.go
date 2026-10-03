package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi 0.99.1 packages/agent/src/agent-loop.ts:912-919 emits tool_execution_end with the finalized AgentToolResult as `result`, and AgentToolResult.structuredContent (agent/src/types.ts:433) is part of it, so the RPC and JSON wire carry it beside content and details.
func TestRPCToolExecutionEndCarriesStructuredContent(t *testing.T) {
	events, err := rpcAgentEvent(agent.ToolExecutionEndEvent{
		ToolCallID: "call", ToolName: "probe",
		Result: agent.AgentToolResult{
			Content:           []ai.ToolResultMessageContent{ai.TextContent{Text: "text"}},
			Details:           map[string]any{"d": 1},
			StructuredContent: json.RawMessage(`{"z":1,"a":[true,null]}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	// Pi writes the result's keys in the order the tool built them (bash.ts:395-403: content, details, structuredContent), and the structured value verbatim.
	want := `{"type":"tool_execution_end","toolCallId":"call","toolName":"probe","result":{"content":[{"type":"text","text":"text"}],"details":{"d":1},"structuredContent":{"z":1,"a":[true,null]}},"isError":false}`
	if string(wire) != want {
		t.Fatalf("wire = %s\nwant   %s", wire, want)
	}
}

// Pi 0.99.1 bash.ts:395-406: a successful bash call returns its BashToolOutput as structuredContent, and the RPC tool_execution_end carries it (rpc-mode.ts:354-363 writes every agent event).
func TestRPCBashToolExecutionEndCarriesItsStructuredOutput(t *testing.T) {
	args := json.RawMessage(`{"command":"printf 42"}`)
	result, err := (&tools.BashTool{CWD: t.TempDir()}).Execute(t.Context(), "call", args, nil)
	if err != nil || result.IsError {
		t.Fatalf("execute: %+v, %v", result, err)
	}
	events, err := rpcAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "call", ToolName: "bash", Result: result})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := decodeRPCEvent(t, events[0])["result"].(map[string]any)
	structured, _ := got["structuredContent"].(map[string]any)
	if structured == nil {
		t.Fatalf("result = %#v, want structuredContent", got)
	}
	if structured["output"] != "42" || structured["truncated"] != false || structured["exit_code"] != float64(0) {
		t.Fatalf("structuredContent = %#v", structured)
	}
	if _, ok := structured["wall_time_seconds"].(float64); !ok || len(structured) != 4 {
		t.Fatalf("structuredContent = %#v, want output, truncated, exit_code and wall_time_seconds", structured)
	}
	if !reflect.DeepEqual(got["content"], []any{map[string]any{"type": "text", "text": "42"}}) {
		t.Fatalf("content = %#v", got["content"])
	}
}

// Pi writes the tool's structuredContent value with JSON.stringify (rpc-mode.ts output → serializeJsonLine), so the wire carries JSON.stringify(JSON.parse(raw)) whatever text produced the value: an extension SDK's escapes (Python json.dumps ensure_ascii), float spellings (Python, Rust 1.0), duplicate keys and integer-like key order. Expected bytes are Node 24's output for the same raw text.
func TestRPCToolExecutionEndWritesStructuredContentAsJSONStringify(t *testing.T) {
	raw := json.RawMessage(`{ "s" : "\u00e9<\u2028", "n":1.0, "e":1E2, "d":1, "d":2, "2":"x", "1":"y", "z":-0 }`)
	events, err := rpcAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "call", ToolName: "probe", Result: agent.AgentToolResult{StructuredContent: raw}})
	if err != nil {
		t.Fatal(err)
	}
	line, err := rpcclient.SerializeJsonLine(events[0])
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"type\":\"tool_execution_end\",\"toolCallId\":\"call\",\"toolName\":\"probe\",\"result\":{\"content\":[],\"structuredContent\":{\"1\":\"y\",\"2\":\"x\",\"s\":\"é<\u2028\",\"n\":1,\"e\":100,\"d\":2,\"z\":0}},\"isError\":false}\n"
	if string(line) != want {
		t.Fatalf("line = %q\nwant   %q", line, want)
	}
}
