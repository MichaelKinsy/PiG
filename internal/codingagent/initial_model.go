package codingagent

// Ports packages/coding-agent/src/core/model-resolver.ts.

// InitialModelRuntime keeps exact catalog lookup and the available snapshot separate. A configured provider may have models that are absent from the available snapshot.
type InitialModelRuntime interface {
	ModelResolverRuntime
	GetModel(providerID, modelID string) *RuntimeModel
	GetAvailableSnapshot() []RuntimeModel
}

// FindInitialModelOptions carries selection inputs without owning a Model Runtime, refreshing its snapshots, or resolving credentials. Empty optional thinking strings represent no selection.
type FindInitialModelOptions struct {
	ScopedModels         []ScopedModel
	IsContinuing         bool
	DefaultProvider      string
	DefaultModelId       string
	DefaultThinkingLevel string
	ModelThinkingLevels  map[string]string
	ModelRuntime         InitialModelRuntime
}

// InitialModelResult preserves selection-dependent thinking. Provider-default, first-available, and no-model fallbacks use DefaultThinkingLevel, not the caller's saved level.
type InitialModelResult struct {
	Model           *RuntimeModel
	ThinkingLevel   string
	FallbackMessage string
}

// FindInitialModel selects new-session scope, authenticated saved default, or an available fallback in that order.
func FindInitialModel(options FindInitialModelOptions) (InitialModelResult, error) {
	runtime := options.ModelRuntime
	if len(options.ScopedModels) > 0 && !options.IsContinuing {
		scoped := &options.ScopedModels[0]
		level := scoped.ThinkingLevel
		if level == "" {
			level = options.ModelThinkingLevels[modelRef(scoped.Model)]
		}
		if level == "" {
			level = options.DefaultThinkingLevel
		}
		if level == "" {
			level = DefaultThinkingLevel
		}
		return InitialModelResult{Model: &scoped.Model, ThinkingLevel: level}, nil
	}
	if options.DefaultProvider != "" && options.DefaultModelId != "" {
		found := runtime.GetModel(options.DefaultProvider, options.DefaultModelId)
		if found != nil && runtime.HasConfiguredAuth(found.Provider) {
			level := options.ModelThinkingLevels[options.DefaultProvider+"/"+options.DefaultModelId]
			if level == "" {
				level = options.DefaultThinkingLevel
			}
			if level == "" {
				level = DefaultThinkingLevel
			}
			return InitialModelResult{Model: found, ThinkingLevel: level}, nil
		}
	}
	available := runtime.GetAvailableSnapshot()
	for _, entry := range DefaultModelPerProviderOrder {
		for index := range available {
			model := &available[index]
			if model.Provider == entry.Provider && model.ID == entry.ModelID {
				return InitialModelResult{Model: model, ThinkingLevel: DefaultThinkingLevel}, nil
			}
		}
	}
	result := InitialModelResult{ThinkingLevel: DefaultThinkingLevel}
	if len(available) > 0 {
		result.Model = &available[0]
	}
	return result, nil
}
