package ai

import (
	"encoding/json"
	"errors"
	"testing"
)

// Ports packages/ai/test/mistral-reasoning-mode.test.ts:57,64,71,80,87,97,104,113,120,128 (12 expanded cases).
func TestMistralReasoningModeUpstream(t *testing.T) {
	cases := []struct {
		name, id            string
		reasoning           bool
		thinking            ThinkingLevel
		session             string
		retention           CacheRetention
		effort, mode, cache string
	}{
		{name: "uses reasoning_effort for Mistral Small 4", id: "mistral-small-2603", reasoning: true, thinking: ThinkingMedium, effort: "high"},
		{name: "omits reasoning controls for Mistral Small 4 when thinking is off", id: "mistral-small-2603", reasoning: true},
		{name: "uses prompt_mode for Magistral reasoning models", id: "magistral-medium-latest", reasoning: true, thinking: ThinkingMedium, mode: "reasoning"},
		{name: "GLM uses reasoning_effort when thinking is enabled", id: "zai-glm-5-2", reasoning: true, thinking: ThinkingMedium, effort: "high"},
		{name: "GLM omits reasoning controls when thinking is off", id: "zai-glm-5-2", reasoning: true},
		{name: "omits reasoning controls for non-reasoning Medium models", id: "mistral-medium-2505", thinking: ThinkingMedium},
		{name: "uses the session id as prompt cache key", id: "mistral-large-latest", session: "session-123", cache: "session-123"},
		{name: "omits prompt cache key when cache retention is disabled", id: "mistral-large-latest", session: "session-123", retention: CacheRetentionNone},
	}
	for _, id := range []string{"mistral-medium-2604", "mistral-medium-latest"} {
		cases = append(cases,
			struct {
				name, id            string
				reasoning           bool
				thinking            ThinkingLevel
				session             string
				retention           CacheRetention
				effort, mode, cache string
			}{name: id + " enabled", id: id, reasoning: true, thinking: ThinkingMedium, effort: "high"},
			struct {
				name, id            string
				reasoning           bool
				thinking            ThinkingLevel
				session             string
				retention           CacheRetention
				effort, mode, cache string
			}{name: id + " off", id: id, reasoning: true})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{ID: tc.id, DisplayName: tc.id, Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "mistral", BaseURL: "http://127.0.0.1:9", Reasoning: tc.reasoning}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}}
			var payload map[string]any
			sentinel := errors.New("payload captured")
			_, err := StreamSimple(t.Context(), m, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{APIKey: "fake-key", Thinking: tc.thinking, SessionID: tc.session, CacheRetention: tc.retention, OnPayload: func(p any, _ *Model) (any, error) {
				data, e := json.Marshal(p)
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal(data, &payload); e != nil {
					t.Fatal(e)
				}
				return nil, sentinel
			}})
			if !errors.Is(err, sentinel) || payload == nil {
				t.Fatalf("capture = %v payload=%v", err, payload)
			}
			// OnPayload precedes SDK-to-wire lowering.
			for key, want := range map[string]string{"reasoningEffort": tc.effort, "promptMode": tc.mode, "promptCacheKey": tc.cache} {
				got, present := payload[key]
				if want == "" {
					if present {
						t.Errorf("%s = %v, want omitted", key, got)
					}
				} else if got != want {
					t.Errorf("%s = %v, want %s", key, got, want)
				}
			}
		})
	}
}
