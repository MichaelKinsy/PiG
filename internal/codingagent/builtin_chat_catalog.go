package codingagent

import (
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// builtinChatCatalogs holds the offered bundled chat models of each provider read so far, for the stripped APIs they were listed under. The bundled catalog never changes, so callers share one list per provider until the stripped APIs change, as Pi's getBuiltinModels shares its catalog objects (providers/all.ts:104).
// It is the model-catalog snapshot of the registry's metadata readers: the generation (this value) is replaced as a whole when the stripped APIs change, so a reader that loads it once reads the lists and the converted models of one set of stripped APIs, never a mix.
type builtinChatCatalogs struct {
	// strip is the pigstrip generation the stripped APIs were read at; stripped is those APIs.
	strip      uint64
	stripped   []string
	mu         sync.RWMutex
	byProvider map[string][]ai.GeneratedModel
	// converted is the chat models of each provider's listed catalog as GeneratedModel.ToModel builds them, built once per provider and generation on first read (convertedModels).
	converted map[string]*convertedChatModels
	// openRouterListed is the OpenRouter list builtinChatCatalogListed derives, built on first use.
	openRouterListed []ai.GeneratedModel
	// openRouterIdentified is the OpenRouter catalog and its listing by identity (builtinChatCatalogListing), identified on first use.
	openRouterIdentified *builtinChatCatalogIdentity
}

// convertedChatModels is the converted chat models of one provider of one generation: built once, then read-only.
type convertedChatModels struct {
	once   sync.Once
	models []*ai.Model
}

// builtinChatCatalogIdentity is a bundled catalog and the listing by identity that names its entries.
type builtinChatCatalogIdentity struct {
	catalog []ai.GeneratedModel
	listing []ai.ListedOpenRouterModel
}

var currentBuiltinChatCatalogs atomic.Pointer[builtinChatCatalogs]

// currentBuiltinChatCatalogsNow is the generation for the stripped APIs in force now, replacing an older one. It compares one counter (pigstrip.Generation) and
// reads the stripped IDs only to build a new generation, so a lookup allocates nothing while the strip state is unchanged. The counter is read before the IDs, so
// a generation never claims a strip state newer than the IDs it holds, and a strip recorded between the two is seen by the next call.
func currentBuiltinChatCatalogsNow() *builtinChatCatalogs {
	strip := pigstrip.Generation()
	catalogs := currentBuiltinChatCatalogs.Load()
	for catalogs == nil || catalogs.strip < strip {
		fresh := &builtinChatCatalogs{strip: strip, stripped: pigstrip.IDs(pigstrip.ListAPIs), byProvider: map[string][]ai.GeneratedModel{}, converted: map[string]*convertedChatModels{}}
		if currentBuiltinChatCatalogs.CompareAndSwap(catalogs, fresh) {
			return fresh
		}
		catalogs = currentBuiltinChatCatalogs.Load()
	}
	return catalogs
}

// builtinChatCatalog returns the models of Pi's bundled catalog that ai.ListModels(id) lists, without the OpenRouter routing variants it derives (D199; builtinChatCatalogListed lists them), in the same order, copying them at most once rather than on every call. The list is shared, and may be a window of ai.GeneratedModels itself, so callers read it and never write an element; its capacity is clipped, so an append copies it.
func builtinChatCatalog(id string) []ai.GeneratedModel {
	return currentBuiltinChatCatalogsNow().catalog(id)
}

// catalog is builtinChatCatalog(id) in this generation.
func (catalogs *builtinChatCatalogs) catalog(id string) []ai.GeneratedModel {
	stripped := catalogs.stripped
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

// builtinChatModels is the chat models builtinChatCatalogListed(id) lists, as GeneratedModel.ToModel builds them, in that order: nil when it lists none.
// The conversion runs once per provider and generation of the catalog (the stripped APIs), where each reader converted the list itself before: the composer's
// models and all-models reads and the provider model data of a registration read this one snapshot (the metadata readers of one refresh).
// Each call returns its own slice of its own top-level models, so a reader may replace a model's field or an element as it could a converted model; the
// slices, maps and pointers inside a model are the snapshot's, as Pi's readers share the models of the runtime's snapshot (model-registry.ts getAll, find),
// so a reader copies one before it changes it.
func builtinChatModels(id string) []*ai.Model {
	snapshot := currentBuiltinChatCatalogsNow().convertedModels(id)
	if len(snapshot) == 0 {
		return nil
	}
	models := make([]*ai.Model, len(snapshot))
	for i, model := range snapshot {
		models[i] = new(*model)
	}
	return models
}

// convertedModels is the converted chat models of provider id in this generation, built on first use: the list and the models come from this one generation.
func (catalogs *builtinChatCatalogs) convertedModels(id string) []*ai.Model {
	listed := catalogs.listed(id)
	if len(listed) == 0 {
		return nil
	}
	catalogs.mu.Lock()
	entry := catalogs.converted[id]
	if entry == nil {
		entry = &convertedChatModels{}
		catalogs.converted[id] = entry
	}
	catalogs.mu.Unlock()
	entry.once.Do(func() {
		models := make([]*ai.Model, len(listed))
		for i := range listed {
			models[i] = listed[i].ToModel()
		}
		entry.models = models
	})
	return entry.models
}

// builtinChatCatalogListed is builtinChatCatalog for a caller that lists the catalog's models: the bundled catalog of OpenRouter also holds each
// model's routing variants, derived once from the bundled models (ai.SharedOpenRouterRoutingVariants) the first time the list is read, so a caller that
// only resolves a model or counts the catalog (builtinChatCatalog) builds none. The list is shared, and read-only like builtinChatCatalog's: when no
// OpenRouter model is stripped it is the list ai holds, whose entries are the ones ai.LookupModel returns.
//
// pig divergence (D199): Pi lists no OpenRouter routing variant; PiG derives them from the bundled models instead of storing them.
func builtinChatCatalogListed(id string) []ai.GeneratedModel {
	return currentBuiltinChatCatalogsNow().listed(id)
}

// listed is builtinChatCatalogListed(id) in this generation.
func (catalogs *builtinChatCatalogs) listed(id string) []ai.GeneratedModel {
	catalog := catalogs.catalog(id)
	if id != "openrouter" || len(catalog) == 0 { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
		return catalog
	}
	catalogs.mu.RLock()
	listed := catalogs.openRouterListed
	catalogs.mu.RUnlock()
	if listed != nil {
		return listed
	}
	listed = ai.SharedOpenRouterRoutingVariants(catalog)
	listed = listed[:len(listed):len(listed)]
	catalogs.mu.Lock()
	defer catalogs.mu.Unlock()
	if catalogs.openRouterListed != nil {
		return catalogs.openRouterListed
	}
	catalogs.openRouterListed = listed
	return listed
}

// builtinChatCatalogListing identifies the models builtinChatCatalogListed(id) lists, in its order, without building that list: catalog is
// builtinChatCatalog(id), and listing names each listed model and the entry of catalog it is, or is a routing variant of (ai.OpenRouterListing).
// A nil listing lists catalog itself. A caller that reads only what identifies a model (its provider, id and name, and what a variant repeats
// from its model) reads these, so no routing variant is built for it. The listing is identified once and shared, read-only like the catalog.
//
// pig divergence (D199): Pi lists no OpenRouter routing variant; PiG derives them from the bundled models instead of storing them.
func builtinChatCatalogListing(id string) (catalog []ai.GeneratedModel, listing []ai.ListedOpenRouterModel) {
	catalog = builtinChatCatalog(id)
	if id != "openrouter" || len(catalog) == 0 { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
		return catalog, nil
	}
	catalogs := currentBuiltinChatCatalogs.Load()
	catalogs.mu.RLock()
	identified := catalogs.openRouterIdentified
	catalogs.mu.RUnlock()
	if identified == nil {
		identified = &builtinChatCatalogIdentity{catalog: catalog, listing: ai.OpenRouterListing(catalog)}
		catalogs.mu.Lock()
		if catalogs.openRouterIdentified == nil {
			catalogs.openRouterIdentified = identified
		}
		identified = catalogs.openRouterIdentified
		catalogs.mu.Unlock()
	}
	return identified.catalog, identified.listing
}
