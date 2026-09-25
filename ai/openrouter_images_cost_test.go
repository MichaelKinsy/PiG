package ai

import (
	"encoding/json"
	"testing"
)

// OpenRouter image usage is priced by upstream openrouter-images.ts
// parseUsage; the expected value is Pi 0.87.1's serialized usage for the same
// response usage (parity/testdata/ai-sdk-pi.mjs).
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
		`{"input":6,"output":3,"cacheRead":3,"cacheWrite":1,"totalTokens":13,"cost":{"input":1.8e-06,"output":7.500000000000001e-06,"cacheRead":8.999999999999999e-08,"cacheWrite":8.33333333333333e-08,"total":9.473333333333333e-06}}`)
}
