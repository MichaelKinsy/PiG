package coding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// What an extension's ctx.modelRegistry reaches for the typed model operations of Pi 0.99.1
// (.upstream/v0.99.1/packages/coding-agent/src/core/model-registry.ts:71-79,145-177): the state its synchronous reads answer
// from lists the image and classifier models, getAvailableOfType and classify are host actions, and a registered wire provider
// config keeps its typed model entries and implementations (types.ts:1896-1898, 1929-1991).

type typedWireBridge struct{ actions map[string]any }

func (probe *typedWireBridge) SetHostAction(key string, action any) { probe.actions[key] = action }
func (*typedWireBridge) PublishModelCatalog()                       {}

func wireTypedModelOperations(t *testing.T, services *AgentSessionServices) *typedWireBridge {
	t.Helper()
	probe := &typedWireBridge{actions: make(map[string]any)}
	runtime := services.ModelRuntime()
	detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{
		ModelLookup: runtime.GetModel, ModelCatalog: func(...string) []*ai.Model { return runtime.GetModels() }, Registry: services.Registry().ModelRegistry, Classify: runtime.Classify, GenerateImages: runtime.GenerateImages,
	})
	t.Cleanup(detach)
	return probe
}

func typedWireState(t *testing.T, probe *typedWireBridge) map[string]any {
	t.Helper()
	state := probe.actions["getModelRegistryState"].(func() map[string]any)()
	// The bridge encodes the state as JSON; read it back as an extension does.
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func typedWireModels(state map[string]any, key string) []map[string]any {
	var models []map[string]any
	list, _ := state[key].([]any)
	for _, entry := range list {
		models = append(models, entry.(map[string]any))
	}
	return models
}

func typedWireFind(models []map[string]any, provider, id string) map[string]any {
	for _, model := range models {
		if model["provider"] == provider && model["id"] == id {
			return model
		}
	}
	return nil
}

// model-registry.ts:145-161: the typed accessors read every model type. The wire state keeps chat models under "models" (getAll is chat only, :51-53) and lists every other model under "typedModels", so an extension's synchronous getModelsOfType answers from state.
func TestExtensionRegistryStateListsTypedModels(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	if err := services.ModelRuntime().RegisterProvider("extension-operations", ProviderConfigInput{
		APIKey: "extension-secret",
		Models: []ai.AnyModel{imagesTestImageModel("ignored", "shared-image"), imagesTestClassifierModel("ignored", "shared-classifier")},
		Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, _ ai.ImagesOptions) (ai.AssistantImages, error) {
			return imagesTestOKResult(model), nil
		}}},
		Classifiers: ai.ProviderClassifierMap{"test-classifier": {Classify: func(_ context.Context, model *ai.ClassifierModel, _ ai.ClassifierContext, _ ai.ClassifierOptions) (ai.ClassifierResult, error) {
			return classifiersTestOKResult(model), nil
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	state := typedWireState(t, wireTypedModelOperations(t, services))
	typed := typedWireModels(state, "typedModels")

	jev := typedWireFind(typed, "typesafe", "jev-latest")
	if jev == nil || jev["type"] != "classifier" || jev["api"] != "typesafe-system-one" || jev["contextWindow"] != float64(64000) || jev["baseUrl"] != "https://api.typesafe.ai/v1/" {
		t.Fatalf("typesafe classifier = %v", jev)
	}
	if want := []any{"text"}; !reflect.DeepEqual(jev["input"], want) {
		t.Fatalf("classifier input = %v", jev["input"])
	}
	flux := typedWireFind(typed, "openrouter", "google/gemini-3-pro-image")
	if flux == nil || flux["type"] != "image" || !slices.Contains(flux["output"].([]any), any("image")) {
		t.Fatalf("openrouter image model = %v", flux)
	}
	// Extension models keep their entry's own values; the provider is the registration's.
	image := typedWireFind(typed, "extension-operations", "shared-image")
	classifier := typedWireFind(typed, "extension-operations", "shared-classifier")
	if image == nil || image["type"] != "image" || image["api"] != "test-images" || classifier == nil || classifier["type"] != "classifier" || classifier["api"] != "test-classifier" || classifier["contextWindow"] != float64(1000) {
		t.Fatalf("extension typed models = %v / %v", image, classifier)
	}
	for _, model := range typed {
		if model["type"] == "chat" || model["type"] == nil {
			t.Fatalf("typedModels holds a chat model: %v", model)
		}
	}
	// The chat list carries no typed model, and openrouter's chat model of the same id stays a chat model.
	chat := typedWireModels(state, "models")
	if typedWireFind(chat, "typesafe", "jev-latest") != nil || typedWireFind(chat, "extension-operations", "shared-image") != nil {
		t.Fatal("models lists a non-chat model")
	}
	if model := typedWireFind(chat, "openrouter", "google/gemini-3-pro-image"); model == nil || model["api"] != "openai-completions" {
		t.Fatalf("openrouter chat model = %v", model)
	}
	// A provider with only classifier models has provider state, so hasConfiguredAuth and availability read from it.
	providers, _ := state["providers"].(map[string]any)
	if _, ok := providers["typesafe"].(map[string]any); !ok {
		t.Fatalf("providers has no typesafe entry: %v", providers)
	}
}

// model-registry.ts:135-143 (getAvailableOfType) through model-runtime.ts: a typed model is available when its provider has working credentials.
func TestExtensionGetAvailableOfTypeAction(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	probe := wireTypedModelOperations(t, services)
	available := probe.actions["getAvailableOfType"].(func(context.Context, string, string) ([]map[string]any, error))

	models, err := available(t.Context(), "classifier", "typesafe")
	if err != nil || len(models) != 0 {
		t.Fatalf("unconfigured = %v, %v", models, err)
	}
	services.Registry().SetRuntimeAPIKey("typesafe", "sk-typesafe")
	models, err = available(t.Context(), "classifier", "typesafe")
	if err != nil || len(models) != 1 || models[0]["id"] != "jev-latest" || models[0]["type"] != "classifier" || models[0]["provider"] != "typesafe" {
		t.Fatalf("configured = %v, %v", models, err)
	}
	// Every provider when none is named; a chat model is a chat model of its provider.
	all, err := available(t.Context(), "classifier", "")
	if err != nil || len(all) != 1 {
		t.Fatalf("all providers = %v, %v", all, err)
	}
	chat, err := available(t.Context(), "chat", "typesafe")
	if err != nil || len(chat) != 0 {
		t.Fatalf("typesafe has no chat models: %v, %v", chat, err)
	}
	// Models.getAvailableOfType filters by type without validating it, so a type no model has lists nothing (packages/ai/src/models.ts:708-716).
	if unknown, err := available(t.Context(), "audio", ""); err != nil || unknown == nil || len(unknown) != 0 {
		t.Fatalf("an unknown model type lists nothing: %v, %v", unknown, err)
	}
}

// model-runtime.ts:800-815 through model-registry.ts:170-177: classify resolves auth at request time, sends the caller's model object, and never rejects.
func TestExtensionClassifyAction(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	probe := wireTypedModelOperations(t, services)
	rawClassify := probe.actions["classify"].(func(context.Context, map[string]any, json.RawMessage, map[string]any) (json.RawMessage, error))
	classify := func(ctx context.Context, model, request, options map[string]any) (map[string]any, error) {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := rawClassify(ctx, model, encoded, options)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("classify result %s: %v", raw, err)
		}
		return result, nil
	}
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"answers":{"approved":{"type":"noul","noul":0.8}}}`)
	}))
	defer server.Close()
	model := map[string]any{"type": "classifier", "id": "jev-latest", "name": "Jev", "api": "typesafe-system-one", "provider": "typesafe", "baseUrl": server.URL + "/v1/", "input": []any{"text"}, "contextWindow": 64000, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
	request := map[string]any{"state": map[string]any{"text": "Looks good"}, "questions": map[string]any{"approved": map[string]any{"type": "bool", "instructions": "Does this express approval?", "criteria": map[string]any{"true": "Approval", "false": "No approval"}}}}

	unconfigured, err := classify(t.Context(), model, request, nil)
	if err != nil || unconfigured["stopReason"] != "error" || !strings.Contains(unconfigured["errorMessage"].(string), "not configured") || unconfigured["provider"] != "typesafe" || unconfigured["model"] != "jev-latest" {
		t.Fatalf("unconfigured = %v, %v", unconfigured, err)
	}
	services.Registry().SetRuntimeAPIKey("typesafe", "sk-typesafe")
	result, err := classify(t.Context(), model, request, nil)
	if err != nil || result["stopReason"] != "stop" || authorization != "Bearer sk-typesafe" {
		t.Fatalf("result = %v, %v (authorization %q)", result, err, authorization)
	}
	if want := map[string]any{"approved": map[string]any{"type": "bool", "probability": 0.8}}; !reflect.DeepEqual(result["answers"], want) {
		t.Fatalf("answers = %v", result["answers"])
	}
	// A caller's explicit key replaces the runtime's, as Models.classify applies its options over resolved auth.
	if result, err = classify(t.Context(), model, request, map[string]any{"apiKey": "sk-explicit"}); err != nil || result["stopReason"] != "stop" || authorization != "Bearer sk-explicit" {
		t.Fatalf("explicit key: %v, %v (authorization %q)", result, err, authorization)
	}
	// A chat model is not a classifier model: an error result, not an error (model-operations.ts assertClassifierModel).
	chat := map[string]any{"id": "gpt", "name": "GPT", "api": "openai-completions", "provider": "openai"}
	rejected, err := classify(t.Context(), chat, request, nil)
	if err != nil || rejected["stopReason"] != "error" || !strings.Contains(rejected["errorMessage"].(string), "is not a classifier model") {
		t.Fatalf("chat model = %v, %v", rejected, err)
	}
	// A cancelled request is an aborted result.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	aborted, err := classify(cancelled, model, request, nil)
	if err != nil || aborted["stopReason"] != "aborted" {
		t.Fatalf("cancelled = %v, %v", aborted, err)
	}
}

// model-runtime.ts:783-798 through model-registry.ts:181-188 (Pi 1.0.0): generateImages resolves auth at request time, sends the caller's model object, and never rejects.
func TestExtensionGenerateImagesAction(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	probe := wireTypedModelOperations(t, services)
	rawGenerate, ok := probe.actions["generateImages"].(func(context.Context, map[string]any, json.RawMessage, map[string]any) (json.RawMessage, error))
	if !ok {
		t.Fatalf("generateImages action = %T", probe.actions["generateImages"])
	}
	generate := func(ctx context.Context, model, request, options map[string]any) (map[string]any, error) {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := rawGenerate(ctx, model, encoded, options)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("generateImages result %s: %v", raw, err)
		}
		return result, nil
	}
	var authorization, prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		prompt = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"img-1","choices":[{"message":{"content":"done","images":[{"image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}}]}`)
	}))
	defer server.Close()
	model := map[string]any{"type": "image", "id": "google/gemini-2.5-flash-image", "name": "Painter", "api": "openrouter-images", "provider": "openrouter", "baseUrl": server.URL, "input": []any{"text", "image"}, "output": []any{"image", "text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
	request := map[string]any{"input": []any{map[string]any{"type": "text", "text": "a red circle"}}}

	unconfigured, err := generate(t.Context(), model, request, nil)
	if err != nil || unconfigured["stopReason"] != "error" || !strings.Contains(unconfigured["errorMessage"].(string), "not configured") || unconfigured["provider"] != "openrouter" || unconfigured["model"] != "google/gemini-2.5-flash-image" || !reflect.DeepEqual(unconfigured["output"], []any{}) {
		t.Fatalf("unconfigured = %v, %v", unconfigured, err)
	}
	services.Registry().SetRuntimeAPIKey("openrouter", "sk-openrouter")
	result, err := generate(t.Context(), model, request, nil)
	if err != nil || result["stopReason"] != "stop" || authorization != "Bearer sk-openrouter" || !strings.Contains(prompt, "a red circle") {
		t.Fatalf("result = %v, %v (authorization %q, request %s)", result, err, authorization, prompt)
	}
	output, _ := result["output"].([]any)
	var images int
	for _, block := range output {
		if block.(map[string]any)["type"] == "image" && block.(map[string]any)["data"] == "aGVsbG8=" {
			images++
		}
	}
	if images != 1 {
		t.Fatalf("output = %v", result["output"])
	}
	// A caller's explicit key replaces the runtime's, as Models.generateImages applies its options over resolved auth.
	if result, err = generate(t.Context(), model, request, map[string]any{"apiKey": "sk-explicit"}); err != nil || result["stopReason"] != "stop" || authorization != "Bearer sk-explicit" {
		t.Fatalf("explicit key: %v, %v (authorization %q)", result, err, authorization)
	}
	// A classifier model is not an image model: an error result, not an error (model-operations.ts assertImageModel).
	classifier := map[string]any{"type": "classifier", "id": "jev-latest", "name": "Jev", "api": "typesafe-system-one", "provider": "typesafe"}
	rejected, err := generate(t.Context(), classifier, request, nil)
	if err != nil || rejected["stopReason"] != "error" || rejected["errorMessage"] != "Model typesafe/jev-latest is not an image model" {
		t.Fatalf("classifier model = %v, %v", rejected, err)
	}
	// A cancelled request is an aborted result.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	aborted, err := generate(cancelled, model, request, nil)
	if err != nil || aborted["stopReason"] != "aborted" {
		t.Fatalf("cancelled = %v, %v", aborted, err)
	}
}

// provider-composer.ts:97-106,275: a wire provider config (what a subprocess extension registers) keeps its typed model entries and the implementations the host attached to it.
func TestExtensionProviderConfigKeepsTypedModelsAndImplementations(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	var observed []string
	config := extension.ProviderConfig{
		BaseURL: "https://ops.test/v1", APIKey: "wire-secret",
		Models: []extension.ProviderModelConfig{
			{ID: "chat-1", Name: "Chat", API: "openai-completions", Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100},
			{ID: "shared", Name: "Image", Type: ai.ModelTypeImage, API: "test-images", Input: []string{"text"}, Output: []string{"image", "text"}, Headers: map[string]string{"X-Operation": "image"}},
			{ID: "shared", Name: "Classifier", Type: ai.ModelTypeClassifier, API: "test-classifier", Input: []string{"text"}, ContextWindow: 8192, Headers: map[string]string{"X-Operation": "classifier"}},
		},
		Images: map[ai.ImageAPI]*ai.ProviderImages{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
			observed = append(observed, "image:"+options.APIKey+":"+model.BaseURL+":"+headerValue(options.Headers, "X-Operation"))
			return imagesTestOKResult(model), nil
		}}},
		Classifiers: map[ai.ClassifierAPI]*ai.ProviderClassifier{"test-classifier": {Classify: func(_ context.Context, model *ai.ClassifierModel, _ ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
			observed = append(observed, "classifier:"+options.APIKey+":"+model.BaseURL+":"+headerValue(options.Headers, "X-Operation"))
			return classifiersTestOKResult(model), nil
		}}},
	}
	if err := services.Registry().RegisterExtensionProvider("wire-operations", config); err != nil {
		t.Fatal(err)
	}
	if got := runtime.GetModel("wire-operations", "chat-1"); got == nil || got.ProviderMeta.API != ai.APIOpenAICompletions {
		t.Fatalf("chat model = %+v", got)
	}
	if got := runtime.GetModel("wire-operations", "shared"); got != nil {
		t.Fatalf("a typed entry became a chat model: %+v", got)
	}
	image, _ := runtime.GetModelOfType(ai.ModelTypeImage, "wire-operations", "shared").(*ai.ImageModel)
	classifier, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, "wire-operations", "shared").(*ai.ClassifierModel)
	if image == nil || classifier == nil || !slices.Equal(image.Output, []string{"image", "text"}) || classifier.ContextWindow != 8192 || image.BaseURL != "https://ops.test/v1" {
		t.Fatalf("image = %+v, classifier = %+v", image, classifier)
	}
	if result := runtime.GenerateImages(t.Context(), image, imagesTestContext); result.StopReason != ai.ImagesStopReasonStop {
		t.Fatalf("image result = %+v", result)
	}
	if result := runtime.Classify(t.Context(), classifier, classifiersTestContext); result.StopReason != ai.ClassifierStopReasonStop {
		t.Fatalf("classifier result = %+v", result)
	}
	// The request carries the headers of the extension definition of the same type and id (provider-composer.ts rawModelHeaders, resolveConfiguredModelHeaders), not the other operation's.
	if want := []string{"image:wire-secret:https://ops.test/v1:image", "classifier:wire-secret:https://ops.test/v1:classifier"}; !reflect.DeepEqual(observed, want) {
		t.Fatalf("observed = %v, want %v", observed, want)
	}
}

func headerValue(headers ai.ProviderHeaders, name string) string {
	if value := headers[name]; value != nil {
		return *value
	}
	return "<none>"
}
