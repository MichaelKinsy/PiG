package main

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func decodeJSONModelGroupV1(data []byte, seen map[string]string) ([]jsonModel, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode model group: %w", err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("model group root is %v, want object", token)
	}
	var models []jsonModel
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("model key is %T, want string", keyToken)
		}
		var encoded json.RawMessage
		if err := decoder.Decode(&encoded); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		model, err := decodeJSONModel(encoded)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, encoded); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		model.record = compact.String()
		if key != model.Type+":"+model.ID {
			return nil, fmt.Errorf("%s: mismatched type/id identity (model is %s:%s)", key, model.Type, model.ID)
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%s appears in more than one API group", key)
		}
		seen[key] = model.API
		models = append(models, model)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return models, nil
}
