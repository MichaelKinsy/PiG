package ai

import (
	"maps"
	"regexp"
	"strings"
)

// Pig divergence D217: the carried Pi fixes past the 1.4.0+b3a29f11 pin change catalog rows the pinned Pi catalog (the published package, its data
// modules, the oracle's models.generated.js) still has in the old form. A test that compares the Go catalog with the pinned catalog reads the Pi row through
// these helpers, which apply the carried edit and nothing else, so every other member is still compared with Pi.

var d217OpenRouterAnthropicLatest = regexp.MustCompile(`^~anthropic/claude-[a-z]+-latest$`)

// d217CarriedOpenRouterAnthropicLatest is Pi 44f6851b (#9534): the ids of OpenRouter's ~anthropic/claude-*-latest aliases.
func d217CarriedOpenRouterAnthropicLatest(provider, id string) bool {
	return provider == "openrouter" && d217OpenRouterAnthropicLatest.MatchString(id)
}

// d217CarriedPiModel is the pinned catalog's model object with the carried edits of Pi 44f6851b applied: the alias uses the Anthropic Messages API on
// openrouter.ai/api with adaptive thinking in place of its Chat Completions compat.
func d217CarriedPiModel(model map[string]any) map[string]any {
	provider, _ := model["provider"].(string)
	id, _ := model["id"].(string)
	if !d217CarriedOpenRouterAnthropicLatest(provider, id) || model["api"] != "openai-completions" {
		return model
	}
	carried := maps.Clone(model)
	carried["api"] = "anthropic-messages"
	carried["baseUrl"] = strings.TrimSuffix(model["baseUrl"].(string), "/v1")
	carried["compat"] = map[string]any{"forceAdaptiveThinking": true}
	return carried
}

var d217CompatMember = regexp.MustCompile(`,"compat":\{[^{}]*\}`)

// d217CarriedPiJSON is d217CarriedPiModel over the model's JSON text, which keeps Pi's member order.
func d217CarriedPiJSON(provider, id, raw string) string {
	if !d217CarriedOpenRouterAnthropicLatest(provider, id) || !strings.Contains(raw, `"api":"openai-completions"`) {
		return raw
	}
	raw = strings.Replace(raw, `"api":"openai-completions"`, `"api":"anthropic-messages"`, 1)
	raw = strings.Replace(raw, `"baseUrl":"https://openrouter.ai/api/v1"`, `"baseUrl":"https://openrouter.ai/api"`, 1)
	return d217CompatMember.ReplaceAllLiteralString(raw, `,"compat":{"forceAdaptiveThinking":true}`)
}
