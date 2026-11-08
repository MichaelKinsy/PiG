package models

import "github.com/MichaelKinsy/PiG/ai"

// Ports apps/p2/plugins/models/models.ts.

// CreateP2Models is a host's models: the built-in providers, or in fake mode (demos, tests, docs) the scripted faux
// provider (fake.go).
func CreateP2Models(fake bool, options ai.CreateModelsOptions) *ai.Models {
	if fake {
		return CreateFakeModels(options)
	}
	models := ai.CreateModels(options)
	for _, provider := range ai.BuiltinProviders() {
		models.SetProvider(provider)
	}
	return models
}
