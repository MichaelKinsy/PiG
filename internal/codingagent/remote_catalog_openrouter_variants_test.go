package codingagent

// pig divergence D199: OpenRouter's :nitro and :floor routing variants follow every OpenRouter chat model of the current list, whether that
// list is the bundled catalog or the one a refresh (pig update --models) saved. The refreshed list replaces the bundled one, so the variants
// are derived from it where it is read, not from the bundled models alone.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func openRouterRefreshedChatModel(id string) map[string]any {
	return map[string]any{
		"id": id, "name": "Refreshed " + id, "api": "openai-completions", "provider": "openrouter", "baseUrl": "https://openrouter.ai/api/v1",
		"reasoning": false, "input": []string{"text"},
		"cost":          map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0},
		"contextWindow": 1000, "maxTokens": 100,
	}
}

func openRouterIDs(models []*ai.Model) []string {
	var ids []string
	for _, model := range models {
		if model.ProviderMeta.ProviderID == "openrouter" {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

func TestRefreshedOpenRouterCatalogListsRoutingVariantsOnEveryListingPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/providers/openrouter" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.Header().Set("last-modified", "Thu, 01 Jan 2099 00:00:00 GMT")
		_ = json.NewEncoder(w).Encode(map[string]any{"vendor/refreshed": openRouterRefreshedChatModel("vendor/refreshed")})
	}))
	defer server.Close()

	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	// No credential: the personalization request to OpenRouter's /models/user is skipped, so the saved list is the bundled models plus the fake catalog's.
	registry := NewModelRegistry(dir)
	registry.SetAuthStorage(auth)
	registry.SetModelsStore(ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")))
	registry.SetCatalogBaseURL(server.URL)
	if result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: true, Providers: []string{"openrouter"}}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}

	want := []string{"vendor/refreshed", "vendor/refreshed:nitro", "vendor/refreshed:floor"}
	listings := map[string][]string{
		"GetProviderModelData": openRouterIDs(registry.GetProviderModelData("openrouter")),
		"GetAllModelData":      openRouterIDs(registry.GetAllModelData()),
		"GetNativeModels":      openRouterIDs(registry.GetNativeModels("openrouter")),
	}
	for name, ids := range listings {
		if !slices.Contains(ids, "vendor/refreshed") {
			t.Fatalf("%s does not list the refreshed model; got %d OpenRouter models", name, len(ids))
		}
		for _, id := range want {
			if !slices.Contains(ids, id) {
				t.Errorf("%s lists no %q beside the refreshed model (D199)", name, id)
			}
		}
	}
	var runtime []string
	for _, model := range registry.RuntimeModels() {
		if model.Provider == "openrouter" {
			runtime = append(runtime, model.ID)
		}
	}
	for _, id := range want {
		if !slices.Contains(runtime, id) {
			t.Errorf("RuntimeModels lists no %q beside the refreshed model (D199)", id)
		}
	}
	if model := registry.GetProviderModelDataByID("openrouter", "vendor/refreshed:nitro"); model == nil || model.DisplayName != "Refreshed vendor/refreshed (nitro)" {
		t.Errorf("GetProviderModelDataByID(vendor/refreshed:nitro) = %+v, want the variant of the refreshed model", model)
	}
	// Every variant sits right after its model and copies it but for id and name.
	ids := openRouterIDs(registry.GetProviderModelData("openrouter"))
	at := slices.Index(ids, "vendor/refreshed")
	if at < 0 || len(ids) < at+3 || ids[at+1] != "vendor/refreshed:nitro" || ids[at+2] != "vendor/refreshed:floor" {
		t.Errorf("variants do not follow the refreshed model: %v", ids)
	}
}
