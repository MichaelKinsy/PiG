package main

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// extensionScopedModels materializes only an explicit scope. Extension getters read the Session's retained result instead of resolving models again.
func extensionScopedModels(services *coding.Services, patterns []string) []extension.ScopedModel {
	// upstream: packages/coding-agent/src/main.ts:main
	if len(patterns) == 0 {
		return []extension.ScopedModel{}
	}
	modelRuntime := services.ModelRuntime()
	runtime := newStartupModelRuntime(modelRuntime.RuntimeModels(), modelRuntime.HasConfiguredAuth)
	scoped, _ := resolveModelScopeFromModels(patterns, runtime.getAvailable())
	result := make([]extension.ScopedModel, 0, len(scoped))
	for _, entry := range scoped {
		model := modelRuntime.GetModel(entry.Model.Provider, entry.Model.ID)
		if model != nil {
			result = append(result, extension.ScopedModel{Model: model, ThinkingLevel: ai.ThinkingLevel(entry.ThinkingLevel)})
		}
	}
	return result
}
