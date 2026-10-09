package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The Pi 0.99.1 sources define these behaviors (packages/ai/src/models.ts createProvider currentModels,
// Models.getAllAvailable) but its tests do not exercise them.

// A dynamic overlay replaces the baseline entry of the same type and id only: ids are unique per model type.
func TestCreateProviderOverlayReplacesOnlyTheSameTypeAndID(t *testing.T) {
	overlay := typedChatModel("p", "shared")
	overlay.DisplayName = "overlay"
	provider := CreateProvider(CreateProviderOptions{ID: "p", Auth: typedNoAuth(), API: typedChatStreams(),
		Models:      []AnyModel{typedImageModel("p", "shared"), typedChatModel("p", "shared")},
		FetchModels: func(RefreshModelsContext) ([]AnyModel, error) { return []AnyModel{overlay}, nil }})
	models := CreateModels()
	models.SetProvider(provider)
	models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"p"}})
	all := models.GetAllModels("p")
	if len(all) != 2 || GetModelType(all[0]) != ModelTypeImage || all[1] != AnyModel(overlay) {
		t.Fatalf("models=%v", all)
	}
}

// Without FilterAllModels, FilterModels narrows chat models and keeps every other model; FilterAllModels replaces that policy.
func TestGetAllAvailableAppliesTheProviderFilters(t *testing.T) {
	list := []AnyModel{typedChatModel("p", "keep"), typedChatModel("p", "drop"), typedImageModel("p", "image")}
	auth := ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}
	filterChat := func(models []*Model, _ *Credential) []*Model {
		out := []*Model{}
		for _, model := range models {
			if model.ID == "keep" {
				out = append(out, model)
			}
		}
		return out
	}
	ids := func(t *testing.T, options CreateProviderOptions) []string {
		t.Helper()
		options.ID, options.Auth, options.Models, options.API = "p", auth, list, typedChatStreams()
		models := CreateModels()
		models.SetProvider(CreateProvider(options))
		available, err := models.GetAllAvailable(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return modelIDs(available)
	}
	if got := ids(t, CreateProviderOptions{}); !reflect.DeepEqual(got, []string{"keep", "drop", "image"}) {
		t.Fatalf("no filter=%v", got)
	}
	if got := ids(t, CreateProviderOptions{FilterModels: filterChat}); !reflect.DeepEqual(got, []string{"keep", "image"}) {
		t.Fatalf("chat filter=%v", got)
	}
	onlyImages := func(models []AnyModel, _ *Credential) []AnyModel {
		out := []AnyModel{}
		for _, model := range models {
			if IsModelType(model, ModelTypeImage) {
				out = append(out, model)
			}
		}
		return out
	}
	if got := ids(t, CreateProviderOptions{FilterModels: filterChat, FilterAllModels: onlyImages}); !reflect.DeepEqual(got, []string{"image"}) {
		t.Fatalf("all filter=%v", got)
	}
}

// The models a provider persists keep their type and every BaseModel field across a store round trip, and the stored
// records carry Pi's required fields (packages/ai/src/types.ts BaseModel, ImageModel, ClassifierModel).
func TestModelsCatalogCodecRoundTripsEveryModelType(t *testing.T) {
	chat := typedChatModel("p", "c")
	chat.Type = ModelTypeChat
	image := typedImageModel("p", "i")
	image.Output, image.Headers = []string{"image", "text"}, map[string]string{"x-image": "1"}
	image.InputLimits = &ModelInputLimits{MaxRequestBytes: 1024}
	image.Cost = ModelCost{Input: 1, Output: 2, Tiers: []CostTier{{InputTokensAbove: 1000, InputCostPer1M: 3, OutputCostPer1M: 4}}}
	classifier := &ClassifierModel{ID: "k", Name: "k", API: "test-classifier", Provider: "p", BaseURL: "https://example.test", Input: []string{"text"}, InputLimits: &ModelInputLimits{MaxRequestBytes: 2048}, Cost: ModelCost{Input: 0.5}}
	var raw []json.RawMessage
	for _, model := range []AnyModel{chat, image, classifier} {
		record, err := EncodeStoredModel(model)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, record)
	}
	decoded, err := DecodeStoredModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range decoded {
		// The decoded record keeps the persisted fields (a catalogShape); the typed members are what must match the originals.
		switch typed := model.(type) {
		case *Model:
			typed.catalog = nil
		case *ImageModel:
			typed.catalog = nil
		case *ClassifierModel:
			typed.catalog = nil
		}
	}
	if len(decoded) != 3 || GetModelType(decoded[0]) != ModelTypeChat || decoded[0].(*Model).Type != ModelTypeChat || !reflect.DeepEqual(decoded[1], AnyModel(image)) || !reflect.DeepEqual(decoded[2], AnyModel(classifier)) {
		t.Fatalf("decoded=%#v", decoded)
	}
	want := `{"type":"classifier","id":"k","name":"k","api":"test-classifier","provider":"p","baseUrl":"https://example.test","input":["text"],"inputLimits":{"maxRequestBytes":2048},"cost":{"input":0.5,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":0}`
	if string(raw[2]) != want {
		t.Fatalf("classifier record=%s", raw[2])
	}
	bare, err := EncodeStoredModel(typedImageModel("p", "bare"))
	if err != nil || !strings.Contains(string(bare), `"output":["image"]`) {
		t.Fatalf("image record=%s err=%v", bare, err)
	}
}

