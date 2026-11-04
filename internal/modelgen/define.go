package modelgen

// Ports packages/ai/catalog/define.ts

// ChatModel is a chat model entry: a pi `Model` object, member order as the catalog constructs it.
type ChatModel = *Obj

// CompatFunc returns the compat defaults for one model of an API. A fixed default returns a fresh copy for every model.
type CompatFunc func(model *Obj) *Obj

// FixedCompat is compat defaults that do not vary by model.
func FixedCompat(compat *Obj) CompatFunc {
	return func(*Obj) *Obj { return compat.Clone() }
}

// ApiCompatDefaults are compat defaults per API. They sit under each model's own compat, so model values win.
type ApiCompatDefaults map[string]CompatFunc

// ProviderContext is passed to every provider hook.
type ProviderContext struct {
	// ProviderID is the provider being built.
	ProviderID string
	Sources    *CatalogSources
	// Strict fails instead of degrading when upstream data is unexpected.
	Strict bool

	providerModels     func(providerID string) ([]ChatModel, error)
	modelsDevSource    func(model ChatModel) *ModelsDevModel
	recordModelsDevSrc func(model ChatModel, source *ModelsDevModel)
}

// ProviderModels returns copies of another provider's chat models before shared rules run.
func (c *ProviderContext) ProviderModels(providerID string) ([]ChatModel, error) {
	return c.providerModels(providerID)
}

// ModelsDevSource returns the models.dev entry a model was built from, if it was recorded.
func (c *ProviderContext) ModelsDevSource(model ChatModel) *ModelsDevModel {
	return c.modelsDevSource(model)
}

// RecordModelsDevSource records the models.dev entry so shared rules can use its reasoning metadata.
func (c *ProviderContext) RecordModelsDevSource(model ChatModel, source *ModelsDevModel) {
	c.recordModelsDevSrc(model, source)
}

// ProviderDefinition is one built-in provider's catalog. The build runs, in order:
//  1. Models: upstream models plus authored ones, with data corrections.
//  2. Compat defaults and the shared model-family rules in the families files.
//  3. Finalize: provider-specific behavior that must win over shared rules.
//  4. Shared normalization (image input limits), then FinalizeAll.
type ProviderDefinition struct {
	ID          string
	Models      func(ctx *ProviderContext) ([]ChatModel, error)
	Images      func(ctx *ProviderContext) ([]*Obj, error)
	Classifiers func(ctx *ProviderContext) ([]*Obj, error)
	Compat      ApiCompatDefaults
	Finalize    func(model ChatModel, ctx *ProviderContext)
	FinalizeAll func(models []ChatModel, ctx *ProviderContext)
}

// MergeCompat shallow-merges compat flags into a model.
func MergeCompat(model ChatModel, compat *Obj) {
	model.Set("compat", Spread(model.Object("compat"), compat))
}

// MergeThinking shallow-merges thinking level mappings into a model. Later values win.
func MergeThinking(model ChatModel, levels *Obj) {
	model.Set("thinkingLevelMap", Spread(model.Object("thinkingLevelMap"), levels))
}

// WithInputLimits sets provider input limits for image-capable models. Limits the model already carries take precedence.
func WithInputLimits(model ChatModel, limits *Obj) {
	if !model.Includes("input", "image") {
		return
	}
	existing := model.Object("inputLimits")
	model.Set("inputLimits", Spread(limits, existing).Set("images", Spread(limits.Object("images"), existing.Object("images"))))
}

// AddMissing adds models only when upstream has not listed them (yet).
func AddMissing(models, additions []ChatModel) []ChatModel {
	for _, addition := range additions {
		listed := false
		for _, model := range models {
			if model.Str("id") == addition.Str("id") {
				listed = true
				break
			}
		}
		if !listed {
			models = append(models, addition)
		}
	}
	return models
}

// ModelsDevOptions configures ModelsDevModels.
type ModelsDevOptions struct {
	API     string
	BaseURL string
	Compat  *Obj
	Headers *Obj
	// Protocol keeps only models models.dev lists on this API protocol (e.g. "responses").
	Protocol string
	// SkipDeprecated skips models models.dev marks deprecated.
	SkipDeprecated bool
	// NoRecordReasoning is `recordReasoning: false`: models.dev effort levels are not exposed through the shared rules.
	NoRecordReasoning bool
	// DefaultContextWindow and DefaultMaxTokens are fallback limits when models.dev has none; 4096 each when zero.
	DefaultContextWindow, DefaultMaxTokens float64
	// Include is an extra filter. Models without tool support are always skipped.
	Include func(source *ModelsDevModel) bool
	// Map adjusts or replaces the mapped model. Returning nil skips the model.
	Map func(model ChatModel, source *ModelsDevModel) (ChatModel, error)
}

// ModelsDevModels maps one models.dev provider's tool-capable chat models onto pi models.
func ModelsDevModels(ctx *ProviderContext, sourceProvider string, options ModelsDevOptions) ([]ChatModel, error) {
	models := []ChatModel{}
	for _, source := range ModelsDevChatModels(ctx.Sources.ModelsDev, sourceProvider) {
		if !source.ToolCall {
			continue
		}
		if options.Protocol != "" && !hasProtocol(source, options.Protocol) {
			continue
		}
		if options.SkipDeprecated && source.Status == "deprecated" {
			continue
		}
		if options.Include != nil && !options.Include(source) {
			continue
		}
		name := source.Name
		if name == "" {
			name = source.ID
		}
		input := Strings("text")
		if source.ImageInput {
			input = Strings("text", "image")
		}
		contextWindow := firstNonZero(source.ContextWindow, options.DefaultContextWindow, 4096)
		maxTokens := firstNonZero(source.MaxTokens, options.DefaultMaxTokens, 4096)
		base := O(
			"id", source.ID,
			"name", name,
			"api", options.API,
			"provider", ctx.ProviderID,
			"baseUrl", options.BaseURL,
			"reasoning", source.Reasoning,
			"input", input,
			"cost", ModelsDevToCost(source.Cost),
			"contextWindow", contextWindow,
			"maxTokens", maxTokens,
		)
		if options.Headers != nil {
			base.Set("headers", options.Headers.Clone())
		}
		if options.Compat != nil {
			base.Set("compat", options.Compat.Clone())
		}
		model := base
		if options.Map != nil {
			mapped, err := options.Map(base, source)
			if err != nil {
				return nil, err
			}
			model = mapped
		}
		if model == nil {
			continue
		}
		if !options.NoRecordReasoning {
			ctx.RecordModelsDevSource(model, source)
		}
		models = append(models, model)
	}
	return models, nil
}

func hasProtocol(source *ModelsDevModel, protocol string) bool {
	for _, api := range source.APIs {
		if api.Protocol == protocol {
			return true
		}
	}
	return false
}

// firstNonZero is `a || b || c`.
func firstNonZero(values ...float64) float64 {
	for _, value := range values {
		if truthy(value) {
			return value
		}
	}
	return values[len(values)-1]
}
