//go:build live

package ai

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// upstream: packages/ai/test/openai-codex-cache-affinity-e2e.test.ts:9
func TestOpenAICodexCacheAffinityLiveUpstream(t *testing.T) {
	t.Run("handles SSE requests with aligned cache-affinity identifiers/live-only", func(t *testing.T) {
		runCacheAffinityCase(t, true, liveProviderKey(t, "openai-codex"), "")
	})
}

// upstream: packages/ai/test/openai-responses-cache-affinity-e2e.test.ts:6
func TestOpenAIResponsesCacheAffinityLiveUpstream(t *testing.T) {
	t.Run("handles direct OpenAI Responses requests with aligned cache-affinity identifiers/live-only", func(t *testing.T) {
		runCacheAffinityCase(t, false, liveProviderKey(t, "openai"), "")
	})
}

func TestResponsesReasoningReplayLiveUpstream(t *testing.T) {
	for _, tc := range reasoningReplayCases() {
		t.Run(tc.name+"/live-only", func(t *testing.T) {
			openaiKey := liveProviderKey(t, "openai")
			anthropicKey := ""
			if tc.anthropic {
				anthropicKey = liveProviderKey(t, "anthropic")
			}
			runReasoningReplayCase(t, t.Context(), tc.aborted, tc.anthropic, openaiKey, anthropicKey, "")
		})
	}
}

func TestOpenAIResponsesToolResultImagesLiveUpstream(t *testing.T) {
	for _, tc := range responsesImageCases() {
		t.Run(tc.provider+"/should send tool result images in function_call_output/live-only", func(t *testing.T) {
			key := liveProviderKey(t, tc.provider)
			base := ""
			if tc.provider == "azure-openai-responses" {
				// upstream: packages/ai/test/azure-utils.ts:18-22 accepts either a base URL or a resource name.
				base = os.Getenv("AZURE_OPENAI_BASE_URL")
				if base == "" && os.Getenv("AZURE_OPENAI_RESOURCE_NAME") == "" {
					testenv.RequireLiveEnv(t, "AZURE_OPENAI_BASE_URL", "AZURE_OPENAI_RESOURCE_NAME")
				}
			}
			image, err := os.ReadFile("testdata/upstream-red-circle.png")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			runResponsesImageCase(t, ctx, tc.provider, tc.model, key, base, base64.StdEncoding.EncodeToString(image), false)
		})
	}
}
