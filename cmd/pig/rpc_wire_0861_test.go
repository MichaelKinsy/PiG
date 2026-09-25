package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func decodeRPCEvent(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestRPCMessageUpdateExact0861UsageAndVariant(t *testing.T) {
	cacheWrite1h, reasoning := 7, 11
	usage := &ai.Usage{
		Input: 2, Output: 3, CacheRead: 5, CacheWrite: 6,
		CacheWrite1h: &cacheWrite1h, Reasoning: &reasoning, TotalTokens: 999,
		Cost: ai.UsageCost{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Total: 10},
	}
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "x"}}, Usage: *usage, StopReason: ai.StopReasonPending}
	events, err := rpcAgentEvent(agent.MessageUpdateEvent{
		Message:               agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Usage: usage}},
		AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	got := decodeRPCEvent(t, events[0])
	want := map[string]any{
		"type": "message_update",
		"usage": map[string]any{
			"input": float64(2), "output": float64(3), "cacheRead": float64(5), "cacheWrite": float64(6),
			"cacheWrite1h": float64(7), "reasoning": float64(11), "totalTokens": float64(999),
			"cost": map[string]any{"input": float64(1), "output": float64(2), "cacheRead": float64(3), "cacheWrite": float64(4), "total": float64(10)},
		},
		"assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": float64(0), "delta": "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event = %#v, want %#v", got, want)
	}
}

func TestRPCMessageUpdateRejectsInvalidMessageAndToolStart(t *testing.T) {
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "not a tool"}}}
	tests := []struct {
		name  string
		event agent.MessageUpdateEvent
		want  string
	}{
		{
			name:  "non-assistant message",
			event: agent.MessageUpdateEvent{Message: agent.AgentMessage{User: &agent.UserMessage{}}, AssistantMessageEvent: ai.TextDeltaEvent{Partial: partial}},
			want:  "not an assistant",
		},
		{
			name:  "invalid tool index",
			event: agent.MessageUpdateEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{}}, AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 2, Partial: partial}},
			want:  "index 2 is invalid",
		},
		{
			name:  "non-tool content",
			event: agent.MessageUpdateEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{}}, AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial}},
			want:  "is not a tool call",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := rpcAgentEvent(test.event); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRPCToolCallStartExactIdentityAndMarshalFailure(t *testing.T) {
	call := ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{"path": "main.go"}}
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{call}}
	events, err := rpcAgentEvent(agent.MessageUpdateEvent{
		Message:               agent.AgentMessage{Assistant: &agent.AssistantMessage{}},
		AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial},
	})
	if err != nil {
		t.Fatal(err)
	}
	wire := decodeRPCEvent(t, events[0])["assistantMessageEvent"].(map[string]any)
	if wire["type"] != "toolcall_start" || wire["id"] != "call-1" || wire["toolName"] != "read" {
		t.Fatalf("tool start = %#v", wire)
	}
	if _, exists := wire["partial"]; exists {
		t.Fatalf("tool start leaked partial: %#v", wire)
	}

	bad := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "bad", Name: "bad", Arguments: ai.JsonObject{"unsupported": func() {}}}}}
	if _, err := rpcAgentEvent(agent.MessageUpdateEvent{
		Message:               agent.AgentMessage{Assistant: &agent.AssistantMessage{}},
		AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: bad},
	}); err == nil || !strings.Contains(err.Error(), "marshal message_update") {
		t.Fatalf("marshal error = %v", err)
	}
}

func TestRPCAssistantMessageEndPreserves0861Metadata(t *testing.T) {
	cacheWrite1h, reasoning := 1, 2
	endTurn := false
	usage := &ai.Usage{Input: 3, Output: 4, CacheWrite1h: &cacheWrite1h, Reasoning: &reasoning, TotalTokens: 88}
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done", TextSignature: "sig"}},
		API: ai.APIOpenAIResponses, Provider: "gateway", ModelID: "model", ResponseModel: "served-model", ResponseID: "response-1",
		ProviderThinkingLevel: "high", Diagnostics: []ai.AssistantMessageDiagnostic{{Type: "fallback", Timestamp: 9}},
		Usage: usage, StopReason: ai.StopReasonStop, Deferred: &ai.DeferredHandle{Provider: "gateway", ModelID: "model", API: ai.APIOpenAIResponses, ID: "deferred-1"},
		RawStopReason: "completed", EndTurn: &endTurn, Timestamp: 123,
	}}
	events, err := rpcAgentEvent(agent.MessageEndEvent{Message: message})
	if err != nil {
		t.Fatal(err)
	}
	wire := decodeRPCEvent(t, events[0])["message"].(map[string]any)
	checks := map[string]any{
		"api": string(ai.APIOpenAIResponses), "provider": "gateway", "model": "model",
		"responseModel": "served-model", "responseId": "response-1", "providerThinkingLevel": "high",
		"rawStopReason": "completed", "endTurn": false,
	}
	for key, want := range checks {
		if got := wire[key]; got != want {
			t.Fatalf("%s = %#v, want %#v; wire=%#v", key, got, want, wire)
		}
	}
	if len(wire["diagnostics"].([]any)) != 1 || wire["deferred"].(map[string]any)["id"] != "deferred-1" {
		t.Fatalf("metadata = %#v", wire)
	}
	wireUsage := wire["usage"].(map[string]any)
	if wireUsage["totalTokens"] != float64(88) || wireUsage["cacheWrite1h"] != float64(1) || wireUsage["reasoning"] != float64(2) {
		t.Fatalf("usage = %#v", wireUsage)
	}
}

func TestRPCToolResultPreservesOrderedContentAndMetadata(t *testing.T) {
	usage := &ai.Usage{Input: 1, Output: 2, TotalTokens: 17}
	message := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: "call-1", ToolName: "read",
		Content: []ai.ToolResultMessageContent{
			ai.TextContent{Text: "before"}, ai.ImageContent{Data: "AAAA", MimeType: "image/png"}, ai.TextContent{Text: "after"},
		},
		Details: map[string]any{"lines": 3}, Usage: usage, IsError: true, Timestamp: 55,
	}}
	events, err := rpcAgentEvent(agent.MessageEndEvent{Message: message})
	if err != nil {
		t.Fatal(err)
	}
	wire := decodeRPCEvent(t, events[0])["message"].(map[string]any)
	content := wire["content"].([]any)
	if content[0].(map[string]any)["text"] != "before" || content[1].(map[string]any)["mimeType"] != "image/png" || content[2].(map[string]any)["text"] != "after" {
		t.Fatalf("content = %#v", content)
	}
	if wire["isError"] != true || wire["details"].(map[string]any)["lines"] != float64(3) || wire["usage"].(map[string]any)["totalTokens"] != float64(17) {
		t.Fatalf("tool result = %#v", wire)
	}
}
