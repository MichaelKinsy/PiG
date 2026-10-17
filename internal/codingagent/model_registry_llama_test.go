//go:build !pig_strip_llama_cpp

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
	if err := registry.RegisterExtensionProvider("dyn", extension.ProviderConfig{APIKey: "key", BaseURL: "http://a/v1", API: "openai-completions", Models: []extension.ProviderModelConfig{{ID: "m"}}}); err != nil {
		t.Error(err)
	}
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
func TestLlamaProviderPublishesCatalogIntoModelRegistry(t *testing.T) {
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
	registry.SetModelsStore(ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")))
	if err := registry.RegisterNativeModelsProvider(llama.CreateLlamaProvider().ModelsProvider()); err != nil {
		t.Fatal(err)
	}
	if registry.HasConfiguredAuth(llama.LlamaProviderID) || len(registry.GetAvailable()) != 0 {
		t.Fatal("unconfigured llama.cpp provider is available")
	}
	if err := auth.Set(llama.LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}
	refreshLlamaProvider(t, registry)
	entry, ok := registry.Resolve(llama.LlamaProviderID, "qwen")
	if !ok || entry.BaseURL != server.URL+"/v1" || entry.API != "openai-completions" ||
		entry.ContextWindow != 8192 || !entry.Reasoning || entry.Compat == nil || entry.Compat.ThinkingFormat != "qwen-chat-template" {
		t.Fatalf("Resolve(llama.cpp/qwen) = %+v, %v", entry, ok)
	}
	if resolved, err := registry.GetProviderAuth(context.Background(), llama.LlamaProviderID); err != nil || resolved == nil || resolved.Auth.APIKey != "local" || resolved.Auth.BaseURL != server.URL+"/v1" {
		t.Fatalf("GetProviderAuth = %+v, %v; want the request auth the stream sends", resolved, err)
	}
	if available := registry.GetAvailable(); len(available) != 1 || available[0].ModelID != "qwen" {
		t.Fatalf("GetAvailable = %+v", available)
	}

	if err := auth.Delete(t.Context(), llama.LlamaProviderID); err != nil {
		t.Fatal(err)
	}
	refreshLlamaProvider(t, registry)
	if registry.HasConfiguredAuth(llama.LlamaProviderID) || len(registry.GetAvailable()) != 0 {
		t.Fatal("llama.cpp models stay available after the credential is removed")
	}
}

func refreshLlamaProvider(t *testing.T, registry *ModelRegistry) {
	t.Helper()
	allowNetwork := true
	result := registry.RefreshModelRuntime(context.Background(), ai.ModelsRefreshOptions{Providers: []string{llama.LlamaProviderID}, AllowNetwork: &allowNetwork})
	if err := result.Errors[llama.LlamaProviderID]; err != nil || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
}
