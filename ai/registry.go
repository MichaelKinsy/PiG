package ai

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// ─── Model Registry ───────────────────────────────────────────────────────────
//
// The registry indexes the codegen'd `GeneratedModels` slice for lookup
// by full id (`provider/model`), bare model id, and model id alone.
// Status line, /cost, and `--diagnose` all consume from here.

// Each index is built on first use: resolving a "<provider>/<model-id>" (every start that names a model) needs only the full-id index, listing a provider's models only the provider index, and the bare-id index is the largest.
var (
	registryFQOnce       sync.Once
	registryByFQ         map[string]*GeneratedModel // "<provider>/<model-id>"
	registryIDOnce       sync.Once
	registryByID         map[string][]*GeneratedModel // "<model-id>" → entries (multiple providers)
	registryProviderOnce sync.Once
	registryByProvider   map[string][]*GeneratedModel
	// registryFQBuilt is set once the FQ index exists. Until then LookupModelExact scans the catalog, so a process that resolves one
	// exact key (a print run's startup model) does not build the index over the whole catalog.
	registryFQBuilt atomic.Bool
)

func initRegistryFQ() {
	registryByFQ = indexGeneratedModelsByFQ(GeneratedModels)
	registryFQBuilt.Store(true)
}

func initRegistryID() {
	registryByID = groupGeneratedModels(GeneratedModels, len(GeneratedModels), func(m *GeneratedModel) string { return m.ID })
}

func initRegistryProvider() {
	registryByProvider = groupGeneratedModels(GeneratedModels, len(GeneratedProviders), func(m *GeneratedModel) string { return m.Provider })
}

// indexGeneratedModelsByFQ indexes models by "<provider>/<model-id>"; a repeated key resolves to the last such model. Every key is a substring of one string, so the index costs a fixed number of allocations rather than one per model.
func indexGeneratedModelsByFQ(generated []GeneratedModel) map[string]*GeneratedModel {
	keyBytes := 0
	for i := range generated {
		keyBytes += len(generated[i].Provider) + 1 + len(generated[i].ID)
	}
	var keys strings.Builder
	keys.Grow(keyBytes)
	for i := range generated {
		keys.WriteString(generated[i].Provider)
		keys.WriteByte('/')
		keys.WriteString(generated[i].ID)
	}
	allKeys := keys.String()
	byFQ := make(map[string]*GeneratedModel, len(generated))
	for i := range generated {
		m := &generated[i]
		var fq string
		fq, allKeys = allKeys[:len(m.Provider)+1+len(m.ID)], allKeys[len(m.Provider)+1+len(m.ID):]
		byFQ[fq] = m
	}
	return byFQ
}

// groupGeneratedModels lists models by key, in catalog order; distinct sizes the count table. Every list is a capacity-clipped window of one array, so the index costs a fixed number of allocations rather than several per model.
func groupGeneratedModels(generated []GeneratedModel, distinct int, key func(*GeneratedModel) string) map[string][]*GeneratedModel {
	counts := make(map[string]int, distinct)
	for i := range generated {
		counts[key(&generated[i])]++
	}
	lists := make([]*GeneratedModel, len(generated))
	groups := make(map[string][]*GeneratedModel, len(counts))
	for i := range generated {
		m := &generated[i]
		k := key(m)
		list, ok := groups[k]
		if !ok {
			count := counts[k]
			list, lists = lists[:0:count], lists[count:]
		}
		groups[k] = append(list, m)
	}
	return groups
}

// LookupModel resolves a spec like "<provider>/<model-id>" or just
// "<model-id>" against the generated catalog. Returns the matching
// model and true on success.
//
// When only a bare model id is given and multiple providers ship a
// model with the same id (rare; "claude-3-5-sonnet" appears under
// both anthropic and copilot for example), the first match in
// alphabetical-provider order wins. Callers wanting deterministic
// resolution should pass the full "<provider>/<model-id>" form.
func LookupModel(spec string) (*GeneratedModel, bool) {
	registryFQOnce.Do(initRegistryFQ)
	if spec == "" {
		return nil, false
	}
	if m, ok := registryByFQ[spec]; ok {
		return m, true
	}
	// Try bare id.
	registryIDOnce.Do(initRegistryID)
	id := spec
	if _, after, ok := strings.Cut(spec, "/"); ok {
		id = after
	}
	if entries := registryByID[id]; len(entries) > 0 {
		return entries[0], true
	}
	return nil, false
}

// LookupModelExact resolves only the fully-qualified "provider/id" key,
// without LookupModel's bare-id fallback across other providers. This is
// what model resolution needs so that "github-copilot/gpt-4o" yields no
// match when the copilot catalog lacks gpt-4o, instead of silently
// borrowing openai's gpt-4o capabilities. Mirrors upstream resolveCliModel,
// which filters candidate models by provider before matching the id.
func LookupModelExact(spec string) (*GeneratedModel, bool) {
	if spec == "" {
		return nil, false
	}
	if !registryFQBuilt.Load() {
		return scanModelExact(GeneratedModels, spec)
	}
	m, ok := registryByFQ[spec]
	return m, ok
}

// scanModelExact is the byFQ index's answer for spec without the index: the last catalog model whose "<provider>/<model-id>" is spec
// (indexGeneratedModelsByFQ stores each key in catalog order, so a repeated key keeps its last model).
func scanModelExact(generated []GeneratedModel, spec string) (*GeneratedModel, bool) {
	for i := len(generated) - 1; i >= 0; i-- {
		m := &generated[i]
		if len(spec) == len(m.Provider)+1+len(m.ID) && spec[len(m.Provider)] == '/' && strings.HasPrefix(spec, m.Provider) && strings.HasSuffix(spec, m.ID) {
			return m, true
		}
	}
	return nil, false
}

