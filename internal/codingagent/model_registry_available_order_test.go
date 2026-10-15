package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// model-registry.ts getAvailable: the available catalog is the composed catalog restricted to providers with configured auth, in catalog order.
func TestGetAvailableModelDataIsTheCatalogOfConfiguredProvidersInCatalogOrder(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	// Configure providers far apart in the catalog order, last one first.
	for _, id := range []string{"xai", "anthropic", "openai"} {
		registry.SetRuntimeAPIKey(id, "key-"+id)
	}
	var want []*ai.Model
	for _, model := range registry.GetAllModelData() {
		if registry.HasConfiguredAuth(model.ProviderMeta.ProviderID) {
			want = append(want, model)
		}
	}
	got := registry.GetAvailableModelData()
	if len(want) < 3 || len(got) != len(want) {
		t.Fatalf("available models = %d, want the %d models of configured providers", len(got), len(want))
	}
	providers := map[string]bool{}
	for i := range want {
		if got[i].ProviderMeta.ProviderID != want[i].ProviderMeta.ProviderID || got[i].ID != want[i].ID {
			t.Fatalf("available[%d] = %s/%s, want %s/%s (catalog order)", i, got[i].ProviderMeta.ProviderID, got[i].ID, want[i].ProviderMeta.ProviderID, want[i].ID)
		}
		providers[want[i].ProviderMeta.ProviderID] = true
	}
	for _, id := range []string{"xai", "anthropic", "openai"} {
		if !providers[id] {
			t.Errorf("configured provider %s has no available model", id)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("available models differ from the configured providers' models of the full catalog")
	}
}

// The available catalog builds the bundled models of a provider only when they are available, so a start with one configured provider does not build the whole catalog.
func TestGetAvailableModelDataBuildsOnlyAvailableBundledModels(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	registry.SetRuntimeAPIKey("xai", "key-xai")
	all := float64(testing.AllocsPerRun(5, func() { registry.GetAllModelData() }))
	available := testing.AllocsPerRun(5, func() { registry.GetAvailableModelData() })
	t.Logf("allocs: all %.0f, available %.0f", all, available)
	if available > all/4 {
		t.Fatalf("GetAvailableModelData allocated %.0f, more than a quarter of the full catalog's %.0f", available, all)
	}
}
