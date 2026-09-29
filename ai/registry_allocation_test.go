package ai

import (
	"reflect"
	"testing"
)

// Filtering keeps the generated provider and model order and returns an owned slice.
func TestListModelsAllocatesOnlyFilteredCatalog(t *testing.T) {
	providers := append(ListProviders(), "", "missing-provider")
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			want := []GeneratedModel{}
			for _, model := range GeneratedModels {
				if provider == "" || model.Provider == provider {
					want = append(want, model)
				}
			}
			got := ListModels(provider)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("filtered catalog differs from generated order or metadata")
			}
			if cap(got) != len(got) {
				t.Errorf("filtered catalog capacity = %d, want result length %d", cap(got), len(got))
			}
			if len(got) > 0 {
				got[0].ID = "modified copy"
				if ListModels(provider)[0].ID != want[0].ID {
					t.Fatal("caller mutated catalog")
				}
			}
		})
	}
}

func BenchmarkListModelsByProvider(b *testing.B) {
	providers := ListProviders()
	b.ReportAllocs()
	for b.Loop() {
		for _, provider := range providers {
			_ = ListModels(provider)
		}
	}
}

// BenchmarkGeneratedToModelCatalog materializes the whole generated catalog as ModelRuntime startup does.
func BenchmarkGeneratedToModelCatalog(b *testing.B) {
	models := ListModels("")
	b.ReportAllocs()
	for b.Loop() {
		for i := range models {
			_ = models[i].ToModel()
		}
	}
}

// referenceCapabilities is the pre-optimization ToCapabilities: MaxThinking is the highest level GetSupportedThinkingLevels reports for a model whose only reasoning signal is thinkingMaxLevel.
func referenceCapabilities(m *GeneratedModel) ModelCapabilities {
	caps := ModelCapabilities{
		ContextWindow:       m.ContextWindow,
		MaxOutputTokens:     m.MaxOutputTokens,
		InputCostPer1M:      m.InputCostPerMTokens,
		OutputCostPer1M:     m.OutputCostPerMTokens,
		CacheReadCostPer1M:  m.CacheReadCost,
		CacheWriteCostPer1M: m.CacheWriteCost,
		CostTiers:           m.Tiers,
		SupportsToolUse:     true,
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

// ToCapabilities keeps the level selection of GetSupportedThinkingLevels for every catalog entry and for every combination of reasoning flag and level map, including maps that remove High or every level.
func TestToCapabilitiesMatchesSupportedThinkingLevelSelection(t *testing.T) {
	models := ListModels("")
	str := func(s string) *string { return &s }
	levels := extendedThinkingLevels
	for mask := range 1 << (2 * len(levels)) {
		levelMap := ThinkingLevelMap{}
		for i, level := range levels {
			switch mask >> (2 * i) & 3 {
			case 1:
				levelMap[level] = nil
			case 2:
				levelMap[level] = str(string(level))
			}
		}
		for _, reasoning := range []bool{false, true} {
			models = append(models, GeneratedModel{Reasoning: reasoning, ThinkingLevelMap: levelMap})
		}
		if mask == 0 {
			models = append(models, GeneratedModel{Reasoning: true})
		}
	}
	for i := range models {
		got, want := models[i].ToCapabilities(), referenceCapabilities(&models[i])
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s/%s (reasoning=%v map=%v): ToCapabilities = %+v, want %+v", models[i].Provider, models[i].ID, models[i].Reasoning, models[i].ThinkingLevelMap, got, want)
		}
	}
}

// Materializing the catalog is startup work: it must not allocate the throwaway Model, level list and map copy that selecting MaxThinking once required.
func TestToModelCatalogAllocationBudget(t *testing.T) {
	models := ListModels("")
	got := testing.AllocsPerRun(5, func() {
		for i := range models {
			_ = models[i].ToModel()
		}
	})
	if perModel := got / float64(len(models)); perModel > 10 {
		t.Fatalf("ToModel allocates %.1f times per catalog model, budget 10", perModel)
	}
}
