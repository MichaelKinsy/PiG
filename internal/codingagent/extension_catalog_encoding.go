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

// extensionCatalogEncoding caches only the model projection: the chat catalog and the typed (image and classifier) models. It compares an owned snapshot of the composed metadata and registration order for the chat catalog, and drops the typed encoding on every committed registry change (invalidateTyped, called from the registry change observer that also publishes the catalog) or chat catalog change; provider auth and errors are read on every publication. The cache is owned by one bridge binding and retains one catalog, not a history of revisions.
type extensionCatalogEncoding struct {
	mu     sync.Mutex
	input  []*ai.Model
	models json.RawMessage
	// typed is the encoding of registry.extensionTypedModels for typedRegistry, valid while typedGeneration is unchanged since it was computed.
	typed           json.RawMessage
	typedProviders  []string
	typedRegistry   *ModelRegistry
	typedGeneration uint64
}

// invalidateTyped drops the cached typed-model encoding after a committed registry change. A computation that started before the change does not store its result.
func (c *extensionCatalogEncoding) invalidateTyped() {
	c.mu.Lock()
	c.typedGeneration++
	c.typed = nil
	c.mu.Unlock()
}

// typedModels returns the encoding of the registry's typed models and their providers, composing the typed providers only when no encoding is retained for registry.
func (c *extensionCatalogEncoding) typedModels(registry *ModelRegistry) (json.RawMessage, []string) {
	if registry == nil {
		return nil, nil
	}
	c.mu.Lock()
	if c.typed != nil && c.typedRegistry == registry {
		typed, providers := json.RawMessage(bytes.Clone(c.typed)), c.typedProviders
		c.mu.Unlock()
		return typed, providers
	}
	generation := c.typedGeneration
	c.mu.Unlock()
	list := registry.extensionTypedModels()
	typed, err := json.Marshal(list)
	if err != nil {
		return nil, nil
	}
	providers := typedModelProviders(list)
	c.mu.Lock()
	if c.typedGeneration == generation {
		c.typed, c.typedProviders, c.typedRegistry = typed, providers, registry
	}
	c.mu.Unlock()
	return typed, providers
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
		c.typedGeneration++
		c.typed = nil
	}
	models := json.RawMessage(bytes.Clone(c.models))
	c.mu.Unlock()
	typed, typedProviders := c.typedModels(registry)
	return extensionRegistryState(registry, models, providers, typed, typedProviders)
}

func (c *extensionCatalogEncoding) uncached(registry *ModelRegistry, catalog []*ai.Model) map[string]any {
	c.mu.Lock()
	c.models = nil
	c.input = nil
	c.typedGeneration++
	c.typed = nil
	c.mu.Unlock()
	return ExtensionModelRegistryState(registry, catalog)
}
