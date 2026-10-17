package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// providers/all.ts:169: builtinProviders() builds Radius with radiusProvider(), so builtinModels() (and compat.ts:217) refreshes the account's gateway catalog instead of serving a static one.
func TestBuiltinModelsRadiusIsRadiusProvider(t *testing.T) {
	requests := routeRadiusGateway(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(radiusTestConfig))
	})
	credentials := NewInMemoryCredentialStore()
	if err := setRadiusKey(credentials); err != nil {
		t.Fatal(err)
	}
	models := BuiltinModels(CreateModelsOptions{Credentials: credentials, ModelsStore: NewInMemoryModelsStore()})
	provider := models.GetProvider("radius")
	if provider == nil || provider.RefreshModels == nil {
		t.Fatal("builtin radius provider has no refreshModels")
	}
	if result := models.Refresh(context.Background(), ModelsRefreshOptions{Providers: []string{"radius"}}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
	if ids := radiusModelIDs(models.GetModels("radius")); !slices.Equal(ids, []string{"balanced", "organization-only"}) {
		t.Fatalf("builtin radius models after refresh = %v", ids)
	}
	if recorded := requests(); len(recorded) != 1 || recorded[0].URL != "https://radius.pi.dev/v1/config" {
		t.Fatalf("requests = %+v", recorded)
	}
}

// Ported through createModels from radius-provider.test.ts "replaces the static public catalog with a cached catalog without network access".
func TestRadiusProviderModelsRestoreCachedCatalogOffline(t *testing.T) {
	routeRadiusGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("offline refresh contacted the gateway") })
	store := NewInMemoryModelsStore()
	if err := store.Write(context.Background(), "radius", *radiusStoreEntry(GetRadiusModelsFromConfig("radius", radiusTestGatewayConfig(t)))); err != nil {
		t.Fatal(err)
	}
	models := CreateModels(CreateModelsOptions{ModelsStore: store})
	models.SetProvider(NewRadiusProvider(RadiusProviderOptions{}))

	models.Refresh(context.Background(), ModelsRefreshOptions{Providers: []string{"radius"}, AllowNetwork: new(false)})

	if model := models.GetModel("radius", "balanced"); model == nil || model.DisplayName != "Fresh Balanced" {
		t.Fatalf("balanced = %+v", model)
	}
	if ids := radiusModelIDs(models.GetModels("radius")); !slices.Equal(ids, []string{"balanced", "organization-only"}) {
		t.Fatalf("models = %v", ids)
	}
}

// Ported through createModels from radius-provider.test.ts "exposes no models when the organization disabled all of them".
func TestRadiusProviderModelsExposeNoModelsWhenAllDisabled(t *testing.T) {
	routeRadiusGateway(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(`{"baseUrl":"https://radius.example/v1","models":[]}`))
	})
	credentials := NewInMemoryCredentialStore()
	if err := setRadiusKey(credentials); err != nil {
		t.Fatal(err)
	}
	models := CreateModels(CreateModelsOptions{Credentials: credentials})
	models.SetProvider(NewRadiusProvider(RadiusProviderOptions{}))

	if result := models.Refresh(context.Background(), ModelsRefreshOptions{Providers: []string{"radius"}}); len(result.Errors) != 0 {
		t.Fatalf("errors = %+v", result.Errors)
	}
	if got := models.GetModels("radius"); len(got) != 0 {
		t.Fatalf("models = %v, want none", radiusModelIDs(got))
	}
}

// radius.ts:71: an aborted refresh does not contact the gateway.
func TestRadiusProviderAbortedRefreshSkipsTheGateway(t *testing.T) {
	routeRadiusGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("aborted refresh contacted the gateway") })
	provider := NewRadiusProvider(RadiusProviderOptions{})
	signal, cancel := context.WithCancel(context.Background())
	cancel()
	err := provider.RefreshModels(RefreshModelsContext{Credential: &Credential{Type: CredentialAPIKey, Key: "k"}, AllowNetwork: true, Signal: signal,
		Publish: func(ModelsPublication) (bool, error) { t.Error("aborted refresh published"); return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
}

// radius.ts:74: a refresh aborted after the gateway config loaded publishes nothing.
func TestRadiusProviderRefreshAbortedDuringLoadDoesNotPublish(t *testing.T) {
	signal, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := radiusHTTPClient
	radiusHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		cancel() // the abort lands while the load completes from an in-memory body
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(radiusTestConfig)), Request: request}, nil
	})}
	t.Cleanup(func() { radiusHTTPClient = original })
	provider := NewRadiusProvider(RadiusProviderOptions{})
	err := provider.RefreshModels(RefreshModelsContext{Credential: &Credential{Type: CredentialAPIKey, Key: "k"}, AllowNetwork: true, Signal: signal,
		Publish: func(ModelsPublication) (bool, error) {
			t.Error("refresh aborted during load published")
			return true, nil
		}})
	if err != nil {
		t.Fatalf("refresh aborted after the load = %v, want nil (radius.ts returns)", err)
	}
	if models, _ := provider.GetModels(); slices.Contains(radiusModelIDs(models), "organization-only") {
		t.Fatal("aborted refresh updated the catalog")
	}
}

// radius.ts:57: the legacy credential catalog is imported only when nothing is stored, and a superseded import stops the refresh (radius.ts:59-67).
func TestRadiusProviderLegacyImportGuards(t *testing.T) {
	legacy := &Credential{Type: CredentialOAuth, Access: "a", GatewayConfig: json.RawMessage(radiusTestConfig)}
	t.Run("stored catalog wins", func(t *testing.T) {
		provider := NewRadiusProvider(RadiusProviderOptions{})
		var publications int
		err := provider.RefreshModels(RefreshModelsContext{Credential: legacy, Stored: &ModelsStoreEntry{}, Signal: context.Background(),
			Publish: func(publication ModelsPublication) (bool, error) {
				publications++
				if publication.Persist != nil {
					t.Error("legacy catalog imported over a stored one")
				}
				publication.Update()
				return true, nil
			}})
		if err != nil || publications != 1 {
			t.Fatalf("err=%v publications=%d", err, publications)
		}
	})
	t.Run("superseded import stops", func(t *testing.T) {
		routeRadiusGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("superseded import contacted the gateway") })
		provider := NewRadiusProvider(RadiusProviderOptions{})
		err := provider.RefreshModels(RefreshModelsContext{Credential: legacy, AllowNetwork: true, Signal: context.Background(),
			Publish: func(ModelsPublication) (bool, error) { return false, nil }})
		if err != nil {
			t.Fatal(err)
		}
	})
}
