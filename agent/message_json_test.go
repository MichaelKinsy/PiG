package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestAgentMessageJSONUserExactAndRoundTrip(t *testing.T) {
	message := AgentMessage{User: &UserMessage{
		Role: RoleUser,
		Content: ai.UserContentBlocks{
			ai.TextContent{Text: "hello"},
			ai.ImageContent{Data: "QUJD", MimeType: "image/png"},
		},
		Timestamp: 123,
	}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image","data":"QUJD","mimeType":"image/png"}],"timestamp":123}`
	if string(encoded) != want {
		t.Fatalf("Marshal = %s, want %s", encoded, want)
	}
	var decoded AgentMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.User == nil {
		t.Fatalf("decoded user = %#v", decoded.User)
	}
	blocks, ok := decoded.User.Content.(ai.UserContentBlocks)
	if !ok || len(blocks) != 2 {
		t.Fatalf("decoded user content = %#v", decoded.User.Content)
	}
	if text, ok := blocks[0].(ai.TextContent); !ok || text.Text != "hello" {
		t.Fatalf("decoded text block = %#v", blocks[0])
	}
	if image, ok := blocks[1].(ai.ImageContent); !ok || image.MimeType != "image/png" {
		t.Fatalf("decoded image block = %#v", blocks[1])
	}
}

func TestAgentMessageJSONToolResultExactAndRoundTrip(t *testing.T) {
	message := AgentMessage{ToolResult: &ToolResultMessage{
		Role:       RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "read",
		Content: []ai.ToolResultMessageContent{
			ai.TextContent{Text: "done"},
			ai.ImageContent{Data: "QUJD", MimeType: "image/png"},
		},
		Details:   map[string]any{"lines": float64(3)},
		IsError:   false,
		Timestamp: 456,
	}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"role":"toolResult","toolCallId":"call-1","toolName":"read","content":[{"type":"text","text":"done"},{"type":"image","data":"QUJD","mimeType":"image/png"}],"details":{"lines":3},"isError":false,"timestamp":456}`
	if string(encoded) != want {
		t.Fatalf("Marshal = %s, want %s", encoded, want)
	}
	var decoded AgentMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.ToolResult == nil || decoded.ToolResult.ToolCallID != "call-1" || decoded.ToolResult.Text() != "done" {
		t.Fatalf("decoded tool result = %#v", decoded.ToolResult)
	}
	if len(decoded.ToolResult.Images()) != 1 {
		t.Fatalf("decoded images = %#v", decoded.ToolResult.Images())
	}
}

func TestAgentMessageJSONAssistantPreservesUpstreamFields(t *testing.T) {
	reasoning := 2
	message := AgentMessage{Assistant: &AssistantMessage{
		Role: RoleAssistant, API: ai.API("openai-responses"), Provider: "openai", ModelID: "gpt-5",
		ResponseModel: "gpt-5-2026", ResponseID: "resp-1",
		Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "hmm", ThinkingSignature: "sig"}},
		Usage: &ai.Usage{
			Input: 10, Output: 4, Reasoning: &reasoning, CacheRead: 3, CacheWrite: 1,
			TotalTokens: 18, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.01, CacheWrite: 0.02, Total: 0.33},
		},
		StopReason: "deferred", Deferred: &ai.DeferredHandle{
			Provider: "openai", ModelID: "gpt-5", API: ai.API("openai-responses"), ID: "batch-1",
		},
		Timestamp: 321,
	}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded AgentMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got := decoded.Assistant
	if got == nil || got.API != "openai-responses" || got.ResponseModel != "gpt-5-2026" || got.ResponseID != "resp-1" {
		t.Fatalf("assistant identity fields = %#v", got)
	}
	if got.Usage == nil || got.Usage.TotalTokens != 18 || got.Usage.Cost.Total != 0.33 || got.Usage.Reasoning == nil || *got.Usage.Reasoning != 2 {
		t.Fatalf("assistant usage = %#v", got.Usage)
	}
	if got.Deferred == nil || got.Deferred.ID != "batch-1" {
		t.Fatalf("assistant deferred handle = %#v", got.Deferred)
	}
}

func TestAgentMessageJSONCustomPreservesShape(t *testing.T) {
	message := AgentMessage{Custom: map[string]any{
		"role": "branchSummary", "summary": "summary", "fromId": "entry-1", "timestamp": float64(789),
	}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Pi builds a branch summary as {role, summary, fromId, timestamp} and JSON.stringify keeps that order (messages.ts:100-107); the former expectation was a Go map's sorted order.
	want := `{"role":"branchSummary","summary":"summary","fromId":"entry-1","timestamp":789}`
	if string(encoded) != want {
		t.Fatalf("Marshal = %s, want %s", encoded, want)
	}
	var decoded AgentMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Custom["role"] != "branchSummary" || decoded.Custom["summary"] != "summary" {
		t.Fatalf("decoded custom = %#v", decoded.Custom)
	}
}

func TestAgentMessageJSONRejectsInvalidUnion(t *testing.T) {
	cases := []struct {
		name  string
		value AgentMessage
	}{
		{name: "empty"},
		{name: "multiple", value: AgentMessage{User: &UserMessage{Role: RoleUser}, Assistant: &AssistantMessage{Role: RoleAssistant}}},
		{name: "custom without role", value: AgentMessage{Custom: map[string]any{"content": "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := json.Marshal(tc.value); err == nil {
				t.Fatal("Marshal should fail")
			}
		})
	}
	for _, raw := range []string{`{}`, `{"role":1}`} {
		var message AgentMessage
		if err := json.Unmarshal([]byte(raw), &message); err == nil {
			t.Fatalf("Unmarshal(%s) should fail", raw)
		}
	}
	var extension AgentMessage
	if err := json.Unmarshal([]byte(`{"role":"artifact","id":"a1"}`), &extension); err != nil {
		t.Fatalf("custom extension role must remain extensible: %v", err)
	}
	if extension.Custom["role"] != "artifact" || extension.Custom["id"] != "a1" {
		t.Fatalf("extension message = %#v", extension.Custom)
	}
}

// upstream: packages/agent/src/agent-loop.ts createToolResultMessage (1.1.0) writes durationMs between isError and timestamp; a result without one has no key (#10549).
func TestAgentMessageJSONToolResultDurationMs(t *testing.T) {
	duration := int64(4200)
	message := AgentMessage{ToolResult: &ToolResultMessage{
		Role: RoleToolResult, ToolCallID: "call-1", ToolName: "bash",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, DurationMs: &duration, Timestamp: 5,
	}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"toolResult","toolCallId":"call-1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false,"durationMs":4200,"timestamp":5}`
	if string(encoded) != want {
		t.Fatalf("Marshal = %s, want %s", encoded, want)
	}
	var decoded AgentMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ToolResult == nil || decoded.ToolResult.DurationMs == nil || *decoded.ToolResult.DurationMs != 4200 {
		t.Fatalf("decoded = %#v", decoded.ToolResult)
	}
	// A result stored before durations were recorded decodes without one and encodes without the key.
	var legacy AgentMessage
	if err := json.Unmarshal([]byte(`{"role":"toolResult","toolCallId":"c","toolName":"bash","content":[],"isError":false,"timestamp":1}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.ToolResult.DurationMs != nil {
		t.Fatalf("legacy durationMs = %v", *legacy.ToolResult.DurationMs)
	}
	reencoded, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(reencoded), "durationMs") {
		t.Fatalf("re-encoded = %s, %v", reencoded, err)
	}
}

// upstream: event-stream.ts #time assigns durationMs when the provider pushes the final message, agent-loop.ts assigns thinkingLevel after that (#10549).
func TestAgentMessageJSONAssistantDurationMsOrder(t *testing.T) {
	duration := int64(40)
	encoded, err := json.Marshal(AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonStop, Timestamp: 7, DurationMs: &duration, ThinkingLevel: ai.ThinkingHigh}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(encoded), `"timestamp":7,"durationMs":40,"thinkingLevel":"high"}`) {
		t.Fatalf("assistant JSON = %s", encoded)
	}
}

// Pi's providers build the output with its timestamp and set errorMessage in the catch block, so an errored message lists errorMessage after the timestamp (the live RPC events and the reloaded session agree).
func TestAgentMessageJSONAssistantErrorMessageFollowsTimestamp(t *testing.T) {
	duration := int64(3)
	encoded, err := json.Marshal(AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonError, ErrorMessage: "boom", Timestamp: 7, DurationMs: &duration, ThinkingLevel: ai.ThinkingOff}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(encoded), `"stopReason":"error","timestamp":7,"errorMessage":"boom","durationMs":3,"thinkingLevel":"off"}`) {
		t.Fatalf("assistant JSON = %s", encoded)
	}
}
