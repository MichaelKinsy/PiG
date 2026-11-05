package modelgen

// Ports packages/ai/catalog/build.ts

import (
	"fmt"
)

// ProviderCatalog is one provider's built models by type. Chat entries carry `type: "chat"` last.
type ProviderCatalog struct {
	ID         string
	Chat       []*Obj
	Image      []*Obj
	Classifier []*Obj
}

// Keep the generated default no less restrictive than coding-agent's historical image preprocessing. Provider limits can narrow
// this profile, but unknown providers retain the cache-safe 2000px / 4.5 MiB behavior.
func defaultImageResize() *Obj {
	return O("maxWidth", 2000, "maxHeight", 2000, "maxBytes", 4.5*1024*1024, "jpegQuality", 80)
}

func modelKey(model *Obj) string { return model.Str("provider") + ":" + model.Str("id") }

func applyCompatDefaults(model ChatModel, defaults ApiCompatDefaults) {
	entry := defaults[model.Str("api")]
	if entry == nil {
		return
	}
	model.Set("compat", Spread(entry(model), model.Object("compat")))
}

// sendsNativeEffort reports whether the model's API sends models.dev effort values to the host as-is.
func sendsNativeEffort(model ChatModel) bool {
	switch model.Str("api") {
	case "anthropic-messages":
		return model.Object("compat").member("forceAdaptiveThinking") == true
	case "openai-responses", "azure-openai-responses", "openai-codex-responses":
		return true
	case "openai-completions":
	default:
		return false
	}
	compat := model.Object("compat")
	thinkingFormat := "openai"
	if value, ok := compat.member("thinkingFormat").(string); ok {
		thinkingFormat = value
	}
	supportsReasoningEffort := true
	if value := compat.member("supportsReasoningEffort"); value != nil {
		supportsReasoningEffort = truthy(value)
	}
	return thinkingFormat == "openai" && supportsReasoningEffort
}

func applyModelsDevEffort(model ChatModel, source *ModelsDevModel) {
	if source == nil || !sendsNativeEffort(model) {
		return
	}
	if levels := GetEffortThinkingLevelMap(source.ReasoningOptions); levels != nil {
		MergeThinking(model, levels)
	}
}

func applyImageResizeDefaults(model *Obj) {
	if !model.Includes("input", "image") {
		return
	}
	limits := model.Object("inputLimits")
	images := limits.Object("images")
	model.Set("inputLimits", Spread(limits).Set("images", Spread(images).Set("resize", Spread(defaultImageResize(), images.Object("resize")))))
}

// BuildCatalog runs every provider's definition, in the order given, against the fetched sources.
func BuildCatalog(definitions []ProviderDefinition, sources *CatalogSources, strict bool) ([]ProviderCatalog, error) {
	byID := map[string]*ProviderDefinition{}
	for i := range definitions {
		byID[definitions[i].ID] = &definitions[i]
	}
	modelsDevSources := map[string]*ModelsDevModel{}
	stageOne := map[string][]ChatModel{}
	building := map[string]bool{}

	var buildModels func(providerID string) ([]ChatModel, error)
	context := func(providerID string) *ProviderContext {
		return &ProviderContext{
			ProviderID: providerID,
			Sources:    sources,
			Strict:     strict,
			providerModels: func(id string) ([]ChatModel, error) {
				models, err := buildModels(id)
				return CloneAll(models), err
			},
			modelsDevSource: func(model ChatModel) *ModelsDevModel { return modelsDevSources[modelKey(model)] },
			recordModelsDevSrc: func(model ChatModel, source *ModelsDevModel) {
				modelsDevSources[modelKey(model)] = source
			},
		}
	}
	buildModels = func(providerID string) ([]ChatModel, error) {
		if built, ok := stageOne[providerID]; ok {
			return built, nil
		}
		definition := byID[providerID]
		if definition == nil {
			return nil, fmt.Errorf("Unknown provider: %s", providerID)
		}
		if building[providerID] {
			return nil, fmt.Errorf("Provider %s depends on itself", providerID)
		}
		building[providerID] = true
		var models []ChatModel
		if definition.Models != nil {
			var err error
			if models, err = definition.Models(context(providerID)); err != nil {
				return nil, err
			}
		}
		delete(building, providerID)
		for _, model := range models {
			if model.Str("provider") != providerID {
				return nil, fmt.Errorf("%s produced %s for another provider", providerID, modelKey(model))
			}
		}
		stageOne[providerID] = models
		return models, nil
	}

	for _, definition := range definitions {
		if _, err := buildModels(definition.ID); err != nil {
			return nil, err
		}
	}

	catalogs := make([]ProviderCatalog, 0, len(definitions))
	for _, definition := range definitions {
		ctx := context(definition.ID)
		chat := []ChatModel{}
		seen := map[string]bool{}
		for _, model := range stageOne[definition.ID] {
			// The first source listing a model wins.
			if seen[model.Str("id")] {
				continue
			}
			seen[model.Str("id")] = true
			chat = append(chat, model)
		}

		for _, model := range chat {
			applyCompatDefaults(model, definition.Compat)
			applyDeepSeekCompatRules(model)
			applyModelsDevEffort(model, ctx.ModelsDevSource(model))
			applyOpenAIFamilyRules(model)
			applyClaudeFamilyRules(model)
			applyDeepSeekFamilyRules(model)
		}
		if definition.Finalize != nil {
			for _, model := range chat {
				definition.Finalize(model, ctx)
			}
		}
		for _, model := range chat {
			if compat := model.Object("compat"); compat != nil && compat.Len() == 0 {
				model.Delete("compat")
			}
			applyImageResizeDefaults(model)
		}
		if definition.FinalizeAll != nil {
			definition.FinalizeAll(chat, ctx)
		}

		var image, classifier []*Obj
		var err error
		if definition.Images != nil {
			if image, err = definition.Images(ctx); err != nil {
				return nil, err
			}
		}
		for _, model := range image {
			applyImageResizeDefaults(model)
		}
		if definition.Classifiers != nil {
			if classifier, err = definition.Classifiers(ctx); err != nil {
				return nil, err
			}
		}
		catalog := ProviderCatalog{ID: definition.ID, Image: image, Classifier: classifier}
		for _, model := range chat {
			catalog.Chat = append(catalog.Chat, Spread(model).Set("type", "chat"))
		}
		catalogs = append(catalogs, catalog)
	}
	return catalogs, nil
}
