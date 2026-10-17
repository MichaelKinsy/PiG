package ai

import (
	"encoding/json"
	"errors"
	"testing"
)

// A supported level the thinking level map does not name falls back to "high": `effortMap[reasoning] ?? "high"` (mistral-conversations.ts:204-206).
// The upstream suite has no case for this branch.
func TestMistralReasoningEffortFallsBackToHighForAnUnmappedLevel(t *testing.T) {
	model := &Model{ID: "mistral-unmapped", DisplayName: "mistral-unmapped", Input: []string{"text"}, ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: new("none")}, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "mistral", BaseURL: "http://127.0.0.1:9", Reasoning: true}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}}
	var payload map[string]any
	sentinel := errors.New("payload captured")
	_, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{APIKey: "fake-key", Thinking: ThinkingLevelMedium, OnPayload: func(p any, _ *Model) (any, error) {
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		return nil, sentinel
	}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("capture = %v", err)
	}
	if payload["reasoningEffort"] != "high" {
		t.Fatalf("reasoningEffort = %v, want high", payload["reasoningEffort"])
	}
	if _, present := payload["promptMode"]; present {
		t.Fatalf("promptMode = %v, want omitted", payload["promptMode"])
	}
}
