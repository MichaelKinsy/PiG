package ai

import (
	"errors"
	"testing"
)

func BenchmarkCompletionsThinkingBudgetPayload(b *testing.B) {
	model := &Model{ID: "zai-org/glm-5.2", Input: []string{"text"}, Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh, MaxOutputTokens: 16384}}
	provider := NewOpenAIProvider(OpenAIConfig{Model: model.ID, ModelMetadata: model, ProviderID: "local-vllm", APIKey: "test", Compat: &OpenAICompat{ThinkingFormat: "chat-template", ThinkingTokenBudgetField: "thinking_budget", ChatTemplateKwargs: map[string]any{"thinking_budget": map[string]any{"$var": "thinking.budget"}}}})
	captured := errors.New("captured")
	options := StreamOptions{IsReasoning: true, Thinking: ThinkingHigh, MaxTokens: 16384, OnPayload: func(_ any, _ *Model) (any, error) { return nil, captured }}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := provider.Stream(b.Context(), transcript, options); !errors.Is(err, captured) {
			b.Fatal(err)
		}
	}
}

func TestResolveClampedThinkingBudgetUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/src/api/openai-completions.ts:1012-1025 and api/simple-options.ts:clampThinkingBudgetToAnswerRoom.
	for _, tc := range []struct {
		level         ThinkingLevel
		custom        *ThinkingBudgets
		ceiling, want int
	}{
		{ThinkingMedium, &ThinkingBudgets{Medium: 4096}, 16384, 4096},
		{ThinkingHigh, nil, 16384, 15360},
		{ThinkingHigh, &ThinkingBudgets{High: 8192}, 4096, 3072},
		{ThinkingXHigh, &ThinkingBudgets{High: 8192}, 16384, 8192},
		{ThinkingMax, &ThinkingBudgets{High: 8192}, 16384, 8192},
		{ThinkingHigh, nil, 1024, 0},
	} {
		if got := resolveClampedThinkingBudget(tc.level, tc.custom, tc.ceiling); got != tc.want {
			t.Errorf("%s ceiling=%d got=%d want=%d", tc.level, tc.ceiling, got, tc.want)
		}
	}
}

func TestOpenAISelectedModelMetadataControlsImageCapability(t *testing.T) {
	provider := NewOpenAIProvider(OpenAIConfig{Model: "gpt-4o-mini", ProviderID: "openai", ModelMetadata: &Model{ID: "gpt-4o-mini", Input: []string{"text"}}}).(*openAIProvider)
	if provider.modelSupportsImages() {
		t.Fatal("catalog image capability replaced the selected text-only model")
	}
}
