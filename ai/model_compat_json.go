package ai

// Ports packages/ai/src/types.ts.

import "encoding/json"

// MarshalJSON preserves an explicit empty fallback list, which disables server-side fallback rather than inheriting catalog defaults.
func (compat OpenAICompat) MarshalJSON() ([]byte, error) {
	type fields OpenAICompat
	var fallbacks *[]AnthropicAllowedFallbackModel
	if compat.AllowedFallbackModels != nil {
		fallbacks = &compat.AllowedFallbackModels
	}
	return json.Marshal(struct {
		fields
		AllowedFallbackModels *[]AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
	}{fields: fields(compat), AllowedFallbackModels: fallbacks})
}
