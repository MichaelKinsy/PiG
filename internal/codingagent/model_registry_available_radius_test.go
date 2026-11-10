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

// model-runtime.ts:335-340 (the snapshot ModelRegistry.getAvailable returns) is the full catalog restricted to the providers with configured auth, and ai/src/models.ts:743-752 (Models.getAvailable) reads getModels only from authenticated providers. GetAvailableModelData reads a Radius catalog only for a provider with auth; the list, its order and every model must be what the full catalog filtered by provider auth gives, with no Radius auth, with it, and when it appears or goes mid-session.
func TestGetAvailableModelDataListsARadiusCatalogExactlyWhenItsProviderHasAuth(t *testing.T) {
	registry, auth, _ := radiusTestRegistry(t, `{"providers":{"acme":{"oauth":"radius","baseUrl":"https://acme.invalid"}}}`, nil)
	registry.SetRuntimeAPIKey("xai", "key-xai")
	if registry.HasConfiguredAuth(RadiusProviderID) {
		t.Skip("the environment configures Radius auth")
	}
	check := func(step string, wantRadius bool) {
		t.Helper()
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
		if (radiusModels > 0) != wantRadius {
			t.Fatalf("%s: the full catalog has %d Radius models with auth, want some: %v", step, radiusModels, wantRadius)
		}
		if len(want) <= radiusModels {
			t.Fatalf("%s: no model of another configured provider (xai) is available", step)
		}
		got := registry.GetAvailableModelData()
		if len(got) != len(want) {
			t.Fatalf("%s: %d available models, want %d (%d of them Radius's)", step, len(got), len(want), radiusModels)
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("%s: available[%d] = %s/%s differs from the full catalog's %s/%s", step, i, got[i].ProviderMeta.ProviderID, got[i].ID, want[i].ProviderMeta.ProviderID, want[i].ID)
			}
		}
	}
	check("no Radius auth", false)
	registry.SetRuntimeAPIKey(RadiusProviderID, "runtime-key")
	check("a Radius runtime key appears", true)
	registry.RemoveRuntimeAPIKey(RadiusProviderID)
	check("the Radius runtime key is gone", false)
	if err := auth.Set(RadiusProviderID, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"}); err != nil {
		t.Fatal(err)
	}
	check("a Radius credential is stored", true)
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
