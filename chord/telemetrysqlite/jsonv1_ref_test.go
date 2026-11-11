package telemetrysqlite

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/telemetry"
)

func decodeAttributesV1(text string) (telemetry.SpanAttributes, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("telemetry-sqlite: attributes are not an object")
	}
	attributes := telemetry.SpanAttributes{}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		value, err := decodeAttributeValue(raw)
		if err != nil {
			return nil, err
		}
		if value != nil {
			attributes = attributes.Set(name.(string), value)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return attributes, nil
}
