package ai

import (
	"maps"
	"slices"
	"testing"
)

// The registry indexes are the same as assigning and appending each model to its key and lists in catalog order, with lists no caller can append into.
func TestRegistryIndexesMatchPerModelAppendInCatalogOrder(t *testing.T) {
	catalog := append(slices.Clone(GeneratedModels),
		GeneratedModel{Provider: "zz-provider", ID: "shared"}, GeneratedModel{Provider: "zz-other", ID: "shared"}, GeneratedModel{Provider: "zz-provider", ID: "shared"})
	wantFQ := map[string]*GeneratedModel{}
	wantID := map[string][]*GeneratedModel{}
	wantProvider := map[string][]*GeneratedModel{}
	for i := range catalog {
		m := &catalog[i]
		wantFQ[m.Provider+"/"+m.ID] = m
		wantID[m.ID] = append(wantID[m.ID], m)
		wantProvider[m.Provider] = append(wantProvider[m.Provider], m)
	}
	byFQ := indexGeneratedModelsByFQ(catalog)
	byID := groupGeneratedModels(catalog, len(catalog), func(m *GeneratedModel) string { return m.ID })
	byProvider := groupGeneratedModels(catalog, 0, func(m *GeneratedModel) string { return m.Provider })
	if !maps.Equal(byFQ, wantFQ) {
		t.Fatalf("byFQ differs from the per-model index (%d keys, want %d)", len(byFQ), len(wantFQ))
	}
	for name, pair := range map[string][2]map[string][]*GeneratedModel{"byID": {byID, wantID}, "byProvider": {byProvider, wantProvider}} {
		got, want := pair[0], pair[1]
		if !maps.EqualFunc(got, want, slices.Equal) {
			t.Fatalf("%s differs from the per-model index", name)
		}
		for key, list := range got {
			if cap(list) != len(list) {
				t.Fatalf("%s[%q] has capacity %d for %d models: an append would write into another list", name, key, cap(list), len(list))
			}
		}
	}
	if got := byID["shared"]; len(got) != 3 || got[0].Provider != "zz-provider" || got[1].Provider != "zz-other" {
		t.Fatalf("byID[shared] = %v, want catalog order", got)
	}
	if got := byFQ["zz-provider/shared"]; got != &catalog[len(catalog)-1] {
		t.Fatal("a repeated provider/model key must resolve to the last such model, as a map assignment per model does")
	}
	// Indexing allocates a fixed number of arrays and map tables, not one key or list per model (30 for 1,563 models when this was written).
	if allocs := testing.AllocsPerRun(5, func() {
		indexGeneratedModelsByFQ(GeneratedModels)
		groupGeneratedModels(GeneratedModels, len(GeneratedModels), func(m *GeneratedModel) string { return m.ID })
		groupGeneratedModels(GeneratedModels, len(GeneratedProviders), func(m *GeneratedModel) string { return m.Provider })
	}); allocs > float64(len(GeneratedModels)/20) {
		t.Fatalf("indexing %d models allocated %.0f times, want at most %d", len(GeneratedModels), allocs, len(GeneratedModels)/20)
	}
}
