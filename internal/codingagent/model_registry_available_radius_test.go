package codingagent

import (
	"reflect"
	"testing"
	"time"

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

// model-runtime.ts:335-340 (the snapshot ModelRegistry.getAvailable returns) is the full catalog restricted to the providers with configured auth, and ai/src/models.ts:743-752 (Models.getAvailable) reads getModels only from authenticated providers. GetAvailableModelData reads a Radius catalog only for a provider with auth; the list, its order and every model must be what the full catalog filtered by provider auth gives, for the built-in Radius provider and a models.json one (acme, a custom base URL, one defined model), with each kind of credential appearing, leaving and returning mid-session. A runtime API key is Radius auth (model-runtime.ts RuntimeCredentials answers the credential read with {type: "api_key", key}; radius.ts:39 envApiKeyAuth takes credential.key first).
func TestGetAvailableModelDataListsARadiusCatalogExactlyWhenItsProviderHasAuth(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "")
	registry, auth, _ := radiusTestRegistry(t, `{"providers":{"acme":{"oauth":"radius","baseUrl":"https://acme.invalid","models":[{"id":"custom","api":"pi-messages","name":"Custom"}]}}}`, nil)
	registry.SetRuntimeAPIKey("xai", "key-xai")
	const acme = "acme"
	if registry.HasConfiguredAuth(RadiusProviderID) || registry.HasConfiguredAuth(acme) {
		t.Skip("the environment configures Radius auth")
	}
	check := func(step string, wantRadius, wantAcme bool) {
		t.Helper()
		var want []*ai.Model
		counts := map[string]int{}
		for _, model := range registry.GetAllModelData() {
			if registry.HasConfiguredAuth(model.ProviderMeta.ProviderID) {
				want = append(want, model)
				counts[model.ProviderMeta.ProviderID]++
			}
		}
		if (counts[RadiusProviderID] > 0) != wantRadius || (counts[acme] > 0) != wantAcme {
			t.Fatalf("%s: the full catalog has %d Radius and %d acme models with auth, want some: radius %v, acme %v", step, counts[RadiusProviderID], counts[acme], wantRadius, wantAcme)
		}
		if len(want) <= counts[RadiusProviderID]+counts[acme] {
			t.Fatalf("%s: no model of another configured provider (xai) is available", step)
		}
		got := registry.GetAvailableModelData()
		if len(got) != len(want) {
			t.Fatalf("%s: %d available models, want %d (%d Radius, %d acme)", step, len(got), len(want), counts[RadiusProviderID], counts[acme])
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("%s: available[%d] = %s/%s differs from the full catalog's %s/%s", step, i, got[i].ProviderMeta.ProviderID, got[i].ID, want[i].ProviderMeta.ProviderID, want[i].ID)
			}
		}
	}
	check("no Radius auth", false, false)
	for _, id := range []string{RadiusProviderID, acme} {
		wantRadius, wantAcme := id == RadiusProviderID, id == acme
		registry.SetRuntimeAPIKey(id, "runtime-key")
		check(id+": a runtime key appears", wantRadius, wantAcme)
		registry.RemoveRuntimeAPIKey(id)
		check(id+": the runtime key leaves", false, false)
		registry.SetRuntimeAPIKey(id, "runtime-again")
		check(id+": the runtime key returns", wantRadius, wantAcme)
		registry.RemoveRuntimeAPIKey(id)
		for name, credential := range map[string]ai.Credential{
			"an API key":   {Type: ai.CredentialAPIKey, Key: "stored-key"},
			"an OAuth key": {Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
		} {
			if err := auth.Set(id, credential); err != nil {
				t.Fatal(err)
			}
			check(id+": "+name+" is stored", wantRadius, wantAcme)
			if err := auth.Delete(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			check(id+": "+name+" is gone", false, false)
			if err := auth.Set(id, credential); err != nil {
				t.Fatal(err)
			}
			check(id+": "+name+" is stored again", wantRadius, wantAcme)
			if err := auth.Delete(t.Context(), id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Every Radius auth question answers a runtime key as Pi does (RuntimeCredentials, model-runtime.ts:219): the provider has configured auth, its status is the runtime source, its models are available and it counts as an available provider, as it does with a stored key.
func TestRadiusRuntimeKeyIsRadiusAuthEverywhere(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "")
	registry, _, _ := radiusTestRegistry(t, "", nil)
	if registry.HasConfiguredAuth(RadiusProviderID) {
		t.Skip("the environment configures Radius auth")
	}
	if registry.AvailableProviderCount() != 0 {
		t.Fatalf("AvailableProviderCount = %d with no auth", registry.AvailableProviderCount())
	}
	registry.SetRuntimeAPIKey(RadiusProviderID, "runtime-key")
	if !registry.HasConfiguredAuth(RadiusProviderID) {
		t.Error("HasConfiguredAuth is false for a Radius runtime key")
	}
	if status := registry.GetProviderAuthStatus(RadiusProviderID); !status.Configured || status.Source != ai.AuthSourceRuntime {
		t.Errorf("GetProviderAuthStatus = %+v, want configured from the runtime", status)
	}
	if registry.AvailableProviderCount() != 1 {
		t.Errorf("AvailableProviderCount = %d, want 1", registry.AvailableProviderCount())
	}
	if len(registry.GetAvailable()) == 0 || len(registry.GetAvailableModelData()) == 0 {
		t.Errorf("a Radius runtime key lists %d entries and %d models, want the catalog", len(registry.GetAvailable()), len(registry.GetAvailableModelData()))
	}
	if key, err := registry.RadiusAPIKey(t.Context(), RadiusProviderID); err != nil || key != "runtime-key" {
		t.Errorf("RadiusAPIKey = %q, %v, want the runtime key", key, err)
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
