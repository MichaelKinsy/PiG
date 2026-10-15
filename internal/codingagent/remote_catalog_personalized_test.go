package codingagent

// pi-mono ee409a85 packages/coding-agent/test/remote-catalog-provider.test.ts "personalized catalog provider", and the registry wiring of
// model-runtime.ts:create (withPersonalizedCatalog for provider "openrouter").

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func catalogImageModel() *ai.ImageModel {
	return &ai.ImageModel{ID: "image", Name: "image", API: "openrouter-images", Provider: "test-provider", BaseURL: "https://example.test/v1", Input: []string{"text"}, Output: []string{"image"}}
}

func catalogAllModelIDs(t *testing.T, provider *ai.ModelsProvider) []string {
	t.Helper()
	models, err := provider.GetAllModels()
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ModelID())
	}
	return ids
}

func TestPersonalizedCatalogProviderKeepsTheSavedListAndSavesNothingWhenPersonalizationFails(t *testing.T) {
	// Saving the catalog without personalization would replace the key's list with every model.
	server := newCatalogServer(t, true, catalogResponse{status: http.StatusNotFound, body: "not found"})
	store := ai.NewInMemoryModelsStore()
	saved := ai.ModelsStoreEntry{Models: []ai.AnyModel{catalogStaticModel("allowed")}, CheckedAt: new(float64(1))}
	if err := store.Write(t.Context(), "test-provider", saved); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("user models request failed")
	provider := WithPersonalizedCatalog(catalogBuiltinProvider(), func([]ai.AnyModel, ai.RefreshModelsContext) ([]ai.AnyModel, error) { return nil, failure }, server.URL, nil)

	if err := refreshCatalogProvider(t, provider, store, refreshOverrides{}); !errors.Is(err, failure) {
		t.Fatalf("refresh error = %v, want %v", err, failure)
	}
	requireIDs(t, "models", catalogModelIDs(t, provider), []string{"allowed"})
	requireIDs(t, "saved models", storedCatalogIDs(t, store), []string{"allowed"})
	if entry, _ := store.Read(t.Context(), "test-provider"); entry == nil || entry.CheckedAt == nil || *entry.CheckedAt != 1 {
		t.Fatalf("saved entry = %+v, want checkedAt 1 untouched", entry)
	}
}

func TestPersonalizedCatalogProviderRestoresASavedListAsIsAndFallsBackToTheBuiltInModelsOnlyWhenItIsEmpty(t *testing.T) {
	provider := WithPersonalizedCatalog(catalogBuiltinProvider(), func([]ai.AnyModel, ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		return nil, errors.New("not used offline")
	}, "", nil)
	store := ai.NewInMemoryModelsStore()
	offline := refreshOverrides{allowNetwork: new(false)}

	// pi versions before per-key lists stored the plain pi.dev catalog here, and saved an empty list after a missing pi.dev catalog or a failed first request.
	// Only such an entry can be empty.
	if err := store.Write(t.Context(), provider.ID, ai.ModelsStoreEntry{Models: []ai.AnyModel{}, CheckedAt: new(float64(1))}); err != nil {
		t.Fatal(err)
	}
	if err := refreshCatalogProvider(t, provider, store, offline); err != nil {
		t.Fatal(err)
	}
	requireIDs(t, "models of an empty saved list", catalogModelIDs(t, provider), []string{"static"})

	// A key that may use no chat models keeps its image models and shows no chat models.
	if err := store.Write(t.Context(), provider.ID, ai.ModelsStoreEntry{Models: []ai.AnyModel{catalogImageModel()}, CheckedAt: new(float64(1))}); err != nil {
		t.Fatal(err)
	}
	if err := refreshCatalogProvider(t, provider, store, offline); err != nil {
		t.Fatal(err)
	}
	requireIDs(t, "chat models of an image-only list", catalogModelIDs(t, provider), []string{})
	requireIDs(t, "all models of an image-only list", catalogAllModelIDs(t, provider), []string{"image"})
}

