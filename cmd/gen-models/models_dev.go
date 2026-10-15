package main

// Ports packages/ai/scripts/generate-models.ts (models.dev Fireworks and Qwen Token Plan stages).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	Type             string             `json:"type"`
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
	return generatedCatalogModel{Type: "chat", ID: id, Name: name, Provider: provider, Reasoning: source.Reasoning, Input: input,
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
	openRouter, err := fetchOpenRouterModels(ctx, options.Strict)
	if err != nil {
		return err
	}
	aiGateway, err := fetchAiGatewayModels(ctx, aiGatewayModelsURL, options.Strict)
	if err != nil {
		return err
	}
	// models.dev takes priority over OpenRouter, and both over the AI Gateway, for an id they list (generate-models.ts:2723-2725 order, 3443 `??=`).
	catalogs := generatedCatalogs{Chat: make(map[string]map[string]generatedCatalogModel), Image: make(map[string]map[string]openRouterImageModel), Classifier: make(map[string]map[string]openRouterClassifierModel)}
	for _, model := range slices.Concat(models, openRouter.Chat, aiGateway.Chat) {
		if catalogs.Chat[model.Provider] == nil {
			catalogs.Chat[model.Provider] = make(map[string]generatedCatalogModel)
		}
		if _, exists := catalogs.Chat[model.Provider][model.ID]; !exists {
			catalogs.Chat[model.Provider][model.ID] = model
		}
	}
	for _, model := range openRouter.Images {
		if catalogs.Image[model.Provider] == nil {
			catalogs.Image[model.Provider] = make(map[string]openRouterImageModel)
		}
		if _, exists := catalogs.Image[model.Provider][model.ID]; !exists {
			catalogs.Image[model.Provider][model.ID] = model
		}
	}
	for _, model := range slices.Concat(openRouter.Classifiers, aiGateway.Classifiers) {
		if catalogs.Classifier[model.Provider] == nil {
			catalogs.Classifier[model.Provider] = make(map[string]openRouterClassifierModel)
		}
		if _, exists := catalogs.Classifier[model.Provider][model.ID]; !exists {
			catalogs.Classifier[model.Provider][model.ID] = model
		}
	}
	// Validation and serialization precede any mutation, as in upstream's staged generation.
	if !options.JSONOnly {
		var chat []generatedCatalogModel
		for _, provider := range slices.Sorted(maps.Keys(catalogs.Chat)) {
			for _, id := range slices.Sorted(maps.Keys(catalogs.Chat[provider])) {
				chat = append(chat, catalogs.Chat[provider][id])
			}
		}
		rows := modelsDevRows(chat)
		if len(rows) == 0 {
			return fmt.Errorf("gen-models: parsed 0 models: refusing to clobber output")
		}
		if err := emit(options.GoOutput, "models.dev", rows); err != nil {
			return err
		}
	}
	if options.JSONOutput != "" {
		return writeModelsDevCatalog(options.JSONOutput, catalogs, options.Pretty)
	}
	return nil
}

// generatedCatalogs is the hydrated catalog by provider id, then model id, per model type.
type generatedCatalogs struct {
	Chat       map[string]map[string]generatedCatalogModel
	Image      map[string]map[string]openRouterImageModel
	Classifier map[string]map[string]openRouterClassifierModel
}

func fetchOpenRouterList(ctx context.Context, query string) ([]openRouterModelListItem, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models"+query, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenRouter API returned %d", response.StatusCode)
	}
	var list struct {
		Data []openRouterModelListItem `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list.Data, nil
}

// fetchOpenRouterModels ports generate-models.ts:1306-1327. The three listings are fetched concurrently and joined; the
// first failing listing in request order is reported. A failure is fatal under strict and otherwise reported on stderr
// with an empty catalog.
func fetchOpenRouterModels(ctx context.Context, strict bool) (openRouterCatalog, error) {
	queries := []string{"", "?output_modalities=image", "?output_modalities=decisions"}
	lists := make([][]openRouterModelListItem, len(queries))
	errs := make([]error, len(queries))
	var group sync.WaitGroup
	for i, query := range queries {
		group.Go(func() { lists[i], errs[i] = fetchOpenRouterList(ctx, query) })
	}
	group.Wait()
	var err error
	for _, listErr := range errs {
		if listErr != nil {
			err = listErr
			break
		}
	}
	if err == nil {
		catalog := buildOpenRouterCatalog(lists[0], lists[1], lists[2])
		if strict && len(catalog.Images) == 0 {
			err = errors.New("OpenRouter API returned no usable image models")
		} else {
			return catalog, nil
		}
	}
	fmt.Fprintln(os.Stderr, "Failed to fetch OpenRouter models:", err)
	if strict {
		return openRouterCatalog{}, err
	}
	return openRouterCatalog{}, nil
}

func modelsDevRows(models []generatedCatalogModel) []modelRow {
	var rows []modelRow
	for _, model := range models {
		rows = append(rows, modelRow{Type: model.Type, ID: model.ID, Provider: model.Provider, Name: model.Name, API: model.API, BaseURL: model.BaseURL, Compat: model.Compat, ThinkingLevelMap: model.ThinkingLevelMap,
			ContextWindow: model.ContextWindow, MaxTokens: model.MaxTokens, InputCost: model.Cost.Input, OutputCost: model.Cost.Output, CacheRead: model.Cost.CacheRead, CacheWrite: model.Cost.CacheWrite, Reasoning: model.Reasoning, Inputs: model.Input})
	}
	return rows
}

// writeModelsDevCatalog writes generate-models.ts:3486-3505's --json-output tree: models.json and providers/<id>.json keep
// the chat catalog keyed by id; the .all variants list every type, chat then image then classifier, so an id can appear
// once per type.
func writeModelsDevCatalog(root string, catalogs generatedCatalogs, pretty bool) error {
	idSet := make(map[string]bool)
	for id := range catalogs.Chat {
		idSet[id] = true
	}
	for id := range catalogs.Image {
		idSet[id] = true
	}
	for id := range catalogs.Classifier {
		idSet[id] = true
	}
	ids := slices.Sorted(maps.Keys(idSet))
	all := make(map[string][]any, len(ids))
	chat := make(map[string]map[string]generatedCatalogModel, len(ids))
	for _, id := range ids {
		chat[id] = catalogs.Chat[id]
		if chat[id] == nil {
			chat[id] = map[string]generatedCatalogModel{}
		}
		for _, modelID := range slices.Sorted(maps.Keys(catalogs.Chat[id])) {
			all[id] = append(all[id], catalogs.Chat[id][modelID])
		}
		for _, modelID := range slices.Sorted(maps.Keys(catalogs.Image[id])) {
			all[id] = append(all[id], catalogs.Image[id][modelID])
		}
		for _, modelID := range slices.Sorted(maps.Keys(catalogs.Classifier[id])) {
			all[id] = append(all[id], catalogs.Classifier[id][modelID])
		}
		if all[id] == nil {
			all[id] = []any{}
		}
	}
	files := map[string]any{"models.json": chat, "models.all.json": all, "providers.json": ids}
	for _, id := range ids {
		files[filepath.Join("providers", id+".json")] = chat[id]
		files[filepath.Join("providers", id+".all.json")] = all[id]
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
