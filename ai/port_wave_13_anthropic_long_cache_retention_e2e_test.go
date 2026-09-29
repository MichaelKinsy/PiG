//go:build live

package ai

import (
	"testing"
	"time"
)

func TestPortWave13AnthropicLongCacheRetentionE2E(t *testing.T) {
	cases := loadAnthropicAcceptanceCases(t)
	// The catalog denominator case (untagged) is ai/anthropic_e2e_catalog_test.go.
	// packages/ai/test/anthropic-long-cache-retention-e2e.test.ts:122 (all 11 selected rows)
	for _, spec := range cases.Configured {
		t.Run("forced long cache retention probe/"+spec+" accepts long cache retention", func(t *testing.T) {
			model := cloneGeneratedModel(t, spec)
			if model.Compat == nil {
				model.Compat = &ModelCompat{}
			}
			model.Compat.SupportsLongCacheRetention = new(true)
			provider := newAnthropicTestProvider(t, model, liveProviderKey(t, model.Provider))
			request := Context{SystemPrompt: "You are a concise assistant.", Messages: []Message{UserMessage{Content: UserText("Reply with exactly: long cache retention accepted"), Timestamp: time.Now().UnixMilli()}}}
			stream, err := provider.Stream(t.Context(), NormalizeContext(request), StreamOptions{MaxTokens: 128, ThinkingEnabled: new(false), CacheRetention: CacheRetentionLong})
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
