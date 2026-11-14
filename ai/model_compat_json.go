package ai

// Ports packages/ai/src/types.ts.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// openAICompatMembers are the compat members OpenAICompat models.
var openAICompatMembers = sync.OnceValue(func() map[string]struct{} { return orderedjson.JSONNames(reflect.TypeFor[OpenAICompat]()) })

// MarshalJSON writes a lone UTF-16 surrogate as its escape, so a compat copied through its JSON form keeps what its reader kept. It preserves an explicit empty fallback list, which disables server-side fallback rather than inheriting catalog defaults, and
// writes the unmodeled members (Extra) after the modeled ones. openRouterRouting, chatTemplateKwargs and chatTemplateArgs are written in
// the member order the compat records for them.
func (compat OpenAICompat) MarshalJSON() ([]byte, error) {
	type fields OpenAICompat
	var fallbacks *[]AnthropicAllowedFallbackModel
	if compat.AllowedFallbackModels != nil {
		fallbacks = &compat.AllowedFallbackModels
	}
	data, err := jsstring.MarshalKeepingSurrogates(struct {
		fields
		AllowedFallbackModels *[]AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
	}{fields: fields(compat), AllowedFallbackModels: fallbacks})
	if err != nil {
		return nil, err
	}
	if compat.objectOrder != nil {
		if data, err = compat.marshalObjectsInOrder(data); err != nil {
			return nil, err
		}
	}
	return orderedjson.AppendMembers(data, compat.Extra)
}

// UnmarshalJSON decodes the modeled members and keeps every other member of the object in Extra, as Pi's compat object keeps an
// additional property. A lone UTF-16 surrogate of a string or a member name is kept, as JSON.parse keeps it (models.json, an extension's compat). It records the member order of openRouterRouting, chatTemplateKwargs and chatTemplateArgs, which Pi sends or
// enumerates as the object lists them.
func (compat *OpenAICompat) UnmarshalJSON(data []byte) error {
	type fields OpenAICompat
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	if err := jsstring.UnmarshalKeepingSurrogates(data, (*fields)(compat)); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := jsstring.UnmarshalKeepingSurrogates(data, &members); err != nil {
		return err
	}
	order, err := readCompatObjectOrder(members)
	if err != nil {
		return err
	}
	compat.objectOrder = order
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
