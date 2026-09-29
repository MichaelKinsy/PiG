//go:build live

package ai

import (
	"testing"
	"time"
)

func TestPortWave13AnthropicEagerToolInputE2E(t *testing.T) {
	cases := loadAnthropicAcceptanceCases(t)
	// The catalog denominator case (untagged) is ai/anthropic_e2e_catalog_test.go.
	for _, group := range []struct {
		name, title string
		rows        []string
		force       bool
	}{
		// packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:136 (all 11 selected rows)
		{"generated compatibility settings", " accepts configured tool streaming", cases.Configured, false},
		// packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:146 (all 10 selected rows)
		{"forced eager_input_streaming probe", " accepts forced eager_input_streaming", cases.ForcedEager, true},
	} {
		for _, spec := range group.rows {
			t.Run(group.name+"/"+spec+group.title, func(t *testing.T) {
				model := cloneGeneratedModel(t, spec)
				if group.force {
					if model.Compat == nil {
						model.Compat = &ModelCompat{}
					}
					model.Compat.SupportsEagerToolInputStreaming = new(true)
				}
				provider := newAnthropicTestProvider(t, model, liveProviderKey(t, model.Provider))
				request := Context{
					SystemPrompt: "You are a concise assistant. Use tools when useful.",
					Messages:     []Message{UserMessage{Content: UserText("Call echo_value with value set to eager-input-streaming-compat."), Timestamp: time.Now().UnixMilli()}},
					Tools:        []ToolSchema{{Name: "echo_value", Description: "Echo a string value", Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string", "description": "The value to echo"}}, "required": []string{"value"}}}},
				}
				stream, err := provider.Stream(t.Context(), NormalizeContext(request), StreamOptions{MaxTokens: 128, ThinkingEnabled: new(false)})
				if err != nil {
					t.Fatal(err)
				}
				response := stream.Result()
				if response.ErrorMessage != "" || response.StopReason == StopReasonError {
					t.Fatalf("stopReason=%s error=%s", response.StopReason, response.ErrorMessage)
				}
			})
		}
	}
}
