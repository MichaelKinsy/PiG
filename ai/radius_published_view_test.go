package ai

import (
	"maps"
	"reflect"
	"slices"
	"testing"
)

// copiedGeneratedModel is ToModel as it was before catalogView: the reference both ToModel and the Radius baseline must match.
func copiedGeneratedModel(m *GeneratedModel) *Model {
	return &Model{
		catalogRecord:    m.Record,
		Type:             ModelTypeChat,
		ID:               m.ID,
		DisplayName:      m.DisplayName,
		Capabilities:     m.ToCapabilities(),
		Input:            slices.Clone(m.Capabilities),
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

// ToModel still copies every catalog entry the way it did, and its models share nothing a caller can write with the catalog.
func TestGeneratedModelToModelEqualsTheCopiedModel(t *testing.T) {
	for i := range GeneratedModels {
		generated := &GeneratedModels[i]
		if got, want := generated.ToModel(), copiedGeneratedModel(generated); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s/%s: ToModel = %+v, want %+v", generated.Provider, generated.ID, got, want)
		}
	}
	before := make([]*Model, len(GeneratedModels))
	for i := range GeneratedModels {
		before[i] = copiedGeneratedModel(&GeneratedModels[i])
	}
	for i := range GeneratedModels {
		writeEveryCollection(GeneratedModels[i].ToModel(), false)
	}
	for i := range GeneratedModels {
		if after := copiedGeneratedModel(&GeneratedModels[i]); !reflect.DeepEqual(after, before[i]) {
			t.Fatalf("%s/%s: writing to ToModel's model changed the generated catalog", GeneratedModels[i].Provider, GeneratedModels[i].ID)
		}
	}
}

// writeEveryCollection writes through every slice, map and pointer of model. ToModel shares the catalog's cost tiers, as it always has, so they are written only when tiers is set.
func writeEveryCollection(model *Model, tiers bool) {
	for i := range model.Input {
		model.Input[i] = "mutated"
	}
	for level, value := range model.ThinkingLevelMap {
		if value != nil {
			*value = "mutated"
		}
		model.ThinkingLevelMap[level+"-mutated"] = nil
	}
	if tiers {
		for i := range model.Capabilities.CostTiers {
			model.Capabilities.CostTiers[i].InputCostPer1M = -1
		}
	}
	if model.InputLimits != nil {
		model.InputLimits.MaxRequestBytes = -1
		if model.InputLimits.Images != nil {
			model.InputLimits.Images.MaxPerMessage = -1
		}
	}
	if model.PromptCache != nil {
		model.PromptCache["mutated"] = -1
	}
	if model.SamplingParams != nil {
		model.SamplingParams["mutated"] = 1
	}
	if model.ProviderMeta.Headers != nil {
		model.ProviderMeta.Headers["mutated"] = "x"
	}
	if model.ProviderMeta.Compat != nil {
		*model.ProviderMeta.Compat = ModelCompat{}
	}
}

var publishedRadiusModelsSink []*Model

// The Radius baseline is a view of the catalog (radius.ts builds it once and getModels returns it), and what a caller gets from GetModels equals the baseline built from ToModel's copy and shares nothing with the catalog or with another call: a caller that writes through every collection of its models changes neither.
func TestPublishedRadiusModelsAreViewsAndGetModelsIsolatesCallers(t *testing.T) {
	reference := func(id string) []*Model {
		var models []*Model
		for i := range GeneratedModels {
			if GeneratedModels[i].Provider != RadiusProviderID {
				continue
			}
			model := copiedGeneratedModel(&GeneratedModels[i])
			model.ProviderMeta.ProviderID = id
			models = append(models, cloneRadiusModel(model))
		}
		return models
	}
	for _, id := range []string{RadiusProviderID, "my-gateway"} {
		want := reference(id)
		if len(want) == 0 {
			t.Fatal("the generated catalog has no Radius models")
		}
		views := publishedRadiusModels(id)
		got := make([]*Model, len(views))
		for i, view := range views {
			got[i] = cloneRadiusModel(view)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: the copies of publishedRadiusModels = %+v, want %+v", id, got, want)
		}
	}

	before := reference(RadiusProviderID)
	provider := NewRadiusGatewayProvider(RadiusProviderOptions{})
	first := provider.GetModels()
	if !reflect.DeepEqual(first, before) {
		t.Fatal("GetModels is not the baseline built from ToModel's copy")
	}
	for _, model := range first {
		writeEveryCollection(model, true)
	}
	if after := reference(RadiusProviderID); !reflect.DeepEqual(after, before) {
		t.Fatal("writing to the models GetModels returned changed the generated catalog")
	}
	if second := provider.GetModels(); !reflect.DeepEqual(second, before) {
		t.Fatal("writing to the models GetModels returned changed what the next call returns")
	}

	// The baseline costs what building the views costs and no copy of them: radius.ts builds the baseline once, and GetModels is the one copy.
	viewsOnly := func(id string) []*Model {
		var models []*Model
		for i := range GeneratedModels {
			if GeneratedModels[i].Provider != RadiusProviderID {
				continue
			}
			model := GeneratedModels[i].catalogView()
			model.ProviderMeta.ProviderID = id
			models = append(models, model)
		}
		return models
	}
	published := testing.AllocsPerRun(20, func() { publishedRadiusModelsSink = publishedRadiusModels(RadiusProviderID) })
	views := testing.AllocsPerRun(20, func() { publishedRadiusModelsSink = viewsOnly(RadiusProviderID) })
	if published > views {
		t.Fatalf("publishedRadiusModels: %v allocations, building the catalog views alone takes %v; the baseline is not copied", published, views)
	}
}
