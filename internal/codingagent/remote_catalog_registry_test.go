package codingagent

// pi: packages/coding-agent/src/core/remote-catalog-provider.ts

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

// upstream model-runtime.ts:225-231 and remote-catalog-provider.ts:52-57: the registry passes the bundled catalog's generation time to withRemoteCatalog, so a stored remote catalog whose `Last-Modified` is not later than it (or is missing) adds no models, and a later one does.
func TestRegistryIgnoresAStoredRemoteCatalogThatIsNotNewerThanTheBundledOne(t *testing.T) {
	generatedAt := ai.GetBuiltinModelDataGeneratedAt()
	if generatedAt == nil {
		t.Fatal("the generated catalogs record no generation time")
	}
	overlay := []json.RawMessage{json.RawMessage(`{"id":"remote/new-chat","name":"New chat","api":"openai-completions","provider":"openrouter","baseUrl":"https://openrouter.ai/api/v1","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}`)}
	lastModified := func(delta float64) *float64 { return new(*generatedAt + delta) }
	for name, tc := range map[string]struct {
		lastModified *float64
		want         bool
	}{
		"older":    {lastModified(-1), false},
		"same":     {lastModified(0), false},
		"missing":  {nil, false},
		"newer":    {lastModified(1), true},
		"far away": {lastModified(1e12), true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
			if err := store.Write(t.Context(), "openrouter", ai.ModelsStoreEntry{Models: mustStoredModels(overlay), CheckedAt: new(float64(1)), LastModified: tc.lastModified}); err != nil {
				t.Fatal(err)
			}
			registry := NewModelRegistry(dir)
			registry.SetModelsStore(store)
			registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: false, Providers: []string{"openrouter"}})
			got := slices.ContainsFunc(registry.GetProviderModelData("openrouter"), func(model *ai.Model) bool { return model.ID == "remote/new-chat" })
			if got != tc.want {
				t.Fatalf("overlay model listed = %v, want %v", got, tc.want)
			}
		})
	}
}
