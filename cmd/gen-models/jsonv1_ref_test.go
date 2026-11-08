package main

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

func parseDataJSONV1(path string) ([]modelRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode catalog root: %w", err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("catalog root is %v, want object", token)
	}
	var models []jsonModel
	seen := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		api, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("catalog key is %T, want string", keyToken)
		}
		var encoded json.RawMessage
		if err := decoder.Decode(&encoded); err != nil {
			return nil, fmt.Errorf("%s: %w", api, err)
		}
		group, err := decodeJSONModelGroupV1(encoded, seen)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", api, err)
		}
		models = append(models, group...)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	rows := make([]modelRow, 0, len(models))
	for _, jm := range models {
		row := modelRow{
			Type:             jm.Type,
			ID:               jm.ID,
			Provider:         jm.Provider,
			Name:             jm.Name,
			API:              jm.API,
			BaseURL:          jm.BaseURL,
			Headers:          jm.Headers,
			Compat:           normalizeCompat(jm.Compat),
			ThinkingLevelMap: jm.ThinkingLevelMap,
			SamplingParams:   jm.SamplingParams,
			PromptCache:      jm.PromptCache,
			InputLimits:      jm.InputLimits,
			ContextWindow:    jm.ContextWindow,
			MaxTokens:        jm.MaxTokens,
			Reasoning:        jm.Reasoning,
			Inputs:           jm.Input,
			Output:           jm.Output,
			Enabled:          jm.Enabled,
			Lab:              jm.Lab,
			Providers:        jm.Providers,
			Record:           jm.record,
		}
		if jm.Cost != nil {
			row.InputCost = jm.Cost.Input
			row.OutputCost = jm.Cost.Output
			row.CacheRead = jm.Cost.CacheRead
			row.CacheWrite = jm.Cost.CacheWrite
			row.Tiers = jm.Cost.Tiers
		}
		rows = append(rows, row)
	}
	return rows, nil
}

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
		encoded, err = applyD240SonnetFiveTemperature(encoded)
		if err != nil {
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
