package subprocess

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// A tool's result object reaches the host as the members the SDK wrote, and Pi hands the tool's own object on (agent-loop.ts:778-786, 912-919), so the host keeps the order: every SDK writes the object with its members in the author's order (a Node object, a Python dict, a Rust JSON object), and the host names them as Pi does (is_error is isError). A member that is not an AgentToolResult member (preview, an unknown key) has no place in the object Pi writes.
func TestToolResultRecordsTheWireObjectsMemberOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
		want []string
	}{
		{"author's order", `{"details":{"k":1},"is_error":true,"content":[{"type":"text","text":"x"}]}`, []string{"details", "isError", "content"}},
		{"every member", `{"usage":{"input":1},"terminate":true,"structured_content":{"a":1},"is_error":false,"details":null,"content":"x"}`, []string{"usage", "terminate", "structuredContent", "isError", "details", "content"}},
		{"preview and unknown members are not members", `{"preview":"p","weird":1,"content":"x","details":2}`, []string{"content", "details"}},
		{"a repeated key keeps its first position", `{"content":"a","details":1,"content":"b"}`, []string{"content", "details"}},
		{"a text result has only its content", `{"content":"x"}`, []string{"content"}},
		{"an empty object has none", `{}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result ToolResult
			if err := json.Unmarshal([]byte(tc.wire), &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.MemberOrder, tc.want) {
				t.Fatalf("MemberOrder = %v, want %v", result.MemberOrder, tc.want)
			}
		})
	}
}

// A nested call's outcome carries the called tool's own object too (nested-tool-calls.ts:220-245, agent-loop.ts:810-818 runToolCall), so ctx.executeTool's result and its partial results keep the order the called tool wrote, with the members a hook added after them, as tool_execution_end does.
func TestNestedToolResultWireKeepsTheToolsMemberOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result agent.AgentToolResult
		want   string
	}{
		{"the tool's order", agent.AgentToolResult{MemberOrder: []string{"details", "isError", "content"}, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}, Details: map[string]any{"k": 1}, IsError: true},
			`{"details":{"k":1},"isError":true,"content":[{"type":"text","text":"x"}]}`},
		{"declared order without a recorded one", agent.AgentToolResult{Details: map[string]any{}, Terminate: true},
			`{"content":[],"details":{},"terminate":true}`},
		{"a hook's members follow the tool's", agent.AgentToolResult{MemberOrder: []string{"content"}, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}, Details: "d", StructuredContent: json.RawMessage(`{"s":1}`)},
			`{"content":[{"type":"text","text":"x"}],"details":"d","structuredContent":{"s":1}}`},
		{"members the tool wrote as null or false", agent.AgentToolResult{MemberOrder: []string{"content", "isError", "details", "terminate", "usage"}},
			`{"content":[],"isError":false,"details":null,"terminate":false,"usage":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(agentToolResultWire(tc.result)); got != tc.want {
				t.Fatalf("agentToolResultWire = %s, want %s", got, tc.want)
			}
		})
	}
}
