package coding

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports .upstream/v0.99.1/packages/coding-agent/test/model-runtime-images.test.ts.
// The cases that classify (lines 129-172 and the typesafe assertion at line 320) read
// ai.ProviderClassifier.Classify, which family 3 defines; they live in
// model_runtime_classifiers_upstream_test.go once that lands.

func imagesTestImageModel(provider, id string) *ai.ImageModel {
	return &ai.ImageModel{ID: id, Name: id, API: "test-images", Provider: provider, BaseURL: "https://images.test/v1", Input: []string{"text"}, Output: []string{"image"}}
}

func imagesTestClassifierModel(provider, id string) *ai.ClassifierModel {
	return &ai.ClassifierModel{ID: id, Name: id, API: "test-classifier", Provider: provider, BaseURL: "https://classifier.test/v1", Input: []string{"text"}, ContextWindow: 1000}
}

var imagesTestContext = ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "a red circle"}}}

func imagesTestOKResult(model *ai.ImageModel) ai.AssistantImages {
	return ai.AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, Output: []ai.ContentBlock{ai.ImageContent{Data: "aGk=", MimeType: "image/png"}}, StopReason: ai.ImagesStopReasonStop}
}

func imagesTestKeyAuth(name string) ai.ProviderAuth {
	return ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: name, Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		if input.Credential != nil && input.Credential.Key != "" {
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Source: "stored"}, nil
		}
		return nil, nil
	}}}
}

