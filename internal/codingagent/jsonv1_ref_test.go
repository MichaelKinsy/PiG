package codingagent

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/text"
)

func decodeOrderedHeaderEntriesV1(data []byte) ([]orderedHeaderEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("model configuration must be an object")
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if key != "headers" {
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				return nil, err
			}
			continue
		}
		return decodeOrderedHeaderObjectV1(decoder)
	}
	return nil, nil
}

func decodeOrderedHeaderObjectV1(decoder *json.Decoder) ([]orderedHeaderEntry, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("headers must be an object")
	}
	var entries []orderedHeaderEntry
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value *string
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		entries = append(entries, orderedHeaderEntry{Name: name.(string), Value: value})
	}
	_, err = decoder.Token()
	return entries, err
}

func orderedObjectValuesV1(data json.RawMessage) ([]json.RawMessage, map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return nil, nil, err
	}
	var values []json.RawMessage
	byKey := map[string]json.RawMessage{}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, err
		}
		values = append(values, value)
		byKey[name.(string)] = value
	}
	return values, byKey, nil
}

func modelsJSONProviderOrderV1(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(stripJSONComments(text.StripBom(string(data)))), &top) != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(top["providers"]))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	var order []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return order
		}
		key, _ := token.(string)
		order = append(order, key)
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return order
		}
	}
	return order
}
