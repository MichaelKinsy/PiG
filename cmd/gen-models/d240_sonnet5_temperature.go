package main

import (
	"bytes"
	"encoding/json"
	"strings"
)

// pig divergence (D240): Pi 4b9777737 marks Claude Sonnet 5 as not supporting temperature. Pi 1.5.0's published provider data (the source of the
// generated Go catalogs) predates it, so the Sonnet 5 entries of an anthropic-messages provider get `"supportsTemperature": false` in their compat, where
// internal/modelgen/family_anthropic.go puts it for a fresh generation. An entry that already sets the key (Sonnet 5.5) is untouched, so the step does
// nothing once the pinned package contains 4b9777737; remove it then.
//
// The compat keeps Pi's member order: the key follows forceAdaptiveThinking, as the generator's MergeCompat order puts it.
func applyD240SonnetFiveTemperature(encoded json.RawMessage) (json.RawMessage, error) {
	var probe struct {
		ID     string          `json:"id"`
		API    string          `json:"api"`
		Compat json.RawMessage `json:"compat"`
	}
	if err := json.Unmarshal(encoded, &probe); err != nil || probe.API != "anthropic-messages" {
		return encoded, nil //nolint:nilerr // decodeJSONModel reports a malformed entry
	}
	id := strings.ToLower(probe.ID)
	if !strings.Contains(id, "sonnet-5") && !strings.Contains(id, "sonnet.5") {
		return encoded, nil
	}
	if len(probe.Compat) == 0 || string(probe.Compat) == "null" {
		// Pi's MergeCompat creates the compat object when the model has none.
		var entry bytes.Buffer
		if err := json.Compact(&entry, encoded); err != nil {
			return nil, err
		}
		return json.RawMessage(strings.TrimSuffix(entry.String(), "}") + `,"compat":{"supportsTemperature":false}}`), nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(probe.Compat, &members); err != nil {
		return encoded, nil //nolint:nilerr // decodeJSONModel reports a malformed entry
	}
	if _, set := members["supportsTemperature"]; set {
		return encoded, nil
	}
	var compat, entry bytes.Buffer
	if err := json.Compact(&compat, probe.Compat); err != nil {
		return nil, err
	}
	if err := json.Compact(&entry, encoded); err != nil {
		return nil, err
	}
	const adaptive = `"forceAdaptiveThinking":true`
	patched := compat.String()
	switch {
	case strings.Contains(patched, adaptive):
		patched = strings.Replace(patched, adaptive, adaptive+`,"supportsTemperature":false`, 1)
	case patched == "{}":
		patched = `{"supportsTemperature":false}`
	default:
		patched = strings.TrimSuffix(patched, "}") + `,"supportsTemperature":false}`
	}
	return json.RawMessage(strings.Replace(entry.String(), compat.String(), patched, 1)), nil
}
