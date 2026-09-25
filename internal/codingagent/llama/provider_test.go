package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports the llama-extension.test.ts provider cases.

func modelIDs(models []Model) []string {
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func publishInto(cached **ai.ModelsStoreEntry) func(ModelsPublication) (bool, error) {
	return func(publication ModelsPublication) (bool, error) {
		if publication.Persist != nil {
			clone := *publication.Persist
			*cached = &clone
		}
		if publication.Update != nil {
			publication.Update()
		}
		return true, nil
	}
}

func apiKeyCredential(key, url string) *ai.Credential {
	return &ai.Credential{Type: ai.CredentialAPIKey, Key: key, Env: map[string]string{"LLAMA_BASE_URL": url}}
}

func TestSetCatalogExposesLoadedAndSleepingModelsWithRouterMetadata(t *testing.T) {
	controller := CreateLlamaProvider()
	nCtx, nCtxTrain := 65536.0, 131072.0
	controller.SetCatalog([]LlamaModelInfo{
		{
			ID:           "loaded",
			Status:       LlamaModelInfoStatus{Value: LlamaModelStatusLoaded, Args: []string{"llama-server", "--n-gpu-layers", "999"}},
			Architecture: &LlamaModelArchitecture{InputModalities: []string{"text", "image"}},
			Meta:         &LlamaModelMeta{NCtx: &nCtx, NCtxTrain: &nCtxTrain},
		},
		{ID: "sleeping", Status: LlamaModelInfoStatus{Value: LlamaModelStatusSleeping}},
		{ID: "unloaded", Status: LlamaModelInfoStatus{Value: LlamaModelStatusUnloaded}},
		{ID: "loading", Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoading}},
	}, "http://localhost:8080", false)

	models := controller.Provider.GetModels()
	if got := modelIDs(models); !reflect.DeepEqual(got, []string{"loaded", "sleeping"}) {
		t.Fatalf("models = %v", got)
	}
	loaded := models[0]
	if loaded.BaseURL != "http://localhost:8080/v1" || loaded.ContextWindow != 65536 || loaded.MaxTokens != 65536 ||
		!reflect.DeepEqual(loaded.Input, []string{"text", "image"}) {
		t.Fatalf("loaded model = %+v", loaded)
	}
	if sleeping := models[1]; sleeping.BaseURL != "http://localhost:8080/v1" || sleeping.ContextWindow != 128000 {
		t.Fatalf("sleeping model = %+v", sleeping)
	}
}