// getAuth(model) merges the model's static headers for every model type (packages/ai/src/models.ts getAuth).
func TestGetModelAuthMergesStaticHeadersOfEveryModelType(t *testing.T) {
	auth := ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
		return &AuthResult{Auth: ModelAuth{Headers: ProviderHeaders{"X-Provider": new("p")}}}, nil
	}}}
	image := typedImageModel("p", "i")
	image.Headers = map[string]string{"X-Model": "image"}
	classifier := &ClassifierModel{ID: "k", Provider: "p", API: "test-classifier", Headers: map[string]string{"X-Model": "classifier"}}
	models := CreateModels()
	models.SetProvider(CreateProvider(CreateProviderOptions{ID: "p", Auth: auth, API: typedChatStreams(), Models: []AnyModel{image, classifier}}))
	for _, model := range []AnyModel{image, classifier} {
		result, err := models.GetModelAuth(t.Context(), model)
		if err != nil || result == nil {
			t.Fatalf("%T: result=%v error=%v", model, result, err)
		}
		headers := result.Auth.Headers
		if headers["X-Provider"] == nil || *headers["X-Provider"] != "p" || headers["X-Model"] == nil || *headers["X-Model"] != anyModelHeaders(model)["X-Model"] {
			t.Fatalf("%T headers=%v", model, headers)
		}
	}
}

// Models.getAuth(model) merges model.headers for every model type, so image requests carry the image model's headers
// (packages/ai/src/models.ts getAuth and applyAuth).
func TestGenerateImagesMergesTheImageModelHeaders(t *testing.T) {
	calls := []typedGenerateCall{}
	model := typedImageModel("p1", "model-a")
	model.Headers = map[string]string{"x-model": "model", "x-shared": "model"}
	models := CreateModels()
	models.SetProvider(CreateProvider(CreateProviderOptions{ID: "p1", Models: []AnyModel{model}, Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
		return &AuthResult{Auth: ModelAuth{APIKey: "key", Headers: ProviderHeaders{"x-auth": new("auth"), "x-shared": new("auth")}}}, nil
	}}}, Images: ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ImageModel, _ ImagesContext, options ImagesOptions) (AssistantImages, error) {
		calls = append(calls, typedGenerateCall{model, options})
		return typedOKResult(model), nil
	}}}}))
	auth, err := models.GetModelAuth(t.Context(), model)
	if err != nil || auth == nil || !reflect.DeepEqual(auth.Auth.Headers, ProviderHeaders{"x-auth": new("auth"), "x-model": new("model"), "x-shared": new("model")}) {
		t.Fatalf("auth=%v err=%v", auth, err)
	}
	result := models.GenerateImages(t.Context(), model, ImagesContext{}, ModelsImagesOptions{ImagesOptions: ImagesOptions{Headers: ProviderHeaders{"x-request": new("request")}}})
	if result.StopReason != ImagesStopReasonStop || len(calls) != 1 || !reflect.DeepEqual(calls[0].options.Headers, ProviderHeaders{"x-auth": new("auth"), "x-model": new("model"), "x-request": new("request"), "x-shared": new("model")}) {
		t.Fatalf("result=%v calls=%v", result, calls)
	}
}

// An api value without any stream function is not a concrete implementation: Pi treats it as an empty api map and
// throws (packages/ai/src/models.ts createProvider; images-models.test.ts `api: {}`).
func TestCreateProviderRejectsAnAPIWithoutStreamFunctions(t *testing.T) {
	create := func(api ProviderAPIs) (recovered any) {
		defer func() { recovered = recover() }()
		CreateProvider(CreateProviderOptions{ID: "empty", Auth: typedNoAuth(), Models: []AnyModel{}, API: api})
		return nil
	}
	if got := create(&ProviderStreams{}); got == nil || !strings.Contains(fmt.Sprint(got), `at least one of "api", "images", or "classifiers"`) {
		t.Fatalf("empty streams: panic=%v", got)
	}
	// Pi counts map entries, not their contents: {"test-chat": {}} is accepted.
	if got := create(ProviderAPIMap{"test-chat": &ProviderStreams{}}); got != nil {
		t.Fatalf("map entry: panic=%v", got)
	}
	if got := create(typedChatStreams()); got != nil {
		t.Fatalf("chat streams: panic=%v", got)
	}
}

// hasApi narrows only chat models (packages/ai/src/models.ts hasApi: isModelType(model, "chat") && model.api === api).
func TestHasApiRejectsAModelWhoseTypeIsNotChat(t *testing.T) {
	model := typedChatModel("p", "m")
	if !HasApi(model, "test-chat") {
		t.Fatal("chat model rejected")
	}
	model.Type = ModelTypeImage
	if HasApi(model, "test-chat") {
		t.Fatal("non-chat model accepted")
	}
}
