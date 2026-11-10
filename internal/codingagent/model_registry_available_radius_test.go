package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// GetAvailableModelData defers a Radius provider's catalog until the provider has auth. That is only the available catalog of model-registry.ts getAvailable if every model a Radius catalog lists carries the provider's own ID.
func TestRadiusCatalogModelsCarryTheirProvidersID(t *testing.T) {
	registry, _, _ := radiusTestRegistry(t, `{"providers":{"acme":{"oauth":"radius","baseUrl":"https://acme.invalid"}}}`, nil)
	radius := 0
	for _, id := range registry.modelProviderIDs() {
		if !registry.isRadiusCatalog(id) {
			continue
		}
		radius++
		models := registry.GetProviderModelData(id)
		if id == RadiusProviderID && len(models) == 0 {
			t.Errorf("the built-in %s provider lists no model", id)
		}
		for _, model := range models {
			if model.ProviderMeta.ProviderID != id {
				t.Errorf("Radius provider %s lists %s/%s", id, model.ProviderMeta.ProviderID, model.ID)
			}
		}
	}
	if radius < 2 {
		t.Fatalf("%d Radius providers; want the built-in one and the models.json one", radius)
	}
}

// model-registry.ts getAvailable: a Radius provider with auth lists its catalog among the available models, in catalog order, and one without auth lists none.
func TestGetAvailableModelDataListsAConfiguredRadiusCatalogInCatalogOrder(t *testing.T) {
	registry, _, _ := radiusTestRegistry(t, `{"providers":{"acme":{"oauth":"radius","baseUrl":"https://acme.invalid"}}}`, map[string]ai.Credential{RadiusProviderID: {Type: ai.CredentialAPIKey, Key: "test-key"}})
	registry.SetRuntimeAPIKey("xai", "key-xai")
	var want []*ai.Model
	radiusModels := 0
	for _, model := range registry.GetAllModelData() {
		if registry.HasConfiguredAuth(model.ProviderMeta.ProviderID) {
			want = append(want, model)
			if model.ProviderMeta.ProviderID == RadiusProviderID {
				radiusModels++
			}
		}
	}
	if radiusModels == 0 {
		t.Fatal("the configured Radius provider lists no model in the full catalog")
	}
	if got := registry.GetAvailableModelData(); !reflect.DeepEqual(got, want) {
		t.Fatalf("available models = %d, want the %d models of configured providers, %d of them Radius's, in catalog order", len(got), len(want), radiusModels)
	}
}

// A start with no Radius auth does not build the Radius catalog: its models are discarded unseen, so listing the available models costs less than reading that catalog.
func TestGetAvailableModelDataDoesNotBuildAnUnconfiguredRadiusCatalog(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	registry.SetRuntimeAPIKey("xai", "key-xai")
	if registry.HasConfiguredAuth(RadiusProviderID) {
		t.Skip("the environment configures Radius auth")
	}
	radius := testing.AllocsPerRun(5, func() { registry.GetProviderModelData(RadiusProviderID) })
	available := testing.AllocsPerRun(5, func() { registry.GetAvailableModelData() })
	t.Logf("allocs: Radius catalog %.0f, available %.0f", radius, available)
	if available >= radius {
		t.Fatalf("GetAvailableModelData allocated %.0f, not fewer than the %.0f the unconfigured Radius catalog costs alone", available, radius)
	}
}
