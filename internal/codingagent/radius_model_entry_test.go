package codingagent

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

var radiusModelEntrySink ModelEntry

// copiedRadiusModelEntry is radiusModelEntry as it was when it copied every slice and map of its model: the reference the entry must still equal. Its cost-tier copy keeps an empty list empty (an authored tiers: []).
func copiedRadiusModelEntry(model *ai.Model) ModelEntry {
	capabilities := model.Capabilities
	return ModelEntry{
		Type:          model.Type,
		CatalogRecord: ai.CatalogRecordOf(model),
		ProviderID:    model.ProviderMeta.ProviderID, ModelID: model.ID, BaseURL: model.ProviderMeta.BaseURL, DisplayName: model.DisplayName,
		API: string(ai.APIPiMessages), Reasoning: model.ProviderMeta.Reasoning,
		ThinkingLevelMap: cloneThinkingLevelMap(model.ThinkingLevelMap), Input: append([]string(nil), model.Input...),
		InputLimits:   model.InputLimits.Clone(),
		ContextWindow: capabilities.ContextWindow, MaxTokens: capabilities.MaxOutputTokens,
		InputCost: capabilities.InputCostPer1M, OutputCost: capabilities.OutputCostPer1M, CacheReadCost: capabilities.CacheReadCostPer1M, CacheWriteCost: capabilities.CacheWriteCostPer1M,
		CostTiers: slices.Clone(capabilities.CostTiers), PromptCache: maps.Clone(model.PromptCache),
		SamplingParams: maps.Clone(model.SamplingParams), SamplingParamsByThinkingLevel: cloneSamplingParamsByThinkingLevel(model.SamplingParamsByThinkingLevel), Headers: maps.Clone(model.ProviderMeta.Headers), ModelHeaders: maps.Clone(model.ProviderMeta.Headers),
		Compat: mergeCompat((*providerCompat)(model.ProviderMeta.Compat), nil),
	}
}

func fullRadiusTestModel() *ai.Model {
	high, low := "high", "low"
	yes := true
	return &ai.Model{
		Type: ai.ModelTypeChat, ID: "auto", DisplayName: "Radius Auto",
		ProviderMeta:     ai.ProviderMetadata{ProviderID: "radius", API: ai.APIPiMessages, BaseURL: "https://gateway.example", Reasoning: true, Headers: map[string]string{"x-team": "a"}, Compat: &ai.OpenAICompat{SupportsStore: &yes}},
		Capabilities:     ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384, InputCostPer1M: 1, OutputCostPer1M: 2, CacheReadCostPer1M: 0.1, CacheWriteCostPer1M: 0.2, CostTiers: []ai.CostTier{{InputTokensAbove: 200000, InputCostPer1M: 3}}},
		ThinkingLevelMap: ai.ThinkingLevelMap{"high": &high, "low": &low, "off": nil},
		Input:            []string{"text", "image"},
		InputLimits:      &ai.ModelInputLimits{MaxRequestBytes: 1 << 20, Images: &ai.ModelImageInputLimits{MaxPerMessage: 4}},
		PromptCache:      ai.ModelPromptCache{"ttl": 300},
		SamplingParams:   map[string]any{"temperature": 0.2},
		SamplingParamsByThinkingLevel: ai.SamplingParamsByThinkingLevel{
			"high": {"top_p": 0.9},
			"low":  nil,
		},
	}
}

// RadiusProvider.GetModels returns copies the caller owns, so the entry takes their slices and maps over instead of copying them a second time. It still equals the copying entry, nil-for-empty normalization included.
func TestRadiusModelEntryTakesOverTheCopyItIsGiven(t *testing.T) {
	empty := fullRadiusTestModel()
	empty.ThinkingLevelMap, empty.Input, empty.Capabilities.CostTiers = ai.ThinkingLevelMap{}, []string{}, []ai.CostTier{}
	empty.PromptCache, empty.SamplingParams, empty.SamplingParamsByThinkingLevel, empty.ProviderMeta.Headers = ai.ModelPromptCache{}, map[string]any{}, ai.SamplingParamsByThinkingLevel{}, map[string]string{}
	bare := &ai.Model{Type: ai.ModelTypeChat, ID: "bare", ProviderMeta: ai.ProviderMetadata{ProviderID: "radius", API: ai.APIPiMessages}}
	for name, model := range map[string]*ai.Model{"full": fullRadiusTestModel(), "empty": empty, "bare": bare} {
		if got, want := radiusModelEntry(model), copiedRadiusModelEntry(model); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: radiusModelEntry =\n%#v\nwant the copying entry\n%#v", name, got, want)
		}
	}

	entry := radiusModelEntry(fullRadiusTestModel())
	entry.ModelHeaders["x-team"] = "changed"
	if entry.Headers["x-team"] != "a" {
		t.Fatalf("Headers and ModelHeaders share one map: Headers[x-team] = %q", entry.Headers["x-team"])
	}

	model := fullRadiusTestModel()
	allocs := testing.AllocsPerRun(100, func() { radiusModelEntrySink = radiusModelEntry(model) })
	copying := testing.AllocsPerRun(100, func() { radiusModelEntrySink = copiedRadiusModelEntry(model) })
	// The ModelHeaders map (header and group) and the merged compat.
	if allocs > 3 {
		t.Fatalf("radiusModelEntry allocates %v times, want at most 3 (the copying entry allocates %v)", allocs, copying)
	}
	t.Logf("radiusModelEntry allocates %v times; the copying entry %v", allocs, copying)
}

