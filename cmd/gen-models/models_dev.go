package main

// Ports packages/ai/scripts/generate-models.ts (models.dev Fireworks and Qwen Token Plan stages).

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type modelsDevModel struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	ToolCall         bool                       `json:"tool_call"`
	Reasoning        bool                       `json:"reasoning"`
	ReasoningOptions []modelsDevReasoningOption `json:"reasoning_options"`
	Limit            struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cache_read"`
		CacheWrite float64 `json:"cache_write"`
	} `json:"cost"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
}

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

type generatedCatalogModel struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	API              string             `json:"api"`
	Provider         string             `json:"provider"`
	BaseURL          string             `json:"baseUrl"`
	Reasoning        bool               `json:"reasoning"`
	Input            []string           `json:"input"`
	Cost             jsonCost           `json:"cost"`
	ContextWindow    int                `json:"contextWindow"`
	MaxTokens        int                `json:"maxTokens"`
	Compat           *ModelCompat       `json:"compat,omitempty"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap,omitempty"`
}

func modelsDevCommon(id, provider string, source modelsDevModel) generatedCatalogModel {
	name := source.Name
	if name == "" {
		name = id
	}
	input := []string{"text"}
	if slices.Contains(source.Modalities.Input, "image") {
		input = append(input, "image")
	}
	contextWindow, maxTokens := source.Limit.Context, source.Limit.Output
	if contextWindow == 0 {
		contextWindow = 4096
	}
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return generatedCatalogModel{ID: id, Name: name, Provider: provider, Reasoning: source.Reasoning, Input: input,
		Cost: jsonCost{Input: source.Cost.Input, Output: source.Cost.Output, CacheRead: source.Cost.CacheRead, CacheWrite: source.Cost.CacheWrite}, ContextWindow: contextWindow, MaxTokens: maxTokens}
}

// upstream: packages/ai/scripts/generate-models.ts:1665-1748
func processFireworksModels(provider modelsDevProvider) []generatedCatalogModel {
	var models []generatedCatalogModel
	for _, id := range slices.Sorted(maps.Keys(provider.Models)) {
		source := provider.Models[id]
		if !source.ToolCall {
			continue
		}
		model := modelsDevCommon(id, "fireworks", source)
		if strings.Contains(id, "glm-") || strings.Contains(id, "kimi-k3") {
			model.API, model.BaseURL = "openai-completions", "https://api.fireworks.ai/inference/v1"
			model.Compat = &ModelCompat{SupportsStore: new(false), SupportsDeveloperRole: new(false), SendSessionAffinityHeaders: new(true), SupportsLongCacheRetention: new(false)}
			if strings.Contains(id, "kimi-k3") {
				model.Compat.RequiresReasoningContentOnAssistantMessages = new(true)
				model.Compat.ThinkingFormat = "openai"
			}
			model.ThinkingLevelMap = getEffortThinkingLevelMap(source.ReasoningOptions)
		} else {
			model.API, model.BaseURL = "anthropic-messages", "https://api.fireworks.ai/inference"
			model.Compat = &ModelCompat{AllowEmptySignature: new(true), SendSessionAffinityHeaders: new(true), SupportsEagerToolInputStreaming: new(false), SupportsCacheControlOnTools: new(false), SupportsLongCacheRetention: new(false)}
			// upstream: packages/ai/scripts/generate-models.ts:299-305
			fallback := slices.Contains([]string{"accounts/fireworks/models/deepseek-v4-flash-0731", "accounts/fireworks/models/deepseek-v4-flash-vision-exp", "accounts/fireworks/models/deepseek-v4-pro-0813", "accounts/fireworks/models/qwen3p8-max", "accounts/fireworks/models/qwen3p8-2p4t-a95b"}, id)
			if fallback || slices.ContainsFunc(source.ReasoningOptions, func(option modelsDevReasoningOption) bool { return option.Type == "effort" }) {
				model.Compat.ForceAdaptiveThinking = new(true)
				model.ThinkingLevelMap = getEffortThinkingLevelMap(source.ReasoningOptions)
			}
		}
		applyFireworksThinkingLevelMetadata(&model, source.ReasoningOptions)
		models = append(models, model)
	}
	return models
}

// upstream: packages/ai/scripts/generate-models.ts:1145-1175
func applyFireworksThinkingLevelMetadata(model *generatedCatalogModel, options []modelsDevReasoningOption) {
	merge := func(values map[string]*string) {
		if model.ThinkingLevelMap == nil {
			model.ThinkingLevelMap = make(map[string]*string)
		}
		maps.Copy(model.ThinkingLevelMap, values)
	}
	if model.API == "anthropic-messages" && model.Compat.ForceAdaptiveThinking != nil && *model.Compat.ForceAdaptiveThinking {
		if model.ID == "accounts/fireworks/models/qwen3p8-max" && model.ThinkingLevelMap == nil {
			model.ThinkingLevelMap = getEffortThinkingLevelMap([]modelsDevReasoningOption{{Type: "effort", Values: []*string{new("low"), new("medium"), new("xhigh")}}})
		}
		if model.ID == "accounts/fireworks/models/qwen3p8-2p4t-a95b" || slices.ContainsFunc(options, func(option modelsDevReasoningOption) bool { return option.Type == "toggle" }) {
			merge(map[string]*string{"off": new("none")})
		}
		if model.ID == "accounts/fireworks/models/deepseek-v4-pro-0813" {
			merge(map[string]*string{"low": new("low")})
		}
	}
	if strings.Contains(model.ID, "glm-5p2") {
		merge(map[string]*string{"off": new("none"), "minimal": nil, "low": nil, "medium": nil, "max": new("max")})
	}
	if strings.Contains(model.ID, "kimi-k3") {
		merge(map[string]*string{"medium": nil})
	}
}

