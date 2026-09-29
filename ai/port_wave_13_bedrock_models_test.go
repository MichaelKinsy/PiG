//go:build live

package ai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestPortWave13BedrockModels(t *testing.T) {
	models := ListModels("amazon-bedrock")
	// packages/ai/test/bedrock-models.test.ts:39 expands every pinned Bedrock model, with the original 10-second per-case deadline.
	for _, model := range models {
		t.Run("should make a simple request with "+model.ID, func(t *testing.T) {
			t.Logf("live provider: %s", "amazon-bedrock")
			testenv.RequireLiveEnv(t, "BEDROCK_EXTENSIVE_MODEL_TEST")
			// packages/ai/test/bedrock-utils.ts:14-20 selects these three credential alternatives.
			switch {
			case os.Getenv("AWS_PROFILE") != "":
				testenv.RequireLiveEnv(t, "AWS_PROFILE")
			case os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "":
				testenv.RequireLiveEnv(t, "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY")
			case os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "":
				testenv.RequireLiveEnv(t, "AWS_BEARER_TOKEN_BEDROCK")
			default:
				testenv.RequireLiveEnv(t, "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			provider := NewBedrockProviderWithName(model.ID, model.DisplayName, model.BaseURL)
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			stream, err := provider.Stream(ctx, NormalizeContext(Context{SystemPrompt: "You are a helpful assistant. Be extremely concise.", Messages: []Message{UserMessage{Content: UserText("Reply with exactly: 'OK'"), Timestamp: time.Now().UnixMilli()}}}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			response := stream.Result()
			if response.messageRole() != "assistant" || len(response.Content) == 0 || response.Usage.Input+response.Usage.CacheRead <= 0 || response.Usage.Output <= 0 || response.ErrorMessage != "" {
				t.Fatalf("role=%s content blocks=%d input=%d cacheRead=%d output=%d error=%s", response.messageRole(), len(response.Content), response.Usage.Input, response.Usage.CacheRead, response.Usage.Output, response.ErrorMessage)
			}
			var text strings.Builder
			for _, block := range response.Content {
				if value, ok := block.(TextContent); ok {
					text.WriteString(value.Text)
				}
			}
			if strings.TrimSpace(text.String()) == "" {
				t.Fatal("empty response text")
			}
		})
	}
}
