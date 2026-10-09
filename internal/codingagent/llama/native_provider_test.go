package llama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/llama-extension.test.ts: the provider object createLlamaProvider returns, run by pi-ai's Models as
// Pi's registerProvider does.

func clearLlamaEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
}

// newTestModels registers a fresh llama.cpp provider object in pi-ai's Models over a credential file and models store in dir.
func newTestModels(t *testing.T, dir string) (*ai.Models, *LlamaProviderController, *ai.AuthStorage) {
	t.Helper()
	credentials, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	models := ai.CreateModels(ai.CreateModelsOptions{Credentials: credentials, ModelsStore: ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))})
	controller := CreateLlamaProvider()
	models.SetProvider(controller.ModelsProvider())
	return models, controller, credentials
}

// testRegistry is pi-ai's Models as the ctx.modelRegistry of /llama: getProviderAuth and refresh.
type testRegistry struct{ *ai.Models }

func (r testRegistry) GetProviderAuth(ctx context.Context, id string) (*ai.AuthResult, error) {
	return r.GetAuth(ctx, id)
}

func (r testRegistry) Refresh(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	return r.Models.Refresh(ctx, options)
}

func refreshLlama(t *testing.T, models *ai.Models, allowNetwork bool) ai.ModelsRefreshResult {
	t.Helper()
	result := models.Refresh(context.Background(), ai.ModelsRefreshOptions{Providers: []string{LlamaProviderID}, AllowNetwork: &allowNetwork})
	if err := result.Errors[LlamaProviderID]; err != nil || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
	return result
}

func llamaServer(t *testing.T, models ...any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			writeJSON(w, map[string]any{"data": models})
		case "/props":
			writeJSON(w, map[string]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestModelsProviderIsDormantUntilConfigured(t *testing.T) {
	clearLlamaEnv(t)
	models, _, _ := newTestModels(t, t.TempDir())
	provider := models.GetProvider(LlamaProviderID)
	if provider == nil || provider.Name != "llama.cpp" || provider.BaseURL != "http://127.0.0.1:8080/v1" || provider.Auth.APIKey == nil {
		t.Fatalf("provider = %+v", provider)
	}
	if result, err := models.GetAuth(context.Background(), LlamaProviderID); err != nil || result != nil {
		t.Fatalf("auth = %+v, %v; want none without LLAMA_BASE_URL or a credential", result, err)
	}
	if got := models.GetAllModels(LlamaProviderID); len(got) != 0 {
		t.Fatalf("models = %+v, want none before a catalog", got)
	}
}

func TestModelsProviderRefreshPersistsCatalogAndRestoresItCacheOnly(t *testing.T) {
	clearLlamaEnv(t)
	server := llamaServer(t,
		map[string]any{"id": "loaded", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 4096}},
		map[string]any{"id": "idle", "status": map[string]any{"value": "unloaded"}},
	)
	dir := t.TempDir()
	models, _, credentials := newTestModels(t, dir)
	if err := credentials.Set(LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Key: "a$$b", Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}
	refreshLlama(t, models, true)

	auth, err := models.GetAuth(context.Background(), LlamaProviderID)
	if err != nil || auth == nil || auth.Auth.APIKey != "a$b" || auth.Auth.BaseURL != server.URL+"/v1" {
		t.Fatalf("auth = %+v, %v", auth, err)
	}
	// The listed models are getAllModels: the chat model, then its classifier (provider.ts:204, llama-extension.test.ts:145-204).
	all := models.GetAllModels(LlamaProviderID)
	if len(all) != 2 || all[0].ModelID() != "loaded" || all[0].ModelType() != ai.ModelTypeChat || all[1].ModelType() != ai.ModelTypeClassifier {
		t.Fatalf("models = %d %+v", len(all), all)
	}
	if chat := models.GetModels(LlamaProviderID); len(chat) != 1 || chat[0].Capabilities.ContextWindow != 4096 {
		t.Fatalf("chat models = %+v", chat)
	}

	server.Close()
	restored, controller, _ := newTestModels(t, dir)
	refreshLlama(t, restored, false)
	if got := restored.GetAllModels(LlamaProviderID); len(got) != 2 || got[0].ModelID() != "loaded" {
		t.Fatalf("restored models = %+v", got)
	}
	if got := controller.Provider.GetModels(); len(got) != 1 || got[0].BaseURL != server.URL+"/v1" {
		t.Fatalf("restored provider models = %+v", got)
	}
}

func TestModelsProviderRefreshReportsNetworkErrorsAndAbortedCallers(t *testing.T) {
	clearLlamaEnv(t)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	t.Setenv("LLAMA_BASE_URL", closedURL)
	models, _, _ := newTestModels(t, t.TempDir())
	allow := true
	result := models.Refresh(context.Background(), ai.ModelsRefreshOptions{Providers: []string{LlamaProviderID}, AllowNetwork: &allow})
	if err := result.Errors[LlamaProviderID]; err == nil || err.Error() != "fetch failed" {
		t.Fatalf("refresh = %+v, want fetch failed", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := models.Refresh(ctx, ai.ModelsRefreshOptions{Providers: []string{LlamaProviderID}, AllowNetwork: &allow}); !result.Aborted || result.Errors[LlamaProviderID] != nil {
		t.Fatalf("aborted refresh = %+v", result)
	}
}

func TestModelsProviderClassifiersRunThroughTheirOwnImplementation(t *testing.T) {
	clearLlamaEnv(t)
	server := llamaServer(t,
		map[string]any{"id": "qwen", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 4096}})
	models, _, credentials := newTestModels(t, t.TempDir())
	if err := credentials.Set(LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Key: "local", Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}
	refreshLlama(t, models, true)
	all := models.GetAllModels(LlamaProviderID)
	if len(all) != 2 {
		t.Fatalf("models = %+v, want the chat model then its classifier", all)
	}
	classifier, ok := all[1].(*ai.ClassifierModel)
	if !ok || classifier.ID != "qwen" || classifier.API != "llama-cpp-classify" || classifier.ContextWindow != 4096 || len(classifier.Input) != 1 || classifier.Input[0] != "text" {
		t.Fatalf("classifier = %+v", all[1])
	}
	if models.GetProvider(LlamaProviderID).Classify == nil {
		t.Fatal("the provider object has no classify operation")
	}
}

// A decision model is a classifier of the System One API and no chat model (provider.ts getAllModels, classify).
func TestModelsProviderListsDecisionModelsAsSystemOneClassifiersOnly(t *testing.T) {
	clearLlamaEnv(t)
	server := llamaServer(t, map[string]any{"id": "kev", "status": map[string]any{"value": "sleeping"}, "architecture": map[string]any{"output_modalities": []any{"decisions"}}, "meta": map[string]any{"n_ctx": 8192}})
	models, _, credentials := newTestModels(t, t.TempDir())
	if err := credentials.Set(LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Key: "local", Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}
	refreshLlama(t, models, true)
	if chat := models.GetModels(LlamaProviderID); len(chat) != 0 {
		t.Fatalf("chat models = %+v, want none for a decision model", chat)
	}
	all := models.GetAllModels(LlamaProviderID)
	classifier, ok := ai.AnyModel(nil), false
	if len(all) == 1 {
		classifier, ok = all[0], true
	}
	got, isClassifier := classifier.(*ai.ClassifierModel)
	if !ok || !isClassifier || got.ID != "kev" || got.API != "typesafe-system-one" || got.ContextWindow != 8192 {
		t.Fatalf("models = %+v, want the decision classifier only", all)
	}
}
