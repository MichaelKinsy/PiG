package ai

// Ports packages/ai/src/types.ts.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// openAICompatMembers are the compat members OpenAICompat models.
var openAICompatMembers = sync.OnceValue(func() map[string]struct{} { return orderedjson.JSONNames(reflect.TypeFor[OpenAICompat]()) })

// MarshalJSON preserves an explicit empty fallback list, which disables server-side fallback rather than inheriting catalog defaults, and
// writes the unmodeled members (Extra) after the modeled ones.
func (compat OpenAICompat) MarshalJSON() ([]byte, error) {
	type fields OpenAICompat
	var fallbacks *[]AnthropicAllowedFallbackModel
	if compat.AllowedFallbackModels != nil {
		fallbacks = &compat.AllowedFallbackModels
	}
	data, err := json.Marshal(struct {
		fields
		AllowedFallbackModels *[]AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
	}{fields: fields(compat), AllowedFallbackModels: fallbacks})
	if err != nil {
		return nil, err
	}
	return orderedjson.AppendMembers(data, compat.Extra)
}

// UnmarshalJSON decodes the modeled members and keeps every other member of the object in Extra, as Pi's compat object keeps an
// additional property.
func (compat *OpenAICompat) UnmarshalJSON(data []byte) error {
	type fields OpenAICompat
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(data, (*fields)(compat)); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	compat.Extra = nil
	known := openAICompatMembers()
	for name, value := range members {
		if _, ok := known[name]; ok {
			continue
		}
		if compat.Extra == nil {
			compat.Extra = make(map[string]json.RawMessage)
		}
		compat.Extra[name] = value
	}
	return nil
}
