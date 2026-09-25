package codingagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
)

func TestModelRegistrySetProviderReplacesInsteadOfMerging(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	registry.RegisterProvider("dyn", extension.ProviderConfig{APIKey: "key", BaseURL: "http://a/v1", API: "openai-completions", Models: []extension.ProviderModelConfig{{ID: "m"}}})
	if !registry.HasConfiguredAuth("dyn") {
		t.Fatal("registered provider with a key is not configured")
	}
	registry.SetProvider("dyn", extension.ProviderConfig{BaseURL: "http://b/v1", API: "openai-completions", Models: []extension.ProviderModelConfig{}})
	if registry.HasConfiguredAuth("dyn") {
		t.Fatal("SetProvider kept the previous API key")
	}
	if entry, ok := registry.Resolve("dyn", "m"); !ok || entry.BaseURL != "http://b/v1" || entry.APIKey != "" {
		t.Fatalf("Resolve after SetProvider = %+v, %v", entry, ok)
	}
}

// The built-in llama.cpp provider publishes its catalog and resolved auth into
// the registry: models appear once a credential resolves and disappear again
// when it is removed, as upstream availability follows provider auth.
func TestLlamaHostPublishesCatalogIntoModelRegistry(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "qwen", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 8192}}}})
		case "/props":
			_ = json.NewEncoder(w).Encode(map[string]any{"chat_template": "enable_thinking"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	registry.SetAuthStorage(auth)
	host := llama.NewHost(registry, auth, ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")))
	if registry.HasConfiguredAuth(llama.LlamaProviderID) || len(registry.GetAvailable()) != 0 {
		t.Fatal("unconfigured llama.cpp provider is available")
	}
	if err := auth.Set(llama.LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}
	if result := host.Refresh(context.Background(), true); result.Err != nil {
		t.Fatal(result.Err)
	}
	entry, ok := registry.Resolve(llama.LlamaProviderID, "qwen")
	if !ok || entry.APIKey != "local" || entry.BaseURL != server.URL+"/v1" || entry.API != "openai-completions" ||
		entry.ContextWindow != 8192 || !entry.Reasoning || entry.Compat == nil || entry.Compat.ThinkingFormat != "qwen-chat-template" {
		t.Fatalf("Resolve(llama.cpp/qwen) = %+v, %v", entry, ok)
	}
	if available := registry.GetAvailable(); len(available) != 1 || available[0].ModelID != "qwen" {
		t.Fatalf("GetAvailable = %+v", available)
	}

	if err := auth.Delete(llama.LlamaProviderID); err != nil {
		t.Fatal(err)
	}
	host.SyncRegistration(context.Background())
	if registry.HasConfiguredAuth(llama.LlamaProviderID) || len(registry.GetAvailable()) != 0 {
		t.Fatal("llama.cpp models stay available after the credential is removed")
	}
}
