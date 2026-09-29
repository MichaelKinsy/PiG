package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/modelgen"
)

// Ports packages/ai/scripts/generate-models.ts
// Raw vendor snapshots update reasoning controls before emitting the Go catalog; unspecified models retain the pinned catalog metadata.
func applyVendorReasoning(rows []modelRow, modelsDevPath, openRouterPath string) error {
	if modelsDevPath != "" {
		data, err := os.ReadFile(modelsDevPath)
		if err != nil {
			return err
		}
		var providers map[string]struct {
			Models map[string]struct {
				ReasoningOptions []modelgen.ModelsDevReasoningOption `json:"reasoning_options"`
			} `json:"models"`
		}
		if err := json.Unmarshal(data, &providers); err != nil {
			return fmt.Errorf("models.dev reasoning: %w", err)
		}
		for i := range rows {
			provider := rows[i].Provider
			if provider == "google-vertex" {
				provider = "google"
			}
			if model, ok := providers[provider].Models[rows[i].ID]; ok {
				rows[i].ThinkingLevelMap = modelgen.GetEffortThinkingLevelMap(model.ReasoningOptions)
			}
		}
	}
	if openRouterPath != "" {
		data, err := os.ReadFile(openRouterPath)
		if err != nil {
			return err
		}
		var response struct {
			Data []struct {
				ID        string                                `json:"id"`
				Reasoning *modelgen.OpenRouterReasoningMetadata `json:"reasoning"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			return fmt.Errorf("OpenRouter reasoning: %w", err)
		}
		metadata := map[string]*modelgen.OpenRouterReasoningMetadata{}
		for _, model := range response.Data {
			metadata[model.ID] = model.Reasoning
		}
		for i := range rows {
			if rows[i].Provider == "openrouter" {
				if value, ok := metadata[rows[i].ID]; ok {
					rows[i].ThinkingLevelMap = modelgen.GetOpenRouterThinkingLevelMap(value)
				}
			}
		}
	}
	return nil
}
