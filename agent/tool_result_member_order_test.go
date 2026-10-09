package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/src/agent-loop.ts:778-786, 877-889, 912-919. Every expectation was checked with the real Pi 0.99.1 CLI (an extension tool returning {isError, content}, a tool_result handler returning {details}, printing tool_execution_end's result: {"isError":true,"content":[...],"details":{...}}).
//
// A tool's result is the object the tool built, and a hook's result is spread over it: each key keeps its position, and a key the tool did not set follows the tool's own, in the order of the spread ({...result, content, details, usage, terminate}, then structuredContent assigned or deleted).
func TestAgentToolResultMemberNames(t *testing.T) {
	text := []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}
	structured := json.RawMessage(`{"s":1}`)
	usage := &ai.Usage{}
	cases := []struct {
		name   string
		result AgentToolResult
		want   []string
	}{
		{"declared order without a recorded one", AgentToolResult{Content: text, Details: 1, StructuredContent: structured, IsError: true, Usage: usage, Terminate: true},
			[]string{"content", "details", "structuredContent", "isError", "usage", "terminate"}},
		{"declared order puts a structuredContent a hook added last", AgentToolResult{Content: text, Details: 1, StructuredContent: structured, IsError: true, StructuredContentAppended: true},
			[]string{"content", "details", "isError", "structuredContent"}},
		{"the tool's order", AgentToolResult{MemberOrder: []string{"details", "isError", "content"}, Content: text, Details: 1, IsError: true},
			[]string{"details", "isError", "content"}},
		{"a member the tool wrote is held even when its value is null or false", AgentToolResult{MemberOrder: []string{"usage", "details", "isError", "content", "terminate"}, Content: text},
			[]string{"usage", "details", "isError", "content", "terminate"}},
		{"zero members the tool did not write are left out", AgentToolResult{MemberOrder: []string{"content"}, Content: text},
			[]string{"content"}},
		{"members a hook added follow the tool's, in the spread's order", AgentToolResult{MemberOrder: []string{"isError", "content"}, Content: text, Details: 1, IsError: true, Usage: usage, Terminate: true, StructuredContent: structured},
			[]string{"isError", "content", "details", "usage", "terminate", "structuredContent"}},
		{"the content is always written", AgentToolResult{MemberOrder: []string{"details"}, Details: 1},
			[]string{"details", "content"}},
		{"a repeated name counts once", AgentToolResult{MemberOrder: []string{"details", "content", "details"}, Content: text, Details: 1},
			[]string{"details", "content"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.MemberNames(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("MemberNames() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The agent hands the tool's object on through tool_execution_update and, after the hooks' spread, tool_execution_end (agent-loop.ts:778-786, 912-919).
func TestAgentLoop_ToolEventsCarryTheToolsMemberOrder(t *testing.T) {
	tool := iserrorTool("echo", func(_ context.Context, _ string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
		onUpdate(AgentToolResult{MemberOrder: []string{"details", "content"}, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "p"}}, Details: 1})
		return AgentToolResult{MemberOrder: []string{"isError", "content"}, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}, IsError: true}, nil
	})
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "echo", ai.JsonObject{"value": "x"}))}
	rec := newEventRecorder(nil)
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider), Tools: []AgentTool{tool}, EventCh: rec.ch,
		AfterToolCallHooks: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			return AfterToolCallResult{Details: "added"}
		}},
	})

	mustSend(t, a, "go")

	events := rec.stop()
	var updates []ToolExecutionUpdateEvent
	for _, event := range events {
		if update, ok := event.(ToolExecutionUpdateEvent); ok {
			updates = append(updates, update)
		}
	}
	if len(updates) != 1 || !reflect.DeepEqual(updates[0].PartialResult.MemberNames(), []string{"details", "content"}) {
		t.Fatalf("tool_execution_update = %+v, want member names [details content]", updates)
	}
	ends := toolEndEvents(events)
	if len(ends) != 1 || !reflect.DeepEqual(ends[0].Result.MemberNames(), []string{"isError", "content", "details"}) {
		t.Fatalf("tool_execution_end = %+v, want member names [isError content details]", ends)
	}
}
