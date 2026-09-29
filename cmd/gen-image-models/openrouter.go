package main

// Ports packages/ai/scripts/generate-image-models.ts (parseOpenRouterImageModels).

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// parseOpenRouterImageModels retains API order and filters duplicate or unsupported modalities before testing image output.
func parseOpenRouterImageModels(payload json.RawMessage, strict bool) ([]imageModel, error) {
	var root map[string]json.RawMessage
	var records []json.RawMessage
	if err := json.Unmarshal(payload, &root); err == nil {
		_ = json.Unmarshal(root["data"], &records)
	}
	models := []imageModel{}
	if len(records) == 0 {
		if strict {
			return nil, errors.New("OpenRouter API returned a missing or empty image model list")
		}
		return models, nil
	}
	for _, raw := range records {
		var record struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Architecture *struct {
				InputModalities  []any `json:"input_modalities"`
				OutputModalities []any `json:"output_modalities"`
			} `json:"architecture"`
			Pricing map[string]string `json:"pricing"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("decode OpenRouter image model: %w", err)
		}
		var input, output []string
		if record.Architecture != nil {
			input = imageModalities(record.Architecture.InputModalities)
			output = imageModalities(record.Architecture.OutputModalities)
		}
		if !slices.Contains(output, "image") {
			continue
		}
		if len(input) == 0 {
			input = []string{"text"}
		}
		model := imageModel{ID: record.ID, Name: record.Name, API: "openrouter-images", Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Input: input, Output: output}
		model.Cost.Input = imageModelPrice(record.Pricing["prompt"])
		model.Cost.Output = imageModelPrice(record.Pricing["completion"])
		model.Cost.CacheRead = imageModelPrice(record.Pricing["input_cache_read"])
		model.Cost.CacheWrite = imageModelPrice(record.Pricing["input_cache_write"])
		models = append(models, model)
	}
	if strict && len(models) == 0 {
		return nil, errors.New("OpenRouter API returned no usable image models")
	}
	return models, nil
}

func imageModalities(values []any) []string {
	out := []string{}
	for _, value := range values {
		if modality, ok := value.(string); ok && (modality == "text" || modality == "image") && !slices.Contains(out, modality) {
			out = append(out, modality)
		}
	}
	return out
}

var imagePricePrefix = regexp.MustCompile(`^[+-]?(?:Infinity|(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)`)

// imageModelPrice follows parseFloat's longest decimal prefix, not Number's whole-string conversion.
func imageModelPrice(value string) float64 {
	if value == "" {
		return 0
	}
	prefix := imagePricePrefix.FindString(strings.TrimLeft(value, " \t\n\r\v\f\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"))
	if prefix == "" {
		return math.NaN()
	}
	price, _ := strconv.ParseFloat(prefix, 64)
	return price * 1_000_000
}
