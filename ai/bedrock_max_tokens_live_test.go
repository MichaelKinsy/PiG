//go:build live

package ai

import (
	"context"
	"testing"
	"time"
)

func TestPortWave13BedrockClaudeMaxTokensE2E(t *testing.T) {
	// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:166-194. This live case inherently requests more than 4096 output tokens; retain the original 180-second deadline.
	t.Run("uses the model maxTokens cap instead of Bedrock's 4096-token default for adaptive Claude models", func(t *testing.T) {
		requireBedrockLiveCredentials(t)
		model := cloneGeneratedModel(t, "amazon-bedrock/global.anthropic.claude-sonnet-4-6").ToModel()
		model.Capabilities.MaxOutputTokens = 6000
		provider := NewBedrockProviderWithModel(*model)
		defer func() {
			if err := provider.Close(); err != nil {
				t.Error(err)
			}
		}()
		ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
		defer cancel()
		stream, err := provider.Stream(ctx, NormalizeContext(Context{SystemPrompt: "You are a deterministic text generator. Follow the requested output format exactly.", Messages: []Message{UserMessage{Content: UserText("Output exactly 5200 repetitions of the token alpha, separated by single spaces. Do not number them. Do not use markdown. Do not add any other text."), Timestamp: time.Now().UnixMilli()}}}), StreamOptions{Thinking: ThinkingLow, IsReasoning: model.ProviderMeta.Reasoning})
		if err != nil {
			t.Fatal(err)
		}
		response := stream.Result()
		if response.StopReason == StopReasonError || response.Usage.Output <= 4096 {
			t.Fatalf("stopReason=%s error=%s output=%d; want non-error and output>4096", response.StopReason, response.ErrorMessage, response.Usage.Output)
		}
	})
}
