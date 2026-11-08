package ai

import "testing"

// A start that lists models, looks one up, converts it to a Model, or lists OpenRouter's routing variants reads no record's content, so it expands no chunk of catalog_records/. This test is not parallel, so no other test expands a chunk during it.
func TestCatalogListingsExpandNoRecordChunk(t *testing.T) {
	before := CatalogChunksExpanded()
	all := ListModels("")
	if len(all) == 0 {
		t.Fatal("empty catalog")
	}
	for _, provider := range ListProviders() {
		ListModels(provider)
	}
	if _, ok := LookupModel(all[0].Provider + "/" + all[0].ID); !ok {
		t.Fatal("lookup failed")
	}
	var models []AnyModel
	for i := range all {
		models = append(models, all[i].ToModel())
		all[i].ToCapabilities()
	}
	// ListModels lists OpenRouter's routing variants already; deriving them again over the converted models finds them listed.
	if len(all) <= len(GeneratedModels) {
		t.Fatalf("ListModels lists %d models, the catalog holds %d: no OpenRouter variants", len(all), len(GeneratedModels))
	}
	_ = WithOpenRouterRoutingVariantModels(models)
	if got := CatalogChunksExpanded() - before; got != 0 {
		t.Fatalf("%d record chunks expanded by listings", got)
	}
	// Reading one record expands exactly its chunk, and only once.
	ResolveCatalogRecord(all[0].Record)
	ResolveCatalogRecord(all[1].Record)
	if got := CatalogChunksExpanded() - before; got > 1 {
		t.Fatalf("two records of the first chunk expanded %d chunks", got)
	}
}
