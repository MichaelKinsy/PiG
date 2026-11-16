package ai

import "testing"

// listedCatalogEntries is every entry the catalog lists: the bundled models and each OpenRouter model's D199 routing variants.
func listedCatalogEntries() []GeneratedModel { return WithOpenRouterRoutingVariants(GeneratedModels) }

// The fast path computes no baseline: nothing is decoded from a record or encoded from a model, so the record cache stays empty, and a call allocates
// only the copy of the record it returns and the entry's typed model it compares with.
func TestRecognizedCatalogModelCostsTwoAllocations(t *testing.T) {
	clearCatalogRecordBaselines()
	for i := range listedCatalogEntries() {
		entry := listedCatalogEntries()[i]
		if _, ok, err := CatalogModelJSON(entry.ToModel()); !ok || err != nil {
			t.Fatalf("%s: ok %t, %v", entry.ID, ok, err)
		}
	}
	if cached := cachedCatalogRecordBaselines(); cached != 0 {
		t.Fatalf("encoding the unchanged catalog computed %d record baselines, want none", cached)
	}
	model := GeneratedModels[0].ToModel()
	if allocs := testing.AllocsPerRun(100, func() { _, _, _ = CatalogModelJSON(model) }); allocs > 2 {
		t.Fatalf("an unchanged model allocates %v times per encoding, want the record copy and the entry's typed model", allocs)
	}
}
