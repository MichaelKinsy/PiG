package ai

import (
	"maps"
	"slices"
	"strings"
	"sync"
)

// ─── Model Registry ───────────────────────────────────────────────────────────
//
// The registry indexes the codegen'd `GeneratedModels` slice for lookup
// by full id (`provider/model`), bare model id, and model id alone.
// Status line, /cost, and `--diagnose` all consume from here.

var (
	registryOnce sync.Once
	registryByFQ map[string]*GeneratedModel   // "<provider>/<model-id>"
	registryByID map[string][]*GeneratedModel // "<model-id>" → entries (multiple providers)
)

func initRegistry() {
	registryByFQ = make(map[string]*GeneratedModel, len(GeneratedModels))
	registryByID = make(map[string][]*GeneratedModel, len(GeneratedModels))
	for i := range GeneratedModels {
		m := &GeneratedModels[i]
		fq := m.Provider + "/" + m.ID
		registryByFQ[fq] = m
		registryByID[m.ID] = append(registryByID[m.ID], m)
	}
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
	registryOnce.Do(initRegistry)
	if spec == "" {
		return nil, false
	}
	if m, ok := registryByFQ[spec]; ok {
		return m, true
	}
	// Try bare id.
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
	registryOnce.Do(initRegistry)
	if spec == "" {
		return nil, false
	}
	m, ok := registryByFQ[spec]
	return m, ok
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
	for _, level := range GetSupportedThinkingLevels(&Model{
		Capabilities:     ModelCapabilities{MaxThinking: thinkingMaxLevel(m.Reasoning, m.ThinkingLevelMap)},
		ThinkingLevelMap: cloneThinkingLevelMap(m.ThinkingLevelMap),
	}) {
		if CompareThinkingLevels(level, caps.MaxThinking) > 0 {
			caps.MaxThinking = level
		}
	}
	return caps
}

func thinkingMaxLevel(reasoning bool, levelMap ThinkingLevelMap) ThinkingLevel {
	if !reasoning {
		return ""
	}
	maxLevel := ThinkingHigh
	for level, mapped := range levelMap {
		if mapped == nil {
			continue
		}
		if CompareThinkingLevels(level, maxLevel) > 0 {
			maxLevel = level
		}
	}
	return maxLevel
}

// ToModel lifts a generated catalog entry into the runtime model metadata shape
// used by provider-specific compat helpers and tests.
func (m *GeneratedModel) ToModel() *Model {
	if m == nil {
		return nil
	}
	return &Model{
		ID:          m.ID,
		DisplayName: m.DisplayName,
		Capabilities: ModelCapabilities{
			MaxThinking:     thinkingMaxLevel(m.Reasoning, m.ThinkingLevelMap),
			MaxOutputTokens: m.MaxOutputTokens,
		},
		Input:            append([]string(nil), m.Capabilities...),
		ThinkingLevelMap: cloneThinkingLevelMap(m.ThinkingLevelMap),
		SamplingParams:   maps.Clone(m.SamplingParams),
		PromptCache:      maps.Clone(m.PromptCache),
		InputLimits:      m.InputLimits.Clone(),
		ProviderMeta: ProviderMetadata{
			ProviderID: m.Provider,
			API:        m.API,
			BaseURL:    m.BaseURL,
			Headers:    cloneStringMap(m.Headers),
			Compat:     cloneCompat(m.Compat),
			Reasoning:  m.Reasoning,
		},
	}
}

// ListModels returns a copy of the catalog filtered by an optional
// provider prefix. Empty provider returns everything.
func ListModels(provider string) []GeneratedModel {
	registryOnce.Do(initRegistry)
	out := make([]GeneratedModel, 0, len(GeneratedModels))
	for _, m := range GeneratedModels {
		if provider == "" || m.Provider == provider {
			out = append(out, m)
		}
	}
	return out
}

// ListProviders returns the sorted list of unique provider names from
// the generated static model catalog. Mirrors upstream
// providers/all.ts:getBuiltinProviders and compat.ts:getProviders.
func ListProviders() []string {
	registryOnce.Do(initRegistry)
	seen := make(map[string]struct{})
	for _, m := range GeneratedModels {
		seen[m.Provider] = struct{}{}
	}
	providers := make([]string, 0, len(seen))
	for p := range seen {
		providers = append(providers, p)
	}
	slices.Sort(providers)
	return providers
}

// ListRuntimeProviders returns providers with an implemented runtime API.
func ListRuntimeProviders() []string {
	return ListProviders()
}