func TestModelRuntimeImagesUpstream(t *testing.T) {
	// model-runtime-images.test.ts:82
	t.Run("lists built-in OpenRouter image models separately from chat models", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		images := runtime.GetModelsOfType(ai.ModelTypeImage, "openrouter")
		if len(images) == 0 {
			t.Fatal("no built-in openrouter image models")
		}
		chat := 0
		for _, model := range runtime.GetModels() {
			if model.ProviderMeta.ProviderID == "openrouter" {
				chat++
				if !ai.IsModelType(model, ai.ModelTypeChat) {
					t.Fatalf("chat list holds %s of type %s", model.ID, model.ModelType())
				}
			}
		}
		if got := runtime.GetModelOfType(ai.ModelTypeImage, "openrouter", images[0].ModelID()); got != images[0] {
			t.Fatalf("GetModelOfType did not return the listed model: %v", got)
		}
		if model := runtime.GetModel("openrouter", "google/gemini-3-pro-image"); model == nil || model.ProviderMeta.API != ai.APIOpenAICompletions {
			t.Fatalf("chat model = %+v", model)
		}
		if got := runtime.GetModelOfType(ai.ModelTypeImage, "openrouter", "google/gemini-3-pro-image"); got == nil || got.ModelType() != ai.ModelTypeImage {
			t.Fatalf("image model = %v", got)
		}
		classifiers := runtime.GetModelsOfType(ai.ModelTypeClassifier, "openrouter")
		if got, want := len(runtime.GetAllModels("openrouter")), chat+len(images)+len(classifiers); got != want {
			t.Fatalf("GetAllModels = %d, want %d", got, want)
		}
	})
	// model-runtime-images.test.ts:96
	t.Run("extension model lists replace undeclared models of every operation", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		chat := &ai.Model{ID: "built-in-chat", DisplayName: "Built-in chat", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "mixed", API: "test-chat", BaseURL: "https://built-in.test/v1"}, Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
		provider := ai.CreateProvider(ai.CreateProviderOptions{
			ID:     "mixed",
			Auth:   ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Mixed key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}},
			Models: []ai.AnyModel{chat, imagesTestImageModel("mixed", "built-in-image"), imagesTestClassifierModel("mixed", "built-in-classifier")},
			Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, _ ai.ImagesOptions) (ai.AssistantImages, error) {
				return imagesTestOKResult(model), nil
			}}},
		})
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		extensionChat := new(*chat)
		extensionChat.ID, extensionChat.DisplayName, extensionChat.ProviderMeta.BaseURL = "extension-chat", "Extension chat", "https://chat-proxy.test/v1"
		if err := runtime.RegisterProvider("mixed", ProviderConfigInput{APIKey: "extension-secret", Models: []ai.AnyModel{extensionChat}}); err != nil {
			t.Fatal(err)
		}
		var got [][2]string
		for _, model := range runtime.GetAllModels("mixed") {
			got = append(got, [2]string{string(model.ModelType()), model.ModelID()})
		}
		if want := [][2]string{{"chat", "extension-chat"}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("GetAllModels = %v, want %v", got, want)
		}
		if model := runtime.GetModel("mixed", "extension-chat"); model == nil || model.ProviderMeta.BaseURL != "https://chat-proxy.test/v1" {
			t.Fatalf("model = %+v", model)
		}
		if got := runtime.GetModelsOfType(ai.ModelTypeImage, "mixed"); len(got) != 0 {
			t.Fatalf("image models = %v", got)
		}
		if got := runtime.GetModelsOfType(ai.ModelTypeClassifier, "mixed"); len(got) != 0 {
			t.Fatalf("classifier models = %v", got)
		}
	})
	// model-runtime-images.test.ts:172 (the image half; the classifier half needs family 3's Classify)
	t.Run("generates images through a native provider with runtime-resolved auth", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		var calls []ai.ImagesOptions
		if err := runtime.RegisterNativeProvider(ai.CreateProvider(ai.CreateProviderOptions{
			ID:     "pixels",
			Auth:   imagesTestKeyAuth("Pixels key"),
			Models: []ai.AnyModel{imagesTestImageModel("pixels", "flux")},
			Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
				calls = append(calls, options)
				return imagesTestOKResult(model), nil
			}}},
		})); err != nil {
			t.Fatal(err)
		}
		model, _ := runtime.GetModelOfType(ai.ModelTypeImage, "pixels", "flux").(*ai.ImageModel)
		if model == nil {
			t.Fatal("missing image model")
		}
		unconfigured := runtime.GenerateImages(t.Context(), model, imagesTestContext)
		if unconfigured.StopReason != ai.ImagesStopReasonError || !strings.Contains(unconfigured.ErrorMessage, "not configured") || len(calls) != 0 {
			t.Fatalf("unconfigured = %+v, calls = %d", unconfigured, len(calls))
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		aborted := runtime.GenerateImages(cancelled, model, imagesTestContext)
		if aborted.StopReason != ai.ImagesStopReasonAborted || len(calls) != 0 {
			t.Fatalf("aborted = %+v, calls = %d", aborted, len(calls))
		}
		services.Registry().SetRuntimeAPIKey("pixels", "sk-pixels")
		available, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeImage, "pixels")
		if err != nil || len(available) != 1 || available[0].ModelID() != "flux" {
			t.Fatalf("available = %v, %v", available, err)
		}
		if result := runtime.GenerateImages(t.Context(), model, imagesTestContext); result.StopReason != ai.ImagesStopReasonStop || len(calls) != 1 || calls[0].APIKey != "sk-pixels" {
			t.Fatalf("result = %+v, calls = %+v", result, calls)
		}
	})
	// model-runtime-images.test.ts:222
	t.Run("rejects image models at every chat entry point before provider dispatch", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		dispatches := 0
		reject := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			dispatches++
			panic("image reached chat adapter")
		}
		if err := runtime.RegisterNativeProvider(ai.CreateProvider(ai.CreateProviderOptions{
			ID:     "hybrid",
			Auth:   imagesTestKeyAuth("Hybrid key"),
			Models: []ai.AnyModel{imagesTestImageModel("hybrid", "shared")},
			API: &ai.ProviderStreams{Stream: reject, StreamSimple: reject,
				FetchDeferred: func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
					dispatches++
					panic("image reached chat adapter")
				},
				CancelDeferred: func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredCancelOptions) error {
					dispatches++
					panic("image reached chat adapter")
				}},
		})); err != nil {
			t.Fatal(err)
		}
		services.Registry().SetRuntimeAPIKey("hybrid", "sk-hybrid")
		image := imagesTestImageModel("hybrid", "shared")
		// The TypeScript test casts the image model to a chat model; a chat-typed struct carrying the image type is the Go equivalent.
		chat := &ai.Model{ID: image.ID, DisplayName: image.Name, Type: ai.ModelTypeImage, Input: image.Input, ProviderMeta: ai.ProviderMetadata{ProviderID: "hybrid", API: ai.API(image.API), BaseURL: image.BaseURL}}
		request := ai.Context{Messages: []ai.Message{}}
		handle := ai.DeferredHandle{Provider: "hybrid", ModelID: image.ID, API: ai.API(image.API), ID: "response-1"}
		results := map[string]*ai.AssistantMessage{
			"Stream":         runtime.Stream(t.Context(), chat, request, ai.StreamOptions{}).Result(),
			"Complete":       runtime.Complete(t.Context(), chat, request, ai.StreamOptions{}),
			"StreamSimple":   runtime.StreamSimple(t.Context(), chat, request, ai.StreamOptions{}).Result(),
			"CompleteSimple": runtime.CompleteSimple(t.Context(), chat, request, ai.StreamOptions{}),
			"StreamDeferred": runtime.StreamDeferred(t.Context(), chat, handle).Result(),
			"FetchDeferred":  runtime.FetchDeferred(t.Context(), chat, handle),
		}
		for name, result := range results {
			if result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, "is not a chat model") {
				t.Errorf("%s: stopReason=%s error=%q", name, result.StopReason, result.ErrorMessage)
			}
		}
		if err := runtime.CancelDeferred(t.Context(), chat, handle); err == nil || !strings.Contains(err.Error(), "is not a chat model") {
			t.Errorf("CancelDeferred error = %v", err)
		}
		if dispatches != 0 {
			t.Fatalf("chat dispatches = %d", dispatches)
		}
	})
	// model-runtime-images.test.ts:262
	t.Run("keeps image generation on a built-in provider composed with models.json overrides", func(t *testing.T) {
		services, _ := nativeCompatServices(t, `{"providers":{"openrouter":{"headers":{"X-Title":"pi"},"modelOverrides":{"openrouter/auto":{"name":"Auto (renamed)"},"google/gemini-3-pro-image":{"headers":{"X-Chat-Only":"yes"}}}}}}`, nil)
		runtime := services.ModelRuntime()
		provider := runtime.GetProvider("openrouter")
		if provider == nil || provider.GenerateImages == nil {
			t.Fatalf("provider = %+v", provider)
		}
		if model := runtime.GetModel("openrouter", "openrouter/auto"); model == nil || model.DisplayName != "Auto (renamed)" {
			t.Fatalf("renamed model = %+v", model)
		}
		if len(runtime.GetModelsOfType(ai.ModelTypeImage, "openrouter")) == 0 {
			t.Fatal("composed provider lost its image models")
		}
		// Auth is provider-scoped: the same key serves chat and image models.
		services.Registry().SetRuntimeAPIKey("openrouter", "sk-or")
		chat := runtime.GetModel("openrouter", "google/gemini-3-pro-image")
		image := runtime.GetModelOfType(ai.ModelTypeImage, "openrouter", "google/gemini-3-pro-image")
		chatAuth, err := runtime.GetModelAuth(t.Context(), chat)
		if err != nil {
			t.Fatal(err)
		}
		imageAuth, err := runtime.GetModelAuth(t.Context(), image)
		if err != nil || imageAuth == nil || chatAuth == nil {
			t.Fatalf("auth = %+v / %+v, %v", chatAuth, imageAuth, err)
		}
		if imageAuth.Auth.APIKey != "sk-or" {
			t.Fatalf("image key = %q", imageAuth.Auth.APIKey)
		}
		header := func(headers ai.ProviderHeaders, name string) string {
			if value := headers[name]; value != nil {
				return *value
			}
			return ""
		}
		if header(chatAuth.Auth.Headers, "X-Title") != "pi" || header(chatAuth.Auth.Headers, "X-Chat-Only") != "yes" {
			t.Fatalf("chat headers = %v", chatAuth.Auth.Headers)
		}
		if len(imageAuth.Auth.Headers) != 1 || header(imageAuth.Auth.Headers, "X-Title") != "pi" {
			t.Fatalf("image headers = %v", imageAuth.Auth.Headers)
		}
	})
	// model-runtime-images.test.ts:295 (chat-only composed providers; the typesafe classify assertion needs family 3)
	t.Run("does not add image generation to composed chat-only providers", func(t *testing.T) {
		services, _ := nativeCompatServices(t, `{"providers":{"anthropic":{"headers":{"X-Title":"pi"}}}}`, nil)
		runtime := services.ModelRuntime()
		if provider := runtime.GetProvider("anthropic"); provider == nil || provider.GenerateImages != nil {
			t.Fatalf("anthropic = %+v", provider)
		}
		if provider := runtime.GetProvider("openrouter"); provider == nil || provider.GenerateImages == nil {
			t.Fatalf("openrouter = %+v", provider)
		}
	})
}
