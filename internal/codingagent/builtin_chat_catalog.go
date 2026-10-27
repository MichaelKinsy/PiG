package codingagent

import (
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// builtinChatCatalogs holds the offered bundled chat models of each provider read so far, for the stripped APIs they were listed under. The bundled catalog never changes, so callers share one list per provider until the stripped APIs change, as Pi's getBuiltinModels shares its catalog objects (providers/all.ts:104).
type builtinChatCatalogs struct {
	stripped   []string
	mu         sync.RWMutex
	byProvider map[string][]ai.GeneratedModel
	// openRouterListed is the OpenRouter list builtinChatCatalogListed derives, built on first use.
	openRouterListed []ai.GeneratedModel
}

var currentBuiltinChatCatalogs atomic.Pointer[builtinChatCatalogs]

// builtinChatCatalog returns the models of Pi's bundled catalog that ai.ListModels(id) lists, without the OpenRouter routing variants it derives (D199; builtinChatCatalogListed lists them), in the same order, copying them at most once rather than on every call. The list is shared, and may be a window of ai.GeneratedModels itself, so callers read it and never write an element; its capacity is clipped, so an append copies it.
func builtinChatCatalog(id string) []ai.GeneratedModel {
	stripped := pigstrip.IDs(pigstrip.ListAPIs)
	catalogs := currentBuiltinChatCatalogs.Load()
	for catalogs == nil || !slices.Equal(catalogs.stripped, stripped) {
		fresh := &builtinChatCatalogs{stripped: stripped, byProvider: map[string][]ai.GeneratedModel{}}
		if currentBuiltinChatCatalogs.CompareAndSwap(catalogs, fresh) {
			catalogs = fresh
			break
		}
		catalogs = currentBuiltinChatCatalogs.Load()
	}
	catalogs.mu.RLock()
	models, listed := catalogs.byProvider[id]
	catalogs.mu.RUnlock()
	if listed {
		return models
	}
	// The models of a stripped API are left out, as ai.ListModels leaves them out.
	offered := func(model *ai.GeneratedModel) bool {
		return model.Provider == id && !slices.Contains(stripped, string(model.API))
	}
	count, first, last := 0, -1, -1
	for i := range ai.GeneratedModels {
		if offered(&ai.GeneratedModels[i]) {
			count++
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if count == 0 {
		// Only a bundled provider is kept, so names that list nothing do not grow the map.
		return nil
	}
	if count == last-first+1 {
		// The generated catalog lists a provider's models together, so the list is usually a window of it and needs no copy.
		models = ai.GeneratedModels[first : last+1 : last+1]
	} else {
		models = make([]ai.GeneratedModel, 0, count)
		for i := range ai.GeneratedModels {
			if offered(&ai.GeneratedModels[i]) {
				models = append(models, ai.GeneratedModels[i])
			}
		}
	}
	catalogs.mu.Lock()
	defer catalogs.mu.Unlock()
	if earlier, listed := catalogs.byProvider[id]; listed {
		return earlier
	}
	catalogs.byProvider[id] = models
	return models
}

// builtinChatCatalogListed is builtinChatCatalog for a caller that lists the catalog's models: the bundled catalog of OpenRouter also holds each
// model's routing variants, derived once from the bundled models (ai.WithOpenRouterRoutingVariants) the first time the list is read, so a caller that
// only resolves a model or counts the catalog (builtinChatCatalog) builds none. The list is shared, and read-only like builtinChatCatalog's.
//
// pig divergence (D199): Pi lists no OpenRouter routing variant; PiG derives them from the bundled models instead of storing them.
func builtinChatCatalogListed(id string) []ai.GeneratedModel {
	catalog := builtinChatCatalog(id)
	if id != "openrouter" || len(catalog) == 0 { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
		return catalog
	}
	catalogs := currentBuiltinChatCatalogs.Load()
	catalogs.mu.RLock()
	listed := catalogs.openRouterListed
	catalogs.mu.RUnlock()
	if listed != nil {
		return listed
	}
	listed = ai.WithOpenRouterRoutingVariants(catalog)
	listed = listed[:len(listed):len(listed)]
	catalogs.mu.Lock()
	defer catalogs.mu.Unlock()
	if catalogs.openRouterListed != nil {
		return catalogs.openRouterListed
	}
	catalogs.openRouterListed = listed
	return listed
}
