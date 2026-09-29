//go:build live

package ai

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPortWave13AnthropicThinkingBindingE2E(t *testing.T) {
	// packages/ai/test/anthropic-thinking-binding-e2e.test.ts:35-60
	t.Run("replays managed effort markers required by signed Fable thinking", func(t *testing.T) {
		key := liveProviderKey(t, "anthropic")
		ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
		defer cancel()
		provider := newAnthropicTestProvider(t, cloneGeneratedModel(t, "anthropic/claude-fable-5-1"), key)
		request := func(history Context, effort string) *AssistantMessage {
			t.Helper()
			// packages/ai/src/api/anthropic-messages.ts:1151-1155 defaults display to summarized, as requested by the upstream helper.
			stream, err := provider.Stream(ctx, NormalizeContext(history), StreamOptions{CacheRetention: CacheRetentionNone, MaxTokens: 1536, ThinkingEnabled: new(true), Effort: effort, OnPayload: func(value any, _ *Model) (any, error) {
				params := value.(anthRequest)
				if params.Thinking != nil && params.Thinking.BlockBinding != nil {
					params.Thinking.BlockBinding.PrefixMismatchBehavior = "error"
				}
				return params, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			return stream.Result()
		}
		firstUser := UserMessage{Content: UserText("Compute 982451653 multiplied by 961748941. Return only the integer."), Timestamp: 1}
		first := request(Context{Messages: []Message{firstUser}}, "low")
		if first.StopReason != StopReasonStop {
			t.Fatalf("first stopReason=%s error=%s", first.StopReason, first.ErrorMessage)
		}
		signed := false
		for _, block := range first.Content {
			if thinking, ok := block.(ThinkingContent); ok && thinking.ThinkingSignature != "" {
				signed = true
			}
		}
		if !signed || first.ProviderThinkingLevel != "low" {
			t.Fatalf("signed thinking=%t providerThinkingLevel=%q", signed, first.ProviderThinkingLevel)
		}
		secondUser := UserMessage{Content: UserText("Reply with exactly: ok"), Timestamp: 2}
		exact := request(Context{Messages: []Message{firstUser, *first, secondUser}}, "high")
		if exact.StopReason != StopReasonStop {
			t.Fatalf("exact stopReason=%s error=%s", exact.StopReason, exact.ErrorMessage)
		}
		unmanaged := *first
		unmanaged.ProviderThinkingLevel = ""
		missingMarker := request(Context{Messages: []Message{firstUser, unmanaged, secondUser}}, "high")
		if missingMarker.StopReason != StopReasonError || !strings.Contains(missingMarker.ErrorMessage, "Invalid `signature`") {
			t.Fatalf("missing marker stopReason=%s error=%s", missingMarker.StopReason, missingMarker.ErrorMessage)
		}
	})
}
