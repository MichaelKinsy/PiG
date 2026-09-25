package ai

import (
	"context"
	"testing"
)

func TestResolveCodexURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/codex", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/codex/responses", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://proxy.example.com", "https://proxy.example.com/codex/responses"},
	}
	for _, tc := range cases {
		got := resolveCodexURL(tc.in)
		if got != tc.want {
			t.Errorf("resolveCodexURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestThinkingToReasoningEffort_CodexModelMap(t *testing.T) {
	model := &Model{Capabilities: ModelCapabilities{MaxThinking: ThinkingXHigh}, ThinkingLevelMap: ThinkingLevelMap{
		ThinkingMinimal: new("low"),
		ThinkingLow:     new("medium"),
		ThinkingMedium:  new("medium"),
		ThinkingHigh:    new("high"),
	}}
	if got := thinkingToReasoningEffort(model, ThinkingMinimal); got != "low" {
		t.Fatalf("thinkingToReasoningEffort(minimal) = %q, want low", got)
	}
	if got := thinkingToReasoningEffort(model, ThinkingLow); got != "medium" {
		t.Fatalf("thinkingToReasoningEffort(low) = %q, want medium", got)
	}
}

func TestNewOpenAICodexResponsesProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "jwt-token")
	p := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{Model: "gpt-5.4"})
	op, ok := p.(*openAIResponsesProvider)
	if !ok {
		t.Fatalf("type = %T, want *openAIResponsesProvider", p)
	}
	if op.cfg.ProviderID != string(APIOpenAICodexResponses) {
		t.Fatalf("ProviderID = %q", op.cfg.ProviderID)
	}
	if op.cfg.Model != "gpt-5.4" {
		t.Fatalf("Model = %q", op.cfg.Model)
	}
	key, err := op.cfg.GetAPIKey(context.Background())
	if err != nil || key != "jwt-token" {
		t.Fatalf("GetAPIKey = %q, %v", key, err)
	}
	baseURL, err := op.cfg.GetBaseURL(context.Background())
	if err != nil || baseURL != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("GetBaseURL = %q, %v", baseURL, err)
	}
	if op.cfg.ExtraHeaders["originator"] != "pi" {
		t.Fatalf("missing originator header")
	}
}
