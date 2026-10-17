package ai

import (
	"net/http"
	"testing"
)

// Upstream 0.99.1 prices the "fast" service tier only for OpenAI Responses (openai-responses.ts getServiceTierCostMultiplier). The Codex multiplier
// has no "fast" case (openai-codex-responses.ts getServiceTierCostMultiplier), so a Codex response that reports it is billed at the default rate.
func TestCodexBillsTheFastServiceTierAtTheDefaultRate(t *testing.T) {
	for _, tc := range []struct{ requested, returned string }{{"fast", "fast"}, {"priority", "fast"}} {
		t.Run("requested "+tc.requested+", returned "+tc.returned, func(t *testing.T) {
			provider := codexUpstreamProvider(t, "gpt-5.5", codexRoundTripper(func(*http.Request) (*http.Response, error) {
				return codexUpstreamHTTP(`data: {"type":"response.completed","response":{"status":"completed","service_tier":"` + tc.returned + `","usage":{"input_tokens":1000000,"output_tokens":1000000,"total_tokens":2000000,"input_tokens_details":{"cached_tokens":0}}}}` + "\n\n"), nil
			}))
			stream, err := provider.Stream(t.Context(), codexUpstreamContext(), StreamOptions{Transport: TransportSSE, ServiceTier: tc.requested, ModelCost: ModelCost{Input: 1, Output: 2}})
			if err != nil {
				t.Fatal(err)
			}
			if cost := stream.Result().Usage.Cost; cost.Input != 1 || cost.Output != 2 || cost.Total != 3 {
				t.Fatalf("cost = %#v, want the default rate", cost)
			}
		})
	}
}
