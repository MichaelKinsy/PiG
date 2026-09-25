package llama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/configvalue"
)

type fakeRegistry struct {
	mu      sync.Mutex
	configs []extension.ProviderConfig
}

func (r *fakeRegistry) SetProvider(name string, config extension.ProviderConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name != LlamaProviderID {
		panic("unexpected provider " + name)
	}
	r.configs = append(r.configs, config)
}

func (r *fakeRegistry) last() extension.ProviderConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configs[len(r.configs)-1]
}

func newTestHost(t *testing.T, dir string) (*Host, *fakeRegistry, *ai.AuthStorage) {
	t.Helper()
	credentials, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry := &fakeRegistry{}
	host := NewHost(registry, credentials, ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")))
	return host, registry, credentials
}

func clearLlamaEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
}

func TestHostRegistersADormantProviderUntilConfigured(t *testing.T) {
	clearLlamaEnv(t)
	_, registry, _ := newTestHost(t, t.TempDir())
	want := extension.ProviderConfig{
		Name:    "llama.cpp",
		BaseURL: "http://127.0.0.1:8080/v1",
		API:     "openai-completions",
		Models:  []extension.ProviderModelConfig{},
	}
	if got := registry.last(); !reflect.DeepEqual(got, want) {
		t.Fatalf("registration = %+v, want %+v", got, want)
	}
}

func TestHostRefreshPersistsCatalogAndRestoresItCacheOnly(t *testing.T) {
	clearLlamaEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			writeJSON(w, map[string]any{"data": []any{
				map[string]any{"id": "loaded", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 4096}},
				map[string]any{"id": "idle", "status": map[string]any{"value": "unloaded"}},
			}})
		case "/props":
			writeJSON(w, map[string]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	host, registry, credentials := newTestHost(t, dir)
	if err := credentials.Set(LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Key: "a$$b", Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}

	if result := host.Refresh(context.Background(), true); result.Err != nil || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
	config := registry.last()
	if config.APIKey != "a$$b" || config.BaseURL != server.URL+"/v1" {
		t.Fatalf("resolved registration = %+v", config)
	}
	if len(config.Models) != 1 || config.Models[0].ID != "loaded" || config.Models[0].ContextWindow != 4096 || config.Models[0].BaseURL != "" {
		t.Fatalf("registered models = %+v", config.Models)
	}

	server.Close()
	restored, restoredRegistry, _ := newTestHost(t, dir)
	if result := restored.Refresh(context.Background(), false); result.Err != nil {
		t.Fatalf("cache-only refresh = %+v", result)
	}
	if got := restoredRegistry.last().Models; len(got) != 1 || got[0].ID != "loaded" {
		t.Fatalf("restored models = %+v", got)
	}
	if got := restored.Provider().GetModels(); len(got) != 1 || got[0].BaseURL != server.URL+"/v1" {
		t.Fatalf("restored provider models = %+v", got)
	}
}

func TestHostRefreshReportsNetworkErrorsAndAbortedCallers(t *testing.T) {
	clearLlamaEnv(t)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	t.Setenv("LLAMA_BASE_URL", closedURL)
	host, _, _ := newTestHost(t, t.TempDir())
	if result := host.Refresh(context.Background(), true); result.Err == nil || result.Err.Error() != "fetch failed" {
		t.Fatalf("refresh error = %+v, want fetch failed", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := host.Refresh(ctx, true); !result.Aborted || result.Err != nil {
		t.Fatalf("aborted refresh = %+v", result)
	}
}

func TestLiteralConfigValueSurvivesConfigValueResolution(t *testing.T) {
	cases := map[string]string{"local": "local", "a$b": "a$$b", "!cmd": "$!cmd", "$X!": "$$X!"}
	for input, want := range cases {
		got := literalConfigValue(input)
		if got != want {
			t.Errorf("literalConfigValue(%q) = %q, want %q", input, got, want)
		}
		if resolved := configvalue.Resolve(got, nil); resolved != input {
			t.Errorf("Resolve(%q) = %q, want %q", got, resolved, input)
		}
	}
}