// withPersonalizedCatalog: the list personalize receives is the built-in models plus a pi.dev catalog newer than the bundled data, the result is saved without
// etag or Last-Modified, replaces the catalog, and the refresh interval and revalidation do not apply (every refresh requests the catalog and personalizes again).
func TestPersonalizedCatalogProviderPersonalizesTheBuiltInModelsPlusANewerPiDevCatalogOnEveryRefresh(t *testing.T) {
	newer := map[string]string{"last-modified": "Thu, 01 Jan 2099 00:00:00 GMT", "etag": `"v1"`}
	server := newCatalogServer(t, true, catalogJSONResponse(newer, map[string]any{"remote": catalogChatModel("remote")}))
	var received [][]string
	personalize := func(catalog []ai.AnyModel, _ ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		ids := []string{}
		for _, model := range catalog {
			ids = append(ids, model.ModelID())
		}
		received = append(received, ids)
		return slices.DeleteFunc(slices.Clone(catalog), func(model ai.AnyModel) bool { return model.ModelID() == "static" }), nil
	}
	provider := WithPersonalizedCatalog(catalogBuiltinProvider(), personalize, server.URL, new(float64(1)))
	store := ai.NewInMemoryModelsStore()

	for range 2 {
		if err := refreshCatalogProvider(t, provider, store, refreshOverrides{}); err != nil {
			t.Fatal(err)
		}
	}
	if server.count() != 2 {
		t.Fatalf("catalog requests = %d, want one per refresh", server.count())
	}
	if got := server.request(1).header.Get("if-none-match"); got != "" {
		t.Fatalf("a personalized refresh revalidated with %q", got)
	}
	for _, ids := range received {
		requireIDs(t, "catalog given to personalize", ids, []string{"static", "remote"})
	}
	requireIDs(t, "models", catalogModelIDs(t, provider), []string{"remote"})
	requireIDs(t, "saved models", storedCatalogIDs(t, store), []string{"remote"})
	if entry, _ := store.Read(t.Context(), "test-provider"); entry == nil || entry.ETag != "" || entry.LastModified != nil || entry.CheckedAt == nil {
		t.Fatalf("saved entry = %+v, want only models and checkedAt", entry)
	}

	// A pi.dev catalog that is not newer than the bundled data is not offered to personalize; a catalog the server lacks (404, 501) is skipped, other failures reject.
	old := newCatalogServer(t, true, catalogJSONResponse(map[string]string{"last-modified": "Thu, 01 Jan 1970 00:00:00 GMT"}, map[string]any{"remote": catalogChatModel("remote")}))
	received = nil
	older := WithPersonalizedCatalog(catalogBuiltinProvider(), personalize, old.URL, new(float64(1e12)))
	if err := refreshCatalogProvider(t, older, ai.NewInMemoryModelsStore(), refreshOverrides{}); err != nil {
		t.Fatal(err)
	}
	requireIDs(t, "catalog given to personalize with an older pi.dev catalog", received[0], []string{"static"})
	for _, status := range []int{http.StatusNotFound, http.StatusNotImplemented} {
		missing := newCatalogServer(t, true, catalogResponse{status: status})
		received = nil
		if err := refreshCatalogProvider(t, WithPersonalizedCatalog(catalogBuiltinProvider(), personalize, missing.URL, nil), ai.NewInMemoryModelsStore(), refreshOverrides{}); err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		requireIDs(t, "catalog given to personalize without a pi.dev catalog", received[0], []string{"static"})
	}
	broken := newCatalogServer(t, true, catalogResponse{status: http.StatusInternalServerError})
	if err := refreshCatalogProvider(t, WithPersonalizedCatalog(catalogBuiltinProvider(), personalize, broken.URL, nil), ai.NewInMemoryModelsStore(), refreshOverrides{}); err == nil || err.Error() != "Model catalog request failed for test-provider: 500" {
		t.Fatalf("a 500 from the catalog: %v", err)
	}
}

// model-runtime.ts:create wires withPersonalizedCatalog and personalizeOpenRouterCatalog for provider "openrouter": a refresh with the user's key asks the provider's
// configured base URL (a models.json override, here a regional host) for /models/user, keeps only the listed chat models with the key's limits, and the composed
// catalog lists exactly those, offline too.
func TestRegistryPersonalizesTheOpenRouterCatalogThroughTheConfiguredBaseURL(t *testing.T) {
	var mu sync.Mutex
	var userModelRequests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/regional/v1/models/user" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		userModelRequests = append(userModelRequests, r.Header.Get("authorization"))
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "openai/gpt-4o-mini", "top_provider": map[string]any{"context_length": 4321, "max_completion_tokens": 123}, "pricing": map[string]any{"prompt": "0.000002", "completion": "0.000004"}}}})
	}))
	defer server.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"openrouter":{"baseUrl":"`+server.URL+`/regional/v1"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("openrouter", ai.Credential{Type: ai.CredentialAPIKey, Key: "sk-or"}); err != nil {
		t.Fatal(err)
	}
	store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
	newRegistry := func() *ModelRegistry {
		registry := NewModelRegistry(dir)
		registry.SetAuthStorage(auth)
		registry.SetModelsStore(store)
		registry.SetCatalogBaseURL(server.URL)
		return registry
	}
	ids := func(registry *ModelRegistry) []string {
		var out []string
		for _, model := range registry.GetProviderModelData("openrouter") {
			out = append(out, model.ID)
		}
		return out
	}

	registry := newRegistry()
	if len(ids(registry)) < 2 {
		t.Fatalf("the bundled openrouter catalog lists %v", ids(registry))
	}
	if result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: true, Providers: []string{"openrouter"}}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
	if got := ids(registry); !slices.Equal(got, []string{"openai/gpt-4o-mini"}) {
		t.Fatalf("personalized chat models = %v, want only the listed model", got)
	}
	listed := registry.GetProviderModelData("openrouter")[0].Capabilities
	if listed.ContextWindow != 4321 || listed.MaxOutputTokens != 123 || listed.InputCostPer1M != 2 || listed.OutputCostPer1M != 4 {
		t.Fatalf("listed model limits and prices = %+v", listed)
	}
	mu.Lock()
	if len(userModelRequests) != 1 || userModelRequests[0] != "Bearer sk-or" {
		t.Fatalf("/models/user requests = %v, want one with the key", userModelRequests)
	}
	mu.Unlock()

	// A new registry over the same store restores the personalized list as is, without the network.
	restored := newRegistry()
	restored.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: false, Providers: []string{"openrouter"}})
	if got := ids(restored); !slices.Equal(got, []string{"openai/gpt-4o-mini"}) {
		t.Fatalf("restored models = %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(userModelRequests) != 1 {
		t.Fatalf("the offline refresh requested /models/user again: %v", userModelRequests)
	}
}
