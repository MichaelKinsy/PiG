//go:build parity

package runner

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func parseJSONRecordsV1(output string) ([]any, error) {
	if output == "" {
		return nil, fmt.Errorf("empty JSONL output")
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	records := make([]any, 0, len(lines))
	for i, line := range lines {
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		value, err := decodeJSONValueV1(decoder, line)
		if err != nil {
			return nil, fmt.Errorf("JSONL record %d: %w", i+1, err)
		}
		if _, err := decoder.Token(); err != io.EOF {
			return nil, fmt.Errorf("JSONL record %d has trailing data", i+1)
		}
		if _, ok := value.(map[string]any); !ok {
			return nil, fmt.Errorf("JSONL record %d is not an object", i+1)
		}
		records = append(records, value)
	}
	return records, nil
}

func decodeJSONValueV1(d *json.Decoder, source string) (any, error) {
	token, err := readJSONTokenV1(d, source)
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		value := map[string]any{}
		for d.More() {
			key, err := readJSONTokenV1(d, source)
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not a string")
			}
			if _, ok := value[name]; ok {
				return nil, fmt.Errorf("duplicate JSON key %q", name)
			}
			child, err := decodeJSONValueV1(d, source)
			if err != nil {
				return nil, err
			}
			value[name] = child
		}
		_, err := d.Token()
		return value, err
	case json.Delim('['):
		value := []any{}
		for d.More() {
			child, err := decodeJSONValueV1(d, source)
			if err != nil {
				return nil, err
			}
			value = append(value, child)
		}
		_, err := d.Token()
		return value, err
	default:
		return token, nil
	}
}

func readJSONTokenV1(d *json.Decoder, source string) (any, error) {
	start := d.InputOffset()
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	if value, ok := token.(string); ok {
		raw := strings.TrimLeft(source[start:d.InputOffset()], ",: \t\r\n")
		return jsonStringIdentity(raw, value), nil
	}
	return token, nil
}
