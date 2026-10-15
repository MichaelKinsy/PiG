package ai

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// resolveSamplingParamsInOrder is ResolveSamplingParams plus the order in which the spread {...model.samplingParams, ...levelParams,
// ...requestParams} (simple-options.ts:31-33) enumerates the result: the model's keys, then the level's new keys, then the request's new
// keys. A Go map keeps no insertion order, so the keys that come from one source are enumerated in sorted order.
func resolveSamplingParamsInOrder(model *Model, thinkingLevel ModelThinkingLevel, requestParams SamplingParams) ([]string, SamplingParams) {
	merged := ResolveSamplingParams(model, thinkingLevel, requestParams)
	if merged == nil {
		return nil, nil
	}
	var sources []SamplingParams
	if model != nil {
		sources = append(sources, model.SamplingParams, model.SamplingParamsByThinkingLevel[ClampThinkingLevel(model, thinkingLevel)])
	}
	sources = append(sources, requestParams)
	keys := make([]string, 0, len(merged))
	seen := make(map[string]bool, len(merged))
	for _, source := range sources {
		for _, key := range slices.Sorted(maps.Keys(source)) {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys, merged
}

// assignSamplingParams is Object.assign(params, samplingParams) over the encoded request: a key the request already has keeps its position
// and takes the new value, and a new key follows the request's keys in the order keys gives. literal names the keys of buildParams' object
// literal in order. A literal key whose value was undefined is absent from the encoded request, but it holds its place in Pi's object, so a
// sampling parameter that sets it lands there.
func assignSamplingParams(request any, literal, keys []string, params SamplingParams) (*orderedjson.Object, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal sampling base: %w", err)
	}
	base, err := orderedjson.Parse(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode sampling base: %w", err)
	}
	values := make(map[string]json.RawMessage, len(keys))
	for _, key := range keys {
		value, err := json.Marshal(params[key])
		if err != nil {
			return nil, fmt.Errorf("marshal sampling parameter %q: %w", key, err)
		}
		values[key] = value
	}
	assigned := orderedjson.New()
	for _, key := range literal {
		if value, ok := base.Get(key); ok {
			assigned.Set(key, value)
		} else if value, ok := values[key]; ok {
			assigned.Set(key, value)
		}
	}
	for _, key := range base.Keys() {
		if !assigned.Has(key) {
			value, _ := base.Get(key)
			assigned.Set(key, value)
		}
	}
	for _, key := range keys {
		assigned.Set(key, values[key])
	}
	return assigned, nil
}

// oaiRequestLiteralKeys are the keys of the object literal openai-completions.ts buildParams starts params with (:822-831).
var oaiRequestLiteralKeys = []string{"model", "messages", "stream", "prompt_cache_key", "prompt_cache_retention"}

// respRequestLiteralKeys are the keys of the object literal openai-responses.ts buildParams starts params with (:330-338).
var respRequestLiteralKeys = []string{"model", "input", "stream", "prompt_cache_key", "prompt_cache_retention", "prompt_cache_options", "store"}