// upstream: packages/ai/scripts/generate-models.ts:325-335
var qwenTokenPlanIndividualModelIDs = []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2", "qwen3.6-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-flash", "qwen3.8-max"}

// upstream: packages/ai/scripts/generate-models.ts:2584-2653
func processQwenTokenPlanModels(catalog map[string]modelsDevProvider, strict bool) ([]generatedCatalogModel, error) {
	var models []generatedCatalogModel
	for _, variant := range []struct {
		source, provider, baseURL string
		ids                       []string
	}{
		{"alibaba-token-plan", "qwen-token-plan", "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1", nil},
		{"alibaba-token-plan", "qwen-token-plan-individual", "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1", qwenTokenPlanIndividualModelIDs},
		{"alibaba-token-plan-cn", "qwen-token-plan-cn", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", nil},
	} {
		var emitted []string
		provider := catalog[variant.source]
		for _, id := range slices.Sorted(maps.Keys(provider.Models)) {
			source := provider.Models[id]
			if !source.ToolCall || id == "qwen3.8-max-preview" || (variant.ids != nil && !slices.Contains(variant.ids, id)) {
				continue
			}
			thinking := getEffortThinkingLevelMap(source.ReasoningOptions)
			if thinking == nil && (id == "glm-5" || id == "glm-5.1") {
				thinking = map[string]*string{"minimal": nil, "low": nil, "medium": nil, "high": new("high"), "xhigh": nil, "max": new("max")}
			}
			model := modelsDevCommon(id, variant.provider, source)
			model.API, model.BaseURL, model.ThinkingLevelMap = "openai-completions", variant.baseURL, thinking
			model.Compat = &ModelCompat{ThinkingFormat: "qwen", SupportsDeveloperRole: new(false), SupportsStore: new(false), SupportsReasoningEffort: new(thinking != nil)}
			models = append(models, model)
			emitted = append(emitted, id)
		}
		if strict && variant.ids != nil {
			var missing []string
			for _, id := range variant.ids {
				if !slices.Contains(emitted, id) {
					missing = append(missing, id)
				}
			}
			if len(missing) != 0 {
				slices.Sort(missing)
				return nil, fmt.Errorf("%s model IDs do not match (missing: %s)", variant.provider, strings.Join(missing, ", "))
			}
		}
	}
	return models, nil
}

type modelsDevGeneratorOptions struct {
	Strict, JSONOnly, Pretty bool
	JSONOutput, GoOutput     string
}

func generateModelsDev(ctx context.Context, options modelsDevGeneratorOptions) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://models.dev/api.json", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("models.dev API returned %d", response.StatusCode)
	}
	var catalog map[string]modelsDevProvider
	if err := json.NewDecoder(response.Body).Decode(&catalog); err != nil {
		return err
	}
	models := processFireworksModels(catalog["fireworks-ai"])
	qwen, err := processQwenTokenPlanModels(catalog, options.Strict)
	if err != nil {
		return err
	}
	models = append(models, qwen...)
	providers := make(map[string]map[string]generatedCatalogModel)
	for _, model := range models {
		if providers[model.Provider] == nil {
			providers[model.Provider] = make(map[string]generatedCatalogModel)
		}
		providers[model.Provider][model.ID] = model
	}
	// Validation and serialization precede any mutation, as in upstream's staged generation.
	if !options.JSONOnly {
		rows := modelsDevRows(models)
		if len(rows) == 0 {
			return fmt.Errorf("gen-models: parsed 0 models: refusing to clobber output")
		}
		if err := emit(options.GoOutput, "models.dev", rows); err != nil {
			return err
		}
	}
	if options.JSONOutput != "" {
		return writeModelsDevCatalog(options.JSONOutput, providers, options.Pretty)
	}
	return nil
}

func modelsDevRows(models []generatedCatalogModel) []modelRow {
	var rows []modelRow
	for _, model := range models {
		rows = append(rows, modelRow{ID: model.ID, Provider: model.Provider, Name: model.Name, API: model.API, BaseURL: model.BaseURL, Compat: model.Compat, ThinkingLevelMap: model.ThinkingLevelMap,
			ContextWindow: model.ContextWindow, MaxTokens: model.MaxTokens, InputCost: model.Cost.Input, OutputCost: model.Cost.Output, CacheRead: model.Cost.CacheRead, CacheWrite: model.Cost.CacheWrite, Reasoning: model.Reasoning, Inputs: model.Input})
	}
	return rows
}

func writeModelsDevCatalog(root string, providers map[string]map[string]generatedCatalogModel, pretty bool) error {
	ids := slices.Sorted(maps.Keys(providers))
	files := map[string]any{"models.json": providers, "providers.json": ids}
	for _, id := range ids {
		files[filepath.Join("providers", id+".json")] = providers[id]
	}
	encoded := make(map[string][]byte, len(files))
	for path, value := range files {
		var data []byte
		var err error
		if pretty {
			data, err = json.MarshalIndent(value, "", "  ")
		} else {
			data, err = json.Marshal(value)
		}
		if err != nil {
			return err
		}
		encoded[path] = append(data, '\n')
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "providers"), 0o755); err != nil {
		return err
	}
	for _, path := range slices.Sorted(maps.Keys(encoded)) {
		if err := os.WriteFile(filepath.Join(root, path), encoded[path], 0o644); err != nil {
			return err
		}
	}
	return nil
}
