package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A start composes the model registry and reads its listings (the model list, the picker, RPC's get_available_models, the headless model resolution) without reading any built-in catalog record's JSON, so none of the compressed record chunks of ai/catalog_records is expanded. A record is read only for a model's own JSON object (ai.CatalogRecordOf): the extension model registry (one chunk per 64 models), or one model's RPC object. This test is not parallel, so no other test expands a chunk during it.
func TestModelRegistryListingsExpandNoCatalogRecordChunk(t *testing.T) {
	before := ai.CatalogChunksExpanded()
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	if len(registry.RuntimeModels()) == 0 || len(registry.GetAll()) == 0 || len(registry.GetAllModelData()) == 0 {
		t.Fatal("the registry lists no models")
	}
	registry.GetAvailable()
	registry.GetAvailableModelData()
	for _, provider := range ai.ListProviders() {
		registry.GetProviderModelData(provider)
	}
	if got := ai.CatalogChunksExpanded() - before; got != 0 {
		t.Fatalf("%d catalog record chunks expanded by the model listings", got)
	}
}
