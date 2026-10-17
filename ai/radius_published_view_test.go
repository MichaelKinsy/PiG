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

// The Radius baseline copies each catalog entry once, from the catalog itself, and equals the baseline built from ToModel's copy. Its models share nothing with the catalog.
func TestPublishedRadiusModelsCopyTheCatalogOnce(t *testing.T) {
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
		got, want := publishedRadiusModels(id), reference(id)
		if len(want) == 0 {
			t.Fatal("the generated catalog has no Radius models")
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: publishedRadiusModels = %+v, want %+v", id, got, want)
		}
	}

	before := reference(RadiusProviderID)
	for _, model := range publishedRadiusModels(RadiusProviderID) {
		writeEveryCollection(model, true)
	}
	if after := reference(RadiusProviderID); !reflect.DeepEqual(after, before) {
		t.Fatal("writing to the published Radius models changed the generated catalog")
	}

	var radiusModels int
	for i := range GeneratedModels {
		if GeneratedModels[i].Provider == RadiusProviderID {
			radiusModels++
		}
	}
	viewAllocs := testing.AllocsPerRun(20, func() { publishedRadiusModelsSink = publishedRadiusModels(RadiusProviderID) })
	copyAllocs := testing.AllocsPerRun(20, func() { publishedRadiusModelsSink = reference(RadiusProviderID) })
	if viewAllocs >= copyAllocs-float64(radiusModels) {
		t.Fatalf("publishedRadiusModels: %v allocations, the second copy took %v; want at least one fewer per model (%d models)", viewAllocs, copyAllocs, radiusModels)
	}
}
