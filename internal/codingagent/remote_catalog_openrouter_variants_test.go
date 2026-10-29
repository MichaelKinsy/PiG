package codingagent

// pig divergence D199: OpenRouter's :nitro and :floor routing variants follow every OpenRouter chat model of the current list, whether that
// list is the bundled catalog or the one a refresh (pig update --models) saved. The refreshed list replaces the bundled one, so the variants
// are derived from it where it is read, not from the bundled models alone.

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func openRouterRefreshedModel(id string) *ai.Model {
	return &ai.Model{
		ID: id, DisplayName: "Refreshed " + id, Input: []string{"text"},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "openrouter", API: "openai-completions", BaseURL: "https://openrouter.ai/api/v1"},
		Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100},
	}
}

func openRouterModelIDs(models []*ai.Model) []string {
	var ids []string
	for _, model := range models {
		if model.ProviderMeta.ProviderID == "openrouter" {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

var refreshedOpenRouterIDs = []string{"vendor/refreshed", "vendor/refreshed:nitro", "vendor/refreshed:floor"}

// The refresh path: the provider fetches the catalog from the catalog server, personalizes it, saves it, and lists it in place of the bundled models.
func TestRefreshedOpenRouterProviderListsRoutingVariantsOfTheRefreshedList(t *testing.T) {
	server := newCatalogServer(t, true, catalogJSONResponse(map[string]string{"last-modified": "Thu, 01 Jan 2099 00:00:00 GMT"}, map[string]any{
		"vendor/refreshed": map[string]any{
			"id": "vendor/refreshed", "name": "Refreshed vendor/refreshed", "api": "openai-completions", "provider": "openrouter", "baseUrl": "https://openrouter.ai/api/v1",
			"reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100,
		},
	}))
	keepRefreshed := func(catalog []ai.AnyModel, _ ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		return slices.DeleteFunc(slices.Clone(catalog), func(model ai.AnyModel) bool { return model.ModelID() != "vendor/refreshed" }), nil
	}
	provider := WithPersonalizedCatalog(builtinTypedBase(openRouterProviderID, nil), keepRefreshed, server.URL, nil)
	store := ai.NewInMemoryModelsStore()
	if err := refreshCatalogProvider(t, provider, store, refreshOverrides{}); err != nil {
		t.Fatal(err)
	}
	if server.count() != 1 {
		t.Fatalf("catalog requests = %d, want 1", server.count())
	}
	requireIDs(t, "chat models after the refresh", catalogModelIDs(t, provider), refreshedOpenRouterIDs)
	requireIDs(t, "all models after the refresh", catalogAllModelIDs(t, provider), refreshedOpenRouterIDs)
	// The saved list is the refreshed list as the catalog returned it: the variants are derived where it is read.
	requireIDs(t, "saved models", storedCatalogIDs(t, store), []string{"vendor/refreshed"})
}

// Every registry listing of a saved OpenRouter list: the composed provider data, all-provider data, the native collection, the selection metadata, a lookup by id.
func TestSavedOpenRouterListShowsRoutingVariantsOnEveryRegistryListing(t *testing.T) {
	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set(openRouterProviderID, ai.Credential{Type: ai.CredentialAPIKey, Key: "fixture-key"}); err != nil {
		t.Fatal(err)
	}
	store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
	if err := store.Write(context.Background(), openRouterProviderID, ai.ModelsStoreEntry{Models: []ai.AnyModel{openRouterRefreshedModel("vendor/refreshed")}, CheckedAt: new(float64(1))}); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	registry.SetAuthStorage(auth)
	registry.SetModelsStore(store)
	if result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: false, Providers: []string{openRouterProviderID}}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("restore = %+v", result)
	}

	requireIDs(t, "GetProviderModelData", openRouterModelIDs(registry.GetProviderModelData(openRouterProviderID)), refreshedOpenRouterIDs)
	requireIDs(t, "GetAllModelData", openRouterModelIDs(registry.GetAllModelData()), refreshedOpenRouterIDs)
	requireIDs(t, "GetNativeModels", openRouterModelIDs(registry.GetNativeModels(openRouterProviderID)), refreshedOpenRouterIDs)
	available, err := registry.GetAvailableModelDataContext(context.Background(), openRouterProviderID)
	if err != nil {
		t.Fatal(err)
	}
	requireIDs(t, "GetAvailableModelDataContext", openRouterModelIDs(available), refreshedOpenRouterIDs)
	var selection []string
	for _, model := range registry.RuntimeModels() {
		if model.Provider == openRouterProviderID {
			selection = append(selection, model.ID)
		}
	}
	requireIDs(t, "RuntimeModels", selection, refreshedOpenRouterIDs)
	variant := registry.GetProviderModelDataByID(openRouterProviderID, "vendor/refreshed:nitro")
	if variant == nil || variant.DisplayName != "Refreshed vendor/refreshed (nitro)" || variant.Capabilities.ContextWindow != 1000 {
		t.Errorf("GetProviderModelDataByID(vendor/refreshed:nitro) = %+v, want the variant of the refreshed model", variant)
	}
}
