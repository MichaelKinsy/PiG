package llama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports the two decision-model cases of packages/coding-agent/test/llama-extension.test.ts (v1.1.0, f6127a1b):
// "exposes decision models reported by the catalog as native System One classifiers" and "lists decision models that
// also output text for chat".

func TestProviderExposesDecisionModelsReportedByTheCatalogAsNativeSystemOneClassifiers(t *testing.T) {
	var mu sync.Mutex
	var propsModels []string
	var systemOneRequests []map[string]any
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			writeJSON(w, map[string]any{"data": []any{
				map[string]any{"id": "qwen", "status": map[string]any{"value": "loaded"}, "architecture": map[string]any{"input_modalities": []any{"text"}, "output_modalities": []any{"text"}}, "meta": map[string]any{"n_ctx": 32768}},
				map[string]any{"id": "kev", "status": map[string]any{"value": "loaded"}, "architecture": map[string]any{"input_modalities": []any{"text"}, "output_modalities": []any{"decisions"}}, "meta": map[string]any{"n_ctx": 8192}},
				map[string]any{"id": "laya", "status": map[string]any{"value": "sleeping"}, "architecture": map[string]any{"input_modalities": []any{"text"}, "output_modalities": []any{"decisions"}}},
				// Servers before llama.cpp 0.6.0 may omit architecture.
				map[string]any{"id": "legacy", "status": map[string]any{"value": "loaded"}},
			}})
		case "/props":
			mu.Lock()
			propsModels = append(propsModels, r.URL.Query().Get("model"))
			mu.Unlock()
			writeJSON(w, map[string]any{})
		case "/v1/systemone":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("systemone body %q: %v", body, err)
			}
			mu.Lock()
			systemOneRequests = append(systemOneRequests, payload)
			mu.Unlock()
			answers := map[string]any{}
			for id := range payload["questions"].(map[string]any) {
				answers[id] = map[string]any{"type": "noul", "noul": 0.82}
			}
			writeJSON(w, map[string]any{"model": payload["model"], "answers": answers, "usage": map[string]any{"input_tokens": 42, "output_tokens": 0}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	var cached *ai.ModelsStoreEntry
	credential := apiKeyCredential("local", url)
	controller := CreateLlamaProvider()
	if err := controller.Provider.RefreshModels(RefreshModelsContext{Ctx: context.Background(), Credential: credential, Publish: publishInto(&cached), AllowNetwork: true}); err != nil {
		t.Fatal(err)
	}

	// The catalog identifies decision models, so a refresh neither probes them nor reads their chat template.
	if len(systemOneRequests) != 0 {
		t.Fatalf("systemone requests = %v", systemOneRequests)
	}
	slices.Sort(propsModels)
	if !reflect.DeepEqual(propsModels, []string{"legacy", "qwen"}) {
		t.Fatalf("props models = %v", propsModels)
	}
	if got := modelIDs(controller.Provider.GetModels()); !reflect.DeepEqual(got, []string{"qwen", "legacy"}) {
		t.Fatalf("chat models = %v", got)
	}
	type stored struct{ ID, API, BaseURL string }
	var storedModels []stored
	for _, raw := range cached.Models {
		var model struct {
			ID      string `json:"id"`
			API     string `json:"api"`
			BaseURL string `json:"baseUrl"`
		}
		data, err := ai.EncodeStoredModel(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &model); err != nil {
			t.Fatal(err)
		}
		storedModels = append(storedModels, stored{model.ID, model.API, model.BaseURL})
	}
	if want := []stored{
		{"qwen", "openai-completions", url + "/v1"},
		{"legacy", "openai-completions", url + "/v1"},
		{"qwen", "llama-cpp-classify", url},
		{"kev", "typesafe-system-one", url + "/v1"},
		{"laya", "typesafe-system-one", url + "/v1"},
		{"legacy", "llama-cpp-classify", url},
	}; !reflect.DeepEqual(storedModels, want) {
		t.Fatalf("cached models = %v, want %v", storedModels, want)
	}

	var kev *ClassifierModel
	for _, model := range classifierModels(controller.Provider.GetAllModels()) {
		if model.ID == "kev" {
			kev = &model
		}
	}
	if kev == nil || kev.ContextWindow != 8192 {
		t.Fatalf("decision classifier model = %+v", kev)
	}
	result := controller.Provider.Classify(context.Background(), *kev, ai.ClassifierContext{
		State: ai.JsonObject{"message": "I was charged twice."},
		Questions: ai.ClassifierQuestions{{ID: "angry", Question: ai.ClassifierBoolQuestion{
			Instructions: "Is the customer angry?", Criteria: ai.ClassifierBoolCriteria{True: "angry", False: "calm"},
		}}},
	}, ai.ClassifierOptions{APIKey: "local"})
	if result.ErrorMessage != "" {
		t.Fatalf("error message = %q", result.ErrorMessage)
	}
	if len(result.Answers) != 1 || result.Answers[0].ID != "angry" || result.Answers[0].Answer != (ai.ClassifierBoolAnswer{Probability: 0.82}) {
		t.Fatalf("answers = %+v", result.Answers)
	}
	if result.Usage == nil || result.Usage.Input != 42 || result.Usage.Output != 0 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	wantRequest := map[string]any{
		"model":     "kev",
		"state":     map[string]any{"message": "I was charged twice."},
		"questions": map[string]any{"angry": map[string]any{"type": "noul", "instructions": "Is the customer angry?", "criteria": map[string]any{"true": "angry", "false": "calm"}}},
	}
	if !reflect.DeepEqual(systemOneRequests, []map[string]any{wantRequest}) {
		t.Fatalf("systemone requests = %v", systemOneRequests)
	}

	// A cache-only startup restores decision models with their native API.
	restored := CreateLlamaProvider()
	if err := restored.Provider.RefreshModels(RefreshModelsContext{Ctx: context.Background(), Credential: credential, Stored: cached, Publish: publishInto(&cached), AllowNetwork: false}); err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(restored.Provider.GetModels()); !reflect.DeepEqual(got, []string{"qwen", "legacy"}) {
		t.Fatalf("restored chat models = %v", got)
	}
	var restoredClassifiers [][2]string
	for _, model := range classifierModels(restored.Provider.GetAllModels()) {
		restoredClassifiers = append(restoredClassifiers, [2]string{model.ID, model.API})
	}
	if want := [][2]string{{"qwen", "llama-cpp-classify"}, {"kev", "typesafe-system-one"}, {"laya", "typesafe-system-one"}, {"legacy", "llama-cpp-classify"}}; !reflect.DeepEqual(restoredClassifiers, want) {
		t.Fatalf("restored classifiers = %v, want %v", restoredClassifiers, want)
	}
}

func TestProviderListsDecisionModelsThatAlsoOutputTextForChat(t *testing.T) {
	controller := CreateLlamaProvider()
	controller.SetCatalog([]LlamaModelInfo{
		{ID: "decide", Status: LlamaModelInfoStatus{Value: LlamaModelStatusSleeping}, Architecture: &LlamaModelArchitecture{OutputModalities: []string{"decisions"}}},
		{ID: "hybrid", Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoaded}, Architecture: &LlamaModelArchitecture{OutputModalities: []string{"text", "decisions"}}},
	}, "http://localhost:8080", false)

	if got := modelIDs(controller.Provider.GetModels()); !reflect.DeepEqual(got, []string{"hybrid"}) {
		t.Fatalf("chat models = %v", got)
	}
	var classifiers [][2]string
	for _, model := range classifierModels(controller.Provider.GetAllModels()) {
		classifiers = append(classifiers, [2]string{model.ID, model.API})
	}
	if want := [][2]string{{"decide", "typesafe-system-one"}, {"hybrid", "typesafe-system-one"}}; !reflect.DeepEqual(classifiers, want) {
		t.Fatalf("classifiers = %v, want %v", classifiers, want)
	}
}