// ToCapabilities lifts a GeneratedModel into the runtime
// ModelCapabilities consumed by status line and provider routing.
func (m *GeneratedModel) ToCapabilities() ModelCapabilities {
	caps := ModelCapabilities{
		ContextWindow:       m.ContextWindow,
		MaxOutputTokens:     m.MaxOutputTokens,
		InputCostPer1M:      m.InputCostPerMTokens,
		OutputCostPer1M:     m.OutputCostPerMTokens,
		CacheReadCostPer1M:  m.CacheReadCost,
		CacheWriteCostPer1M: m.CacheWriteCost,
		CostTiers:           m.Tiers,
		SupportsToolUse:     true, // upstream catalog assumes tool-use universally; per-API gating happens at provider layer
	}
	for _, c := range m.Capabilities {
		if c == "image" {
			caps.SupportsImages = true
		}
	}
	// MaxThinking is the highest level GetSupportedThinkingLevels reports for a model whose only reasoning signal is thinkingMaxLevel, computed without the throwaway Model and level list.
	if thinkingMaxLevel(m.Reasoning, m.ThinkingLevelMap) == "" {
		return caps
	}
	for _, level := range extendedThinkingLevels {
		if !thinkingLevelUnsupported(m.ThinkingLevelMap, level) && CompareThinkingLevels(level, ModelThinkingLevel(caps.MaxThinking)) > 0 {
			caps.MaxThinking = ThinkingLevel(level)
		}
	}
	return caps
}

func thinkingMaxLevel(reasoning bool, levelMap ThinkingLevelMap) ThinkingLevel {
	if !reasoning {
		return ""
	}
	maxLevel := ThinkingLevel(ThinkingHigh)
	for level, mapped := range levelMap {
		if mapped == nil {
			continue
		}
		if CompareThinkingLevels(level, ModelThinkingLevel(maxLevel)) > 0 {
			maxLevel = ThinkingLevel(level)
		}
	}
	return maxLevel
}

// ToModel preserves catalog prices, limits, modalities, capabilities and provider metadata for runtime requests.
func (m *GeneratedModel) ToModel() *Model {
	if m == nil {
		return nil
	}
	model := m.catalogView()
	model.Input = slices.Clone(model.Input)
	model.ThinkingLevelMap = cloneThinkingLevelMap(model.ThinkingLevelMap)
	model.SamplingParams = maps.Clone(model.SamplingParams)
	model.PromptCache = maps.Clone(model.PromptCache)
	model.InputLimits = model.InputLimits.Clone()
	model.ProviderMeta.Headers = cloneStringMap(model.ProviderMeta.Headers)
	model.ProviderMeta.Compat = cloneCompat(model.ProviderMeta.Compat)
	return model
}

// catalogView is ToModel without its copies: the model's slices, maps and pointers are the catalog's own. A caller that keeps the model or hands it out must copy them first.
func (m *GeneratedModel) catalogView() *Model {
	// Every entry of Pi's built-in catalog data carries "type":"chat", so the RPC model objects of built-in models do too.
	return &Model{
		catalogRecord:    m.Record,
		Type:             ModelTypeChat,
		ID:               m.ID,
		DisplayName:      m.DisplayName,
		Capabilities:     m.ToCapabilities(),
		Input:            m.Capabilities,
		ThinkingLevelMap: m.ThinkingLevelMap,
		SamplingParams:   m.SamplingParams,
		PromptCache:      m.PromptCache,
		InputLimits:      m.InputLimits,
		ProviderMeta: ProviderMetadata{
			ProviderID: m.Provider,
			API:        m.API,
			BaseURL:    m.BaseURL,
			Headers:    m.Headers,
			Compat:     m.Compat,
			Reasoning:  m.Reasoning,
		},
	}
}

// ListModels returns a copy of the catalog filtered by an optional
// provider prefix. Empty provider returns everything. It omits the models
// of a stripped API (OfferedModels); LookupModel and LookupModelExact
// still find them.
func ListModels(provider string) []GeneratedModel {
	stripped := strippedAPIs()
	if provider == "" {
		out := make([]GeneratedModel, 0, len(GeneratedModels))
		for _, model := range GeneratedModels {
			if !slices.Contains(stripped, model.API) {
				out = append(out, model)
			}
		}
		return out
	}
	registryProviderOnce.Do(initRegistryProvider)
	models := registryByProvider[provider]
	out := make([]GeneratedModel, 0, len(models))
	for _, model := range models {
		if !slices.Contains(stripped, model.API) {
			out = append(out, *model)
		}
	}
	return out
}

// ListProviders returns the provider ids of the generated catalog barrel in
// barrel order. Mirrors upstream providers/all.ts:getBuiltinProviders
// (Object.keys(MODELS)) and compat.ts:getProviders; a provider with only image
// or classifier models is included.
func ListProviders() []string {
	return slices.Clone(GeneratedProviders)
}

// GetBuiltinModelDataGeneratedAt returns the generation time shared by every built-in provider catalog, in Unix milliseconds, or nil when the data manifest records no valid time.
// Mirrors upstream providers/all.ts:getBuiltinModelDataGeneratedAt.
func GetBuiltinModelDataGeneratedAt() *float64 {
	if GeneratedModelDataGeneratedAt == nil {
		return nil
	}
	return new(*GeneratedModelDataGeneratedAt)
}

// ListRuntimeProviders returns providers with an implemented runtime API.
func ListRuntimeProviders() []string {
	return ListProviders()
}
