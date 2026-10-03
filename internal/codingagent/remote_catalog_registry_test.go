package codingagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The remote catalog overlay reaches the built-in providers of a registry: a refresh with credentials fetches it, persists it, and the
// composed catalog lists its models (upstream: model-runtime.ts:create wraps every built-in provider but radius with withRemoteCatalog).
func TestRegistryAppliesTheRemoteCatalogOverlayToBuiltInProviders(t *testing.T) {
	var mu sync.Mutex
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requested = append(requested, r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/api/models/providers/openrouter" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.Header().Set("last-modified", "Thu, 01 Jan 2099 00:00:00 GMT")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"remote/new-chat": map[string]any{"id": "remote/new-chat", "name": "New chat", "api": "openai-completions", "baseUrl": "https://openrouter.ai/api/v1", "reasoning": false, "input": []string{"text"},
				"cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100},
			"remote/flux": map[string]any{"type": "image", "id": "remote/flux", "name": "FLUX", "api": "openrouter-images", "baseUrl": "https://openrouter.ai/api/v1", "input": []string{"text"}, "output": []string{"image"},
				"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
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
	hasModel := func(registry *ModelRegistry, id string) bool {
		return slices.ContainsFunc(registry.GetProviderModelData("openrouter"), func(model *ai.Model) bool { return model.ID == id })
	}

	registry := newRegistry()
	bundled := len(registry.GetProviderModelData("openrouter"))
	if bundled == 0 {
		t.Fatal("openrouter has no bundled models")
	}
	if hasModel(registry, "remote/new-chat") {
		t.Fatal("the overlay model is listed before any refresh")
	}
	if result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: true, Providers: []string{"openrouter"}}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
	if !hasModel(registry, "remote/new-chat") {
		t.Fatal("the refreshed overlay model is not listed")
	}
	if got := len(registry.GetProviderModelData("openrouter")); got != bundled+1 {
		t.Fatalf("openrouter models = %d, want the %d bundled models plus the overlay chat model", got, bundled)
	}
	var image bool
	for _, model := range registry.GetProviderAllModelData("openrouter") {
		image = image || (model.ModelType() == ai.ModelTypeImage && model.ModelID() == "remote/flux")
	}
	if !image {
		t.Fatal("the overlay image model is not listed with the typed models")
	}
	mu.Lock()
	if count := len(slices.DeleteFunc(slices.Clone(requested), func(path string) bool { return path != "/api/models/providers/openrouter" })); count != 1 {
		t.Fatalf("openrouter catalog requests = %d, want 1 (%v)", count, requested)
	}
	requested = nil
	mu.Unlock()

	// A new registry over the same store restores the overlay without the network.
	restored := newRegistry()
	restored.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: false, Providers: []string{"openrouter"}})
	if !hasModel(restored, "remote/new-chat") {
		t.Fatal("the stored overlay model is not restored from the models store")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requested) != 0 {
		t.Fatalf("cache-only refresh requested %v", requested)
	}
}
