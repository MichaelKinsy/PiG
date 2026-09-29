package ai

import (
	"encoding/json"
	"testing"
)

// .upstream/v0.87.1/packages/ai/test/lax-message-content.test.ts:31 — normalizes null/missing content to an empty array instead of crashing.
func TestLaxMessageContentUpstream(t *testing.T) {
	var user UserMessage
	if err := json.Unmarshal([]byte(`{"role":"user","content":null,"timestamp":0}`), &user); err != nil {
		t.Fatal(err)
	}
	var assistant AssistantMessage
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":null,"api":"openai-completions","provider":"openai","model":"test-model","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":0}`), &assistant); err != nil {
		t.Fatal(err)
	}
	var tool ToolResultMessage
	if err := json.Unmarshal([]byte(`{"role":"toolResult","toolCallId":"call_1","toolName":"web_search","isError":false,"timestamp":0}`), &tool); err != nil {
		t.Fatal(err)
	}
	request := Context{Messages: []Message{user, assistant, tool}}
	normalized := NormalizeContext(request)
	messages := normalized.Messages()
	if len(messages) != 3 {
		t.Fatalf("messages=%#v", messages)
	}
	for _, message := range messages {
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["content"]) != "[]" {
			t.Errorf("content=%s, want []", fields["content"])
		}
	}
	// Drive the text-only provider boundary which originally crashed in Pi.
	provider := NewOpenAIProvider(OpenAIConfig{APIKey: "test", Model: "test-model", ProviderID: "openai", BaseURL: "https://example.invalid/v1"})
	_ = captureSamplingPayload(t, provider, normalized, StreamOptions{})
	if user.Content != nil || assistant.Content != nil || tool.Content != nil {
		t.Fatal("normalization mutated the caller's messages")
	}
}
