package ai

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"

	"github.com/MichaelKinsy/PiG/internal/jsonopts"
)

type orderedJSONEntry struct {
	key   string
	value json.RawMessage
}

// orderedJSONObject decodes one JSON object into its entries in source order. A repeated key keeps the position of its
// first occurrence and the value of its last, as JSON.parse does.
func orderedJSONObject(data []byte) ([]orderedJSONEntry, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(data), jsonopts.Parse)
	token, err := decoder.ReadToken()
	if err != nil {
		return nil, err
	}
	if token.Kind() != '{' {
		return nil, errors.New("not a JSON object")
	}
	var entries []orderedJSONEntry
	index := map[string]int{}
	for decoder.PeekKind() != '}' {
		name, err := jsonopts.ReadName(decoder) // the JavaScript key: "\ud800" and "\ud801" stay two keys, as in JSON.parse
		if err != nil {
			return nil, err
		}
		value, err := decoder.ReadValue()
		if err != nil {
			return nil, err
		}
		raw := json.RawMessage(bytes.Clone(value))
		if position, seen := index[name]; seen {
			entries[position].value = raw
			continue
		}
		index[name] = len(entries)
		entries = append(entries, orderedJSONEntry{key: jsonopts.ValidName(name), value: raw})
	}
	if _, err := decoder.ReadToken(); err != nil {
		return nil, err
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON object")
	}
	return entries, nil
}

// jsonObjectOf decodes raw as a JSON object. A non-object (including an array or null) reports false.
func jsonObjectOf(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil, false
	}
	return object, true
}

// jsonStringOf reads a JSON string. Any other value, including null and an absent member, reports false, like a
// JavaScript typeof check. json.Unmarshal alone would accept null as the empty string.
func jsonStringOf(raw json.RawMessage) (string, bool) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

// jsonArrayOf decodes raw as a JSON array, like Array.isArray on the parsed value. Null and an absent member report
// false; json.Unmarshal alone would accept null as an empty slice.
func jsonArrayOf(raw json.RawMessage) ([]json.RawMessage, bool) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, false
	}
	return values, true
}

// finiteNumberOf reads a JSON number that is a finite JavaScript number.
func finiteNumberOf(raw json.RawMessage) (float64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' || trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == 't' || trimmed[0] == 'f' || trimmed[0] == 'n' {
		return 0, false
	}
	var value float64
	if json.Unmarshal(trimmed, &value) != nil {
		return 0, false
	}
	return value, true
}