// The models GetProviderModelData returns for a Radius provider are the caller's: writing them leaves the next read unchanged.
func TestRadiusProviderModelDataIsTheCallersCopy(t *testing.T) {
	registry, _, _ := radiusTestRegistry(t, "", nil)
	first := registry.GetProviderModelData(ai.RadiusProviderID)
	if len(first) == 0 {
		t.Fatal("no Radius models")
	}
	second := registry.GetProviderModelData(ai.RadiusProviderID)
	mutated := false
	for _, model := range first {
		for level, value := range model.ThinkingLevelMap {
			if value != nil {
				*value = "mutated"
				mutated = true
			}
			model.ThinkingLevelMap[level] = nil
		}
		for i := range model.Input {
			model.Input[i] = "mutated"
			mutated = true
		}
		for i := range model.Capabilities.CostTiers {
			model.Capabilities.CostTiers[i].InputCostPer1M = -1
		}
		if model.InputLimits != nil {
			model.InputLimits.MaxRequestBytes = -1
		}
		maps.DeleteFunc(model.PromptCache, func(string, float64) bool { return true })
		maps.DeleteFunc(model.SamplingParams, func(string, any) bool { return true })
		maps.DeleteFunc(model.ProviderMeta.Headers, func(string, string) bool { return true })
	}
	if !mutated {
		t.Fatal("the Radius models carry no thinking-level map or input list to write")
	}
	if third := registry.GetProviderModelData(ai.RadiusProviderID); !reflect.DeepEqual(third, second) {
		t.Fatalf("writing returned Radius models changed the next read:\n%#v\nwant\n%#v", third, second)
	}
}

// The composed Radius entries the registry resolves are the caller's too: their slices and maps come from the copies RadiusProvider.GetModels makes on each call.
func TestRadiusComposedEntryIsTheCallersCopy(t *testing.T) {
	registry, _, _ := radiusTestRegistry(t, "", nil)
	models := registry.GetProviderModelData(ai.RadiusProviderID)
	if len(models) == 0 {
		t.Fatal("no Radius models")
	}
	id := models[0].ID
	first, found, owned := registry.radiusModel(ai.RadiusProviderID, id)
	if !found || !owned {
		t.Fatalf("radiusModel(%q) found=%v owned=%v", id, found, owned)
	}
	// The expected values are copied out, since an entry that aliased the provider's catalog would change along with the written one.
	wantInput := slices.Clone(first.Input)
	wantThinking := make(map[ai.ModelThinkingLevel]string)
	for level, value := range first.ThinkingLevelMap {
		if value != nil {
			wantThinking[level] = *value
		}
	}
	if len(first.Input) == 0 && len(first.ThinkingLevelMap) == 0 {
		t.Fatal("the Radius entry carries no thinking-level map or input list to write")
	}
	for i := range first.Input {
		first.Input[i] = "mutated"
	}
	for _, value := range first.ThinkingLevelMap {
		if value != nil {
			*value = "mutated"
		}
	}
	third, _, _ := registry.radiusModel(ai.RadiusProviderID, id)
	gotThinking := make(map[ai.ModelThinkingLevel]string)
	for level, value := range third.ThinkingLevelMap {
		if value != nil {
			gotThinking[level] = *value
		}
	}
	if !slices.Equal(third.Input, wantInput) || !maps.Equal(gotThinking, wantThinking) {
		t.Fatalf("writing a resolved Radius entry changed the next read: input %q thinking %q, want %q %q", third.Input, gotThinking, wantInput, wantThinking)
	}
}
