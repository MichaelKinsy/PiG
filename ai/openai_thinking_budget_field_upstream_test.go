package ai

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestOpenAICompletionsThinkingBudgetFieldSelectsTheWireKeyLikePi mirrors packages/ai/src/api/openai-completions.ts:1012-1018
// resolveThinkingTokenBudgetField (an explicit thinkingTokenBudgetField wins; otherwise supportsThinkingTokenBudget selects
// "thinking_token_budget"; otherwise none) and :983-985 (the clamped budget is assigned to exactly that top-level request key).
func TestOpenAICompletionsThinkingBudgetFieldSelectsTheWireKeyLikePi(t *testing.T) {
	yes := true
	budgetKeys := []string{"thinking_token_budget", "thinking_budget", "thinking_budget_tokens"}
	cases := []struct {
		name   string
		compat OpenAICompat
		want   string // "" = no budget key
	}{
		{"neither field nor support sends no budget", OpenAICompat{ThinkingFormat: "chat-template"}, ""},
		{"supportsThinkingTokenBudget selects thinking_token_budget", OpenAICompat{ThinkingFormat: "chat-template", SupportsThinkingTokenBudget: &yes}, "thinking_token_budget"},
		{"explicit thinking_token_budget", OpenAICompat{ThinkingFormat: "chat-template", ThinkingTokenBudgetField: ThinkingTokenBudgetFieldThinkingTokenBudget}, "thinking_token_budget"},
		{"explicit thinking_budget", OpenAICompat{ThinkingFormat: "chat-template", ThinkingTokenBudgetField: ThinkingTokenBudgetFieldThinkingBudget}, "thinking_budget"},
		{"explicit thinking_budget_tokens", OpenAICompat{ThinkingFormat: "chat-template", ThinkingTokenBudgetField: ThinkingTokenBudgetFieldThinkingBudgetTokens}, "thinking_budget_tokens"},
		{"the explicit field wins over supportsThinkingTokenBudget", OpenAICompat{ThinkingFormat: "chat-template", SupportsThinkingTokenBudget: &yes, ThinkingTokenBudgetField: ThinkingTokenBudgetFieldThinkingBudget}, "thinking_budget"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compat := tc.compat
			model := &Model{ID: "zai-org/glm-5.2", Input: []string{"text"}, Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh, MaxOutputTokens: 16384}}
			provider := NewOpenAIProvider(OpenAIConfig{Model: model.ID, ModelMetadata: model, ProviderID: "local-vllm", APIKey: "test", Compat: &compat})
			captured := errors.New("captured")
			var payload map[string]any
			options := StreamOptions{IsReasoning: true, Thinking: ThinkingLevelHigh, MaxTokens: 16384, OnPayload: func(request any, _ *Model) (any, error) {
				raw, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Fatal(err)
				}
				return nil, captured
			}}
			transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}})
			if _, err := provider.Stream(t.Context(), transcript, options); !errors.Is(err, captured) {
				t.Fatalf("Stream error = %v, want the payload capture", err)
			}
			var present []string
			for _, key := range budgetKeys {
				if _, ok := payload[key]; ok {
					present = append(present, key)
				}
			}
			switch {
			case tc.want == "" && len(present) != 0:
				t.Fatalf("budget keys = %v, want none", present)
			case tc.want != "" && (len(present) != 1 || present[0] != tc.want):
				t.Fatalf("budget keys = %v, want only %s", present, tc.want)
			}
			if tc.want != "" {
				if value, _ := payload[tc.want].(float64); value <= 0 {
					t.Fatalf("%s = %v, want the positive clamped budget", tc.want, payload[tc.want])
				}
			}
		})
	}
}
