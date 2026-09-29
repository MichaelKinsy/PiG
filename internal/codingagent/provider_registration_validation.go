package codingagent

// Ports packages/coding-agent/src/core/model-runtime.ts

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// validateExtensionRegistration validates incoming model data against the builtin/models.json base before replacing a registration. Callback ownership remains in providerConfigFromRegistration; validation never invokes or serializes a stream callback.
func (r *ModelRegistry) validateExtensionRegistration(id string, config extension.ProviderConfig, decoded providerConfig) error {
	// upstream: packages/coding-agent/src/core/model-runtime.ts:registerProvider
	if config.StreamSimple != nil && config.API == "" {
		return fmt.Errorf(`Provider %s: "api" is required when registering streamSimple.`, id)
	}
	input := ProviderConfigInput{Name: config.Name, BaseURL: config.BaseURL, API: config.API}
	if config.Models != nil {
		input.Models = make([]*ai.Model, 0, len(decoded.Models))
		for _, definition := range decoded.Models {
			input.Models = append(input.Models, nativeModelFromEntry(modelDefinitionEntry(id, decoded, definition)))
		}
	}
	base := &ai.ModelsProvider{ID: id, Name: id, GetModels: func() ([]*ai.Model, error) {
		models := make([]*ai.Model, 0)
		for _, generated := range ai.ListModels(id) {
			model := generated.ToModel()
			model.Capabilities = generated.ToCapabilities()
			models = append(models, model)
		}
		return models, nil
	}}
	_, err := r.composeNativeProvider(base, &input)
	return err
}
