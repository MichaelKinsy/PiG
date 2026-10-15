package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/modelgen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	output := map[string]any{}
	mapping := ai.ThinkingLevelMap{ai.ThinkingOff: nil, ai.ThinkingMinimal: nil, ai.ThinkingLow: new("low"), ai.ThinkingMedium: nil, ai.ThinkingHigh: new("high"), ai.ThinkingXHigh: nil, ai.ThinkingMax: new("max")}
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		for _, name := range []string{"mandatory-unset", "mandatory-low", "optional-unset"} {
			levels := mapping
			if name == "optional-unset" {
				levels = nil
			}
			level := ai.ThinkingLevel("")
			if name == "mandatory-low" {
				level = ai.ThinkingLevelLow
			}
			var provider ai.Provider
			if api == ai.APIOpenAICompletions {
				provider = ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", ProviderID: "openrouter", Model: "stealth/ox-alpha", BaseURL: "https://example.invalid/v1", Compat: &ai.OpenAICompat{ThinkingFormat: "openrouter"}, ThinkingLevelMap: levels})
			} else {
				provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", ProviderID: "openai", Model: "mapped", BaseURL: "https://example.invalid/v1", IsReasoning: true, ThinkingLevelMap: levels})
			}
			captured := false
			sentinel := errors.New("captured")
			_, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), ai.StreamOptions{Thinking: level, IsReasoning: true, OnPayload: func(payload any, _ *ai.Model) (any, error) {
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				var value map[string]any
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, err
				}
				output[string(api)+"/"+name] = value["reasoning"]
				captured = true
				return nil, sentinel
			}})
			if !captured || !errors.Is(err, sentinel) {
				return fmt.Errorf("capture failed: %w", err)
			}
		}
	}
	metadata, err := os.ReadFile("test/parity/testdata/reasoning-options/metadata.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Source string
		Name   string
		Value  json.RawMessage
	}
	if err := json.Unmarshal(metadata, &cases); err != nil {
		return err
	}
	for _, tc := range cases {
		key := "metadata/" + tc.Source + "/" + tc.Name
		if tc.Source == "models-dev" {
			var options []modelgen.ModelsDevReasoningOption
			if err := json.Unmarshal(tc.Value, &options); err != nil {
				return err
			}
			output[key] = modelgen.GetEffortThinkingLevelMap(options)
		} else {
			var reasoning *modelgen.OpenRouterReasoningMetadata
			if err := json.Unmarshal(tc.Value, &reasoning); err != nil {
				return err
			}
			output[key] = modelgen.GetOpenRouterThinkingLevelMap(reasoning)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}
