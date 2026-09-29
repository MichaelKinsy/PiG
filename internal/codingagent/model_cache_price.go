package codingagent

import "github.com/MichaelKinsy/PiG/ai"

// CacheReadPrice reads composed model pricing without resolving credentials, executing configuration commands, or performing network work.
func (r *ModelRegistry) CacheReadPrice(providerID, modelID string) float64 {
	rate := 0.0
	if model, ok := ai.LookupModelExact(providerID + "/" + modelID); ok {
		rate = model.CacheReadCost
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if provider := r.radiusProviderLocked(providerID); provider != nil {
		rate = 0
		for _, model := range provider.GetModels() {
			if model.ID == modelID {
				rate = model.Cost.CacheRead
				break
			}
		}
	}
	var configured *providerConfig
	if r.config != nil {
		if provider, ok := r.config.Providers[providerID]; ok {
			configured = &provider
		}
	}
	applyDefinition := func(provider providerConfig) {
		if definition, found := findModelDefinition(provider.Models, modelID); found {
			rate = 0
			if definition.Cost != nil {
				rate = definition.Cost.CacheRead
			}
		}
	}
	applyOverride := func(provider providerConfig) {
		if override, ok := provider.ModelOverrides[modelID]; ok && override.Cost != nil && override.Cost.CacheRead != nil {
			rate = *override.Cost.CacheRead
		}
	}
	if configured != nil {
		applyDefinition(*configured)
		applyOverride(*configured)
	}
	if dynamic, ok := r.dynamic[providerID]; ok {
		if dynamic.Models != nil {
			if _, found := findModelDefinition(dynamic.Models, modelID); !found {
				return 0
			}
			applyDefinition(dynamic)
		}
		applyOverride(dynamic)
	}
	if configured != nil {
		applyOverride(*configured)
	}
	return rate
}
