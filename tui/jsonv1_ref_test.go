package tui

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

func extractColorKeyOrderV1(data []byte) []string {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	// Find the "colors" key at the top level.
	var keys []string
	depth := 0
	inColors := false
	for {
		t, err := dec.Token()
		if err != nil {
			break
		}
		switch v := t.(type) {
		case json.Delim:
			switch v {
			case '{':
				if inColors && depth == 1 {
					// We're inside the colors object at depth 2.
					// Read key-value pairs.
					for dec.More() {
						kt, err := dec.Token()
						if err != nil {
							return keys
						}
						if key, ok := kt.(string); ok {
							keys = append(keys, key)
						}
						// Skip the value.
						if _, err := dec.Token(); err != nil {
							return keys
						}
					}
					return keys
				}
				depth++
			case '}':
				depth--
				inColors = false
			case '[':
				// skip arrays
			case ']':
				// skip arrays
			}
		case string:
			if depth == 1 && v == "colors" {
				inColors = true
			} else {
				inColors = false
			}
		}
	}
	return keys
}

func rawObjectKeysV1(raw []byte) []string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	var keys []string
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return keys
		}
		key, _ := token.(string)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return keys
		}
	}
	return keys
}

func toolCallArgEntriesV1(args json.RawMessage) (entries []toolCallArg, ok bool) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return nil, false
	}
	// JSON.parse then JSON.stringify: source order except integer keys first, the last of a repeated key, JS number text.
	canonical, err := jsonstringify.Canonicalize(trimmed)
	if err != nil || string(canonical) == "null" {
		return nil, false
	}
	if canonical[0] != '{' {
		return []toolCallArg{{key: "args", json: canonical}}, true
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		entries = append(entries, toolCallArg{key: key.(string), json: value})
	}
	return entries, true
}
