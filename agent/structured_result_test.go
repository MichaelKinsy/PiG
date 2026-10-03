package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Regression guards for structured tool results in the agent loop. Upstream
// 0.99.1 keeps structuredContent on the result object that events and
// afterToolCall see, keeps it out of the tool-result message
// (.upstream/v0.99.1/packages/agent/src/agent-loop.ts:922-935), lets a tool
// report a failure with `isError: true` while keeping details (types.ts:436-441,
// agent-loop.ts:840), and applies afterToolCall's structuredContent rule
// (agent-loop.ts:878-889).

func TestAgentLoop_ToolResultMessageOmitsStructuredContentButEventKeepsIt(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "echo", ai.JsonObject{"value": "x"}))}
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{newStructuredEchoTool()}, EventCh: rec.ch})

	msgs := mustSend(t, a, "go")

	var ended *ToolExecutionEndEvent
	for _, event := range rec.stop() {
		if end, ok := event.(ToolExecutionEndEvent); ok {
			ended = &end
		}
	}
	if ended == nil || string(ended.Result.StructuredContent) != `{"value":"x"}` {
		t.Fatalf("tool_execution_end = %+v, want structuredContent {\"value\":\"x\"}", ended)
	}
	for _, msg := range msgs {
		if msg.ToolResult == nil {
			continue
		}
		encoded, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "structuredContent") {
			t.Fatalf("tool result message carries structuredContent: %s", encoded)
		}
	}
}

func TestAgentLoop_ToolReturningIsErrorYieldsErrorResultWithDetails(t *testing.T) {
	failing := &scriptTool{name: "failing", params: map[string]any{"type": "object"},
		execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "bad"}}, Details: map[string]any{"partial": true}, IsError: true}, nil
		}}
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "failing", nil))}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{failing}})

	msgs := mustSend(t, a, "go")

	var result *ToolResultMessage
	for _, msg := range msgs {
		if msg.ToolResult != nil {
			result = msg.ToolResult
		}
	}
	if result == nil || !result.IsError || result.Details == nil {
		t.Fatalf("tool result = %+v, want isError with details kept", result)
	}
}

func TestAgentLoop_AfterToolCallStructuredContentRule(t *testing.T) {
	redacted := []ai.ToolResultMessageContent{ai.TextContent{Text: "redacted"}}
	cases := []struct {
		name  string
		after AfterToolCallResult
		want  string
	}{
		{"content only drops it", AfterToolCallResult{Content: redacted}, ""},
		{"empty content also drops it", AfterToolCallResult{Content: []ai.ToolResultMessageContent{}}, ""},
		{"structured content replaces it", AfterToolCallResult{StructuredContent: json.RawMessage(`{"value":"replaced"}`)}, `{"value":"replaced"}`},
		{"both replace", AfterToolCallResult{Content: redacted, StructuredContent: json.RawMessage(`{"value":"both"}`)}, `{"value":"both"}`},
		{"a JSON null falls through like ??", AfterToolCallResult{Content: redacted, StructuredContent: json.RawMessage(`null`)}, ""},
		{"details only keeps it", AfterToolCallResult{Details: "kept"}, `{"value":"original"}`},
	}
	for _, tc := range cases {
		provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "echo", ai.JsonObject{"value": "original"}))}
		rec := newEventRecorder(nil)
		a := NewAgent(AgentOptions{
			Model: scriptedModel(provider), Tools: []AgentTool{newStructuredEchoTool()}, EventCh: rec.ch,
			AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
				return tc.after
			}},
		})

		mustSend(t, a, "go")

		got, found := "", false
		for _, event := range rec.stop() {
			if end, ok := event.(ToolExecutionEndEvent); ok {
				got, found = string(end.Result.StructuredContent), true
			}
		}
		if !found || got != tc.want {
			t.Errorf("%s: structuredContent = %q, want %q", tc.name, got, tc.want)
		}
	}
}
