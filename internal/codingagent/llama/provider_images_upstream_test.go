package llama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports the case of packages/coding-agent/test/llama-extension.test.ts "sends images to decision models that advertise image input as data URLs"
// (Pi main 5799e217e, #12282), served by a mock llama-server. Pi at the pin registers every classifier as text-only (D664).

type systemOneCall struct {
	header http.Header
	body   map[string]any
}

func imageClassifierServer(t *testing.T) (string, func() []systemOneCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []systemOneCall
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			writeJSON(w, map[string]any{"data": []any{
				map[string]any{"id": "clef", "status": map[string]any{"value": "loaded"}, "architecture": map[string]any{"input_modalities": []any{"text", "image"}, "output_modalities": []any{"decisions"}}},
				// The next-token fallback cannot take images, whatever the model reads.
				map[string]any{"id": "vision-chat", "status": map[string]any{"value": "loaded"}, "architecture": map[string]any{"input_modalities": []any{"text", "image"}, "output_modalities": []any{"text"}}},
				map[string]any{"id": "kev", "status": map[string]any{"value": "loaded"}, "architecture": map[string]any{"input_modalities": []any{"text"}, "output_modalities": []any{"decisions"}}},
			}})
		case "/props":
			writeJSON(w, map[string]any{})
		case "/v1/systemone":
			data, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Errorf("systemone body %q: %v", data, err)
			}
			mu.Lock()
			calls = append(calls, systemOneCall{header: r.Header.Clone(), body: payload})
			mu.Unlock()
			writeJSON(w, map[string]any{"answers": map[string]any{"screenshot": map[string]any{"type": "noul", "noul": 0.9}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return url, func() []systemOneCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]systemOneCall(nil), calls...)
	}
}

func screenshotContext(images ...ai.ImageContent) ai.ClassifierContext {
	return ai.ClassifierContext{
		State:  ai.JsonObject{"task": "Check the screenshot."},
		Images: images,
		Questions: ai.ClassifierQuestions{{ID: "screenshot", Question: ai.ClassifierBoolQuestion{
			Instructions: "Is this a screenshot?", Criteria: ai.ClassifierBoolCriteria{},
		}}},
	}
}

var screenshotPNG = ai.ImageContent{MimeType: "image/png", Data: "iVBORw0KGgo="}

func TestProviderSendsImagesToDecisionModelsThatAdvertiseImageInputAsDataURLs(t *testing.T) {
	url, requests := imageClassifierServer(t)
	var cached *ai.ModelsStoreEntry
	controller := CreateLlamaProvider()
	if err := controller.Provider.RefreshModels(RefreshModelsContext{Ctx: context.Background(), Credential: apiKeyCredential("local", url), Publish: publishInto(&cached), AllowNetwork: true}); err != nil {
		t.Fatal(err)
	}
	inputs := map[string][]string{}
	for _, model := range classifierModels(controller.Provider.GetAllModels()) {
		inputs[model.ID] = model.Input
	}
	want := map[string][]string{"clef": {"text", "image"}, "vision-chat": {"text"}, "kev": {"text"}}
	if !reflect.DeepEqual(inputs, want) {
		t.Fatalf("classifier inputs = %v, want %v", inputs, want)
	}

	// The image model and its compat survive the models store, so a cache-only start still sends the images.
	restored := CreateLlamaProvider()
	if err := restored.Provider.RefreshModels(RefreshModelsContext{Ctx: context.Background(), Credential: apiKeyCredential("local", url), Stored: cached, Publish: publishInto(&cached), AllowNetwork: false}); err != nil {
		t.Fatal(err)
	}
	for name, provider := range map[string]*LlamaProviderController{"fresh": controller, "restored": restored} {
		var clef *ClassifierModel
		for _, model := range classifierModels(provider.Provider.GetAllModels()) {
			if model.ID == "clef" {
				clef = &model
			}
		}
		if clef == nil {
			t.Fatalf("%s: no clef classifier", name)
		}
		before := len(requests())
		result := provider.Provider.Classify(context.Background(), *clef, screenshotContext(screenshotPNG), ai.ClassifierOptions{APIKey: "local"})
		if result.ErrorMessage != "" {
			t.Fatalf("%s: error message = %q", name, result.ErrorMessage)
		}
		if len(result.Answers) != 1 || result.Answers[0].Answer != (ai.ClassifierBoolAnswer{Probability: 0.9}) {
			t.Fatalf("%s: answers = %+v", name, result.Answers)
		}
		calls := requests()
		if len(calls) != before+1 {
			t.Fatalf("%s: systemone requests = %d, want %d", name, len(calls), before+1)
		}
		call := calls[before]
		if got := call.header.Get("TypeSafe-Danger-Zone-Images"); got != "" {
			t.Fatalf("%s: opt-in header = %q, want none", name, got)
		}
		if _, has := call.body["assets"]; has {
			t.Fatalf("%s: body has assets: %v", name, call.body)
		}
		if call.body["model"] != "clef" || !reflect.DeepEqual(call.body["images"], []any{"data:image/png;base64,iVBORw0KGgo="}) ||
			!reflect.DeepEqual(call.body["state"], map[string]any{"task": "Check the screenshot."}) {
			t.Fatalf("%s: body = %v", name, call.body)
		}
	}
}

func TestProviderRefusesImagesForClassifiersThatTakeNone(t *testing.T) {
	url, requests := imageClassifierServer(t)
	controller := CreateLlamaProvider()
	var cached *ai.ModelsStoreEntry
	if err := controller.Provider.RefreshModels(RefreshModelsContext{Ctx: context.Background(), Credential: apiKeyCredential("local", url), Publish: publishInto(&cached), AllowNetwork: true}); err != nil {
		t.Fatal(err)
	}
	for _, model := range classifierModels(controller.Provider.GetAllModels()) {
		if model.ID == "clef" {
			continue
		}
		result := controller.Provider.Classify(context.Background(), model, screenshotContext(screenshotPNG), ai.ClassifierOptions{APIKey: "local"})
		if !strings.Contains(result.ErrorMessage, "does not support image input") {
			t.Fatalf("%s (%s): error message = %q", model.ID, model.API, result.ErrorMessage)
		}
	}
	if calls := requests(); len(calls) != 0 {
		t.Fatalf("systemone requests = %v, want none", calls)
	}
}
