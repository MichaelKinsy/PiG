package evals

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"encoding/json"
	"slices"
	"strings"
)

func sortedKeysInInsertionOrderV1(text string, record map[string]any) []string {
	decoder := json.NewDecoder(strings.NewReader(text))
	var keys []string
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return sortedKeys(record)
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if key, ok := token.(string); ok && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
		var skipped json.RawMessage
		if decoder.Decode(&skipped) != nil {
			break
		}
	}
	return keys
}
