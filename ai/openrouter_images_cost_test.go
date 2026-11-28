package ai

import (
	"encoding/json"
	"testing"
)

// OpenRouter image usage is priced by upstream openrouter-images.ts
// parseUsage; the expected value is Pi 0.99.1's serialized usage for the same
// response usage (test/parity/testdata/ai-sdk-pi.mjs): the published catalog
// prices this model's cache writes at 0.083333 (openrouter-catalog.ts roundCost).
func TestOpenRouterImagesUsageCostMatchesUpstream(t *testing.T) {
	model, ok := GetImageModel(ProviderImagesOpenRouter, "google/gemini-2.5-flash-image")
	if !ok {
		t.Fatal("image model missing")
	}
	var raw openRouterImagesUsage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":10,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":1}}`), &raw); err != nil {
		t.Fatal(err)
	}
	assertUsageJSON(t, *parseOpenRouterImagesUsage(raw, model),
		`{"input":6,"output":3,"cacheRead":3,"cacheWrite":1,"reasoning":0,"totalTokens":13,"cost":{"input":0.0000018,"output":0.00009,"cacheRead":8.999999999999999e-8,"cacheWrite":8.333300000000001e-8,"total":0.000091973333,"totalFromProvider":false}}`)
}
