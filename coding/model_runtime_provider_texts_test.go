// SPDX-License-Identifier: MIT

package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/models.ts:832,859 and packages/coding-agent/src/core/model-runtime.ts:662,668. A request through the model runtime for a provider
// with no credentials fails with ModelsError("Provider is not configured: <id>"); a model whose provider the runtime does not know fails with
// ModelsError("Unknown provider: <id>"). The text is the sentence the user reads (print stderr, RPC error, compaction/turn error message).
func TestModelRuntimeRequestFailureTextsAreThoseOfPi(t *testing.T) {
	for _, tc := range []struct{ name, provider string }{
		{"API key provider", "openai"},
		{"OAuth provider", "github-copilot"},
		{"OpenAI-compatible provider", "groq"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"OPENAI_API_KEY", "GROQ_API_KEY", "COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
				t.Setenv(key, "")
			}
			services, _ := nativeCompatServices(t, "", nil)
			runtime := services.ModelRuntime()
			models := runtime.GetModels(tc.provider)
			if len(models) == 0 {
				t.Fatalf("no models for %s", tc.provider)
			}
			request := ai.Context{Messages: []ai.Message{}}
			results := map[string]*ai.AssistantMessage{
				"Stream":         runtime.Stream(t.Context(), models[0], request, ai.StreamOptions{}).Result(),
				"StreamSimple":   runtime.StreamSimple(t.Context(), models[0], request, ai.StreamOptions{}).Result(),
				"CompleteSimple": runtime.CompleteSimple(t.Context(), models[0], request, ai.StreamOptions{}),
			}
			for name, result := range results {
				if result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, "Provider is not configured: "+tc.provider) || strings.Contains(result.ErrorMessage, "provider is not configured") {
					t.Errorf("%s: stopReason=%s error=%q", name, result.StopReason, result.ErrorMessage)
				}
			}
			// A model object the runtime has no provider for (a bare object).
			bare := &ai.Model{ID: "ghost", ProviderMeta: ai.ProviderMetadata{ProviderID: tc.provider}}
			result := runtime.CompleteSimple(t.Context(), bare, request, ai.StreamOptions{})
			if result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, "Unknown provider: "+tc.provider) || strings.Contains(result.ErrorMessage, "unknown provider") {
				t.Errorf("bare model: stopReason=%s error=%q", result.StopReason, result.ErrorMessage)
			}
		})
	}
}