// Regression test for upstream #9528.
func TestRefreshModelsDiscoversChatTemplateThinkingSupportForLoadedModels(t *testing.T) {
	var propsRequests atomic.Int32
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			writeJSON(w, map[string]any{"data": []any{map[string]any{"id": "qwen", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 32768}}}})
		case "/props":
			propsRequests.Add(1)
			if r.URL.Query().Get("model") != "qwen" || r.URL.Query().Get("autoload") != "false" {
				t.Errorf("props query = %q", r.URL.RawQuery)
			}
			writeJSON(w, map[string]any{"chat_template": "{% if enable_thinking %}think{% endif %}"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	controller := CreateLlamaProvider()
	var cached *ai.ModelsStoreEntry
	err := controller.Provider.RefreshModels(RefreshModelsContext{
		Ctx:          context.Background(),
		Credential:   apiKeyCredential("local", url),
		Publish:      publishInto(&cached),
		AllowNetwork: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if propsRequests.Load() != 1 {
		t.Fatalf("props requests = %d, want 1", propsRequests.Load())
	}
	models := controller.Provider.GetModels()
	if len(models) != 1 || !models[0].Reasoning || models[0].Compat.ThinkingFormat != "qwen-chat-template" {
		t.Fatalf("models = %+v", models)
	}
	levels, _ := json.Marshal(models[0].ThinkingLevelMap)
	if string(levels) != `{"off":"off","minimal":null,"low":null,"medium":"medium","high":null,"xhigh":null}` {
		t.Fatalf("thinkingLevelMap = %s", levels)
	}
}

func TestRefreshModelsPersistsAndRestoresSelectableModelsForCacheOnlyStartup(t *testing.T) {
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case "/models":
			writeJSON(w, map[string]any{"data": []any{
				map[string]any{"id": "loaded", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 32768}},
				map[string]any{"id": "sleeping", "status": map[string]any{"value": "sleeping"}, "meta": map[string]any{"n_ctx": 32768}},
				map[string]any{"id": "unloaded", "status": map[string]any{"value": "unloaded"}},
			}})
		case "/props?model=loaded&autoload=false":
			writeJSON(w, map[string]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	var cached *ai.ModelsStoreEntry
	first := CreateLlamaProvider()
	if err := first.Provider.RefreshModels(RefreshModelsContext{
		Ctx: context.Background(), Credential: apiKeyCredential("local", url), Stored: cached,
		Publish: publishInto(&cached), AllowNetwork: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(first.Provider.GetModels()); !reflect.DeepEqual(got, []string{"loaded", "sleeping"}) {
		t.Fatalf("first models = %v", got)
	}
	if cached == nil || len(cached.Models) != 2 || cached.CheckedAt == nil {
		t.Fatalf("cached entry = %+v", cached)
	}
	var persisted Model
	if err := json.Unmarshal(cached.Models[0], &persisted); err != nil || persisted.ID != "loaded" {
		t.Fatalf("persisted model = %+v, %v", persisted, err)
	}

	second := CreateLlamaProvider()
	if err := second.Provider.RefreshModels(RefreshModelsContext{
		Ctx: context.Background(), Credential: apiKeyCredential("local", url), Stored: cached,
		Publish: publishInto(&cached), AllowNetwork: false,
	}); err != nil {
		t.Fatal(err)
	}
	models := second.Provider.GetModels()
	if got := modelIDs(models); !reflect.DeepEqual(got, []string{"loaded", "sleeping"}) {
		t.Fatalf("restored models = %v", got)
	}
	for _, model := range models {
		if model.BaseURL != url+"/v1" || model.ContextWindow != 32768 {
			t.Fatalf("restored model = %+v", model)
		}
	}
}

func TestRefreshModelsExposesUnloadedPresetsOnlyWhenRouterAutoloadIsEnabled(t *testing.T) {
	var propsRequests atomic.Int32
	autoload := true
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.RequestURI() {
		case "/models":
			writeJSON(w, map[string]any{"data": []any{
				map[string]any{"id": "preset", "status": map[string]any{"value": "unloaded"}, "source": "preset", "meta": map[string]any{"n_ctx": 65536}},
				map[string]any{"id": "failed-preset", "status": map[string]any{"value": "unloaded", "failed": true}, "source": "preset"},
				map[string]any{"id": "cache", "status": map[string]any{"value": "unloaded"}, "source": "cache"},
				map[string]any{"id": "models-dir", "status": map[string]any{"value": "unloaded"}, "source": "models_dir"},
			}})
		case "/props":
			propsRequests.Add(1)
			writeJSON(w, map[string]any{"role": "router", "models_autoload": autoload})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	var cached *ai.ModelsStoreEntry
	controller := CreateLlamaProvider()
	if err := controller.Provider.RefreshModels(RefreshModelsContext{
		Ctx: context.Background(), Credential: apiKeyCredential("local", url), Publish: publishInto(&cached), AllowNetwork: true,
	}); err != nil {
		t.Fatal(err)
	}
	if propsRequests.Load() != 1 {
		t.Fatalf("props requests = %d, want 1", propsRequests.Load())
	}
	if got := modelIDs(controller.Provider.GetModels()); !reflect.DeepEqual(got, []string{"preset"}) {
		t.Fatalf("models = %v", got)
	}
	if cached == nil || len(cached.Models) != 1 {
		t.Fatalf("cached = %+v", cached)
	}

	autoload = false
	disabled := CreateLlamaProvider()
	if err := disabled.Provider.RefreshModels(RefreshModelsContext{
		Ctx: context.Background(), Credential: apiKeyCredential("local", url), Publish: publishInto(&cached), AllowNetwork: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := disabled.Provider.GetModels(); len(got) != 0 {
		t.Fatalf("models with autoload disabled = %v", modelIDs(got))
	}
}

func TestProviderStaysDormantUntilConfiguredAndStoresURLPlusOptionalKey(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "")
	auth := CreateLlamaProvider().Provider.APIKey
	empty := AuthContext{Env: func(string) (string, bool) { return "", false }}
	ctx := context.Background()
	if result, err := auth.Check(ctx, empty, nil); result != nil || err != nil {
		t.Fatalf("Check(unconfigured) = %+v, %v", result, err)
	}
	if result, err := auth.Resolve(ctx, empty, nil); result != nil || err != nil {
		t.Fatalf("Resolve(unconfigured) = %+v, %v", result, err)
	}

	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		writeJSON(w, map[string]any{"data": []any{}})
	})
	answers := []string{url, "secret"}
	var prompts []AuthPrompt
	credential, err := auth.Login(AuthInteraction{Ctx: ctx, Prompt: func(prompt AuthPrompt) (string, error) {
		prompts = append(prompts, prompt)
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	wantPrompts := []AuthPrompt{
		{Type: "text", Message: "llama.cpp server URL"},
		{Type: "secret", Message: "API key (optional)"},
	}
	if !reflect.DeepEqual(prompts, wantPrompts) {
		t.Fatalf("prompts = %+v, want %+v (LLAMA_BASE_URL set to empty keeps an empty placeholder)", prompts, wantPrompts)
	}
	want := ai.Credential{Type: ai.CredentialAPIKey, Key: "secret", Env: map[string]string{"LLAMA_BASE_URL": url}}
	if !reflect.DeepEqual(credential, want) {
		t.Fatalf("credential = %+v, want %+v", credential, want)
	}
	result, err := auth.Resolve(ctx, empty, &credential)
	if err != nil {
		t.Fatal(err)
	}
	wantResult := &AuthResult{Auth: ModelAuth{APIKey: "secret", BaseURL: url + "/v1"}, Env: map[string]string{"LLAMA_BASE_URL": url}, Source: "stored credential"}
	if !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("resolve = %+v, want %+v", result, wantResult)
	}
}

func TestResolveFallsBackToEnvironmentServerAndKey(t *testing.T) {
	env := map[string]string{"LLAMA_BASE_URL": " http://env-host:9000/v1 ", "LLAMA_API_KEY": "env-key"}
	authContext := AuthContext{Env: func(name string) (string, bool) { value, ok := env[name]; return value, ok }}
	auth := CreateLlamaProvider().Provider.APIKey
	result, err := auth.Resolve(context.Background(), authContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &AuthResult{Auth: ModelAuth{APIKey: "env-key", BaseURL: "http://env-host:9000/v1"}, Env: map[string]string{"LLAMA_BASE_URL": "http://env-host:9000"}, Source: "LLAMA_BASE_URL"}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("resolve = %+v, want %+v", result, want)
	}
	check, err := auth.Check(context.Background(), authContext, nil)
	if err != nil || check == nil || check.Source != "LLAMA_BASE_URL" || check.Type != "api_key" {
		t.Fatalf("check = %+v, %v", check, err)
	}
	delete(env, "LLAMA_API_KEY")
	if result, _ := auth.Resolve(context.Background(), authContext, nil); result.Auth.APIKey != "local" {
		t.Fatalf("default key = %q, want local", result.Auth.APIKey)
	}
}
