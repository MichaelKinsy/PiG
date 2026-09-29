package codingagent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// extensionCatalogEncoding caches only the model projection. It compares an owned snapshot of the composed metadata and registration order; provider auth and errors are read on every publication. The cache is owned by one bridge binding and retains one catalog, not a history of revisions.
type extensionCatalogEncoding struct {
	mu     sync.Mutex
	input  []*ai.Model
	models json.RawMessage
}

func (c *extensionCatalogEncoding) state(registry *ModelRegistry, catalog []*ai.Model) map[string]any {
	for _, model := range catalog {
		if model == nil {
			continue
		}
		// ModelInfo owns fallback Provider.ID evaluation; a cache key must not invoke it.
		if (model.ProviderMeta.ProviderID == "" && model.Provider != nil) || !catalogMapCacheable(model.SamplingParams) {
			return c.uncached(registry, catalog)
		}
		if compat := model.ProviderMeta.Compat; compat != nil &&
			(!catalogMapCacheable(compat.OpenRouterRouting) || !catalogMapCacheable(compat.ChatTemplateKwargs) ||
				!catalogMapCacheable(compat.VercelGatewayRouting) || !catalogMapCacheable(compat.ChatTemplateArgs)) {
			return c.uncached(registry, catalog)
		}
	}
	ordered := make([]*ai.Model, 0, len(catalog))
	providers := make([]string, 0, len(catalog))
	for _, model := range catalog {
		if model == nil {
			continue
		}
		ordered = append(ordered, model)
		providers = append(providers, model.ProviderMeta.ProviderID)
	}
	if registry != nil {
		if compare := registry.extensionModelOrder(); compare != nil {
			slices.SortStableFunc(ordered, func(a, b *ai.Model) int {
				return compare(a.ProviderMeta.ProviderID, a.ID, b.ProviderMeta.ProviderID, b.ID)
			})
		}
	}
	c.mu.Lock()
	if c.models == nil || !equalCatalogModels(c.input, ordered) {
		normalized := make([]ai.Model, len(ordered))
		values := make([]*ai.Model, len(ordered))
		for i, model := range ordered {
			normalized[i] = *model
			normalized[i].Provider = nil
			values[i] = &normalized[i]
		}
		input, ok := cloneCatalogInput(reflect.ValueOf(values))
		if !ok {
			c.mu.Unlock()
			return c.uncached(registry, catalog)
		}
		snapshot := input.Interface().([]*ai.Model)
		models := make([]map[string]any, 0, len(snapshot))
		for _, model := range snapshot {
			models = append(models, extension.ModelInfo(model))
		}
		encoded, err := json.Marshal(models)
		if err != nil {
			c.mu.Unlock()
			return c.uncached(registry, catalog)
		}
		c.models = encoded
		c.input = snapshot
	}
	models := json.RawMessage(bytes.Clone(c.models))
	c.mu.Unlock()
	return extensionRegistryState(registry, models, providers)
}

func (c *extensionCatalogEncoding) uncached(registry *ModelRegistry, catalog []*ai.Model) map[string]any {
	c.mu.Lock()
	c.models = nil
	c.input = nil
	c.mu.Unlock()
	return ExtensionModelRegistryState(registry, catalog)
}
