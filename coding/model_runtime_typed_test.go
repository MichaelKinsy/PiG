package coding

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Regression tests for the typed ModelRuntime surface beyond the ported upstream cases.

// A built-in provider has no native registration, so its chat entry points run on the legacy backend path. Upstream assertChatModel (model-runtime.ts stream, streamSimple, streamDeferred, fetchDeferred, cancelDeferred) rejects a non-chat model there too.
func TestModelRuntimeRejectsNonChatModelsOnBuiltInProviders(t *testing.T) {
	for _, provider := range []string{"openrouter", "anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			services, _ := nativeCompatServices(t, "", nil)
			runtime := services.ModelRuntime()
			chat := &ai.Model{ID: "shared", Type: ai.ModelTypeImage, ProviderMeta: ai.ProviderMetadata{ProviderID: provider, API: ai.APIOpenAICompletions, BaseURL: "https://images.test/v1"}}
			request := ai.Context{Messages: []ai.Message{}}
			handle := ai.DeferredHandle{Provider: provider, ModelID: "shared", API: ai.APIOpenAICompletions, ID: "r1"}
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
		})
	}
}

// Image generation fails as a result, never an error, for an unknown provider and for a provider without image generation.
func TestModelRuntimeGenerateImagesReportsFailuresAsResults(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	unknown := runtime.GenerateImages(t.Context(), imagesTestImageModel("nobody", "x"), imagesTestContext)
	if unknown.StopReason != ai.ImagesStopReasonError || !strings.Contains(unknown.ErrorMessage, "Unknown provider: nobody") {
		t.Fatalf("unknown provider = %+v", unknown)
	}
	if err := runtime.RegisterNativeProvider(ai.CreateProvider(ai.CreateProviderOptions{
		ID:     "chatty",
		Auth:   imagesTestKeyAuth("Chatty key"),
		Models: []ai.AnyModel{imagesTestImageModel("chatty", "flux")},
		Images: ai.ProviderImageAPIMap{"other-images": {GenerateImages: func(context.Context, *ai.ImageModel, ai.ImagesContext, ai.ImagesOptions) (ai.AssistantImages, error) {
			return ai.AssistantImages{}, nil
		}}},
	})); err != nil {
		t.Fatal(err)
	}
	services.Registry().SetRuntimeAPIKey("chatty", "sk-chatty")
	result := runtime.GenerateImages(t.Context(), imagesTestImageModel("chatty", "flux"), imagesTestContext)
	if result.StopReason != ai.ImagesStopReasonError || !strings.Contains(result.ErrorMessage, `no image generation implementation for "test-images"`) {
		t.Fatalf("missing implementation = %+v", result)
	}
}

// Built-in providers with image models stay separated by type for available models, and a chat-only provider lists none.
func TestModelRuntimeAvailableModelsOfTypeAcrossProviderShapes(t *testing.T) {
	services, _ := nativeCompatServices(t, `{"providers":{"custom-compatible":{"baseUrl":"https://compat.test/v1","apiKey":"k","api":"openai-completions","models":[{"id":"m"}]}}}`, nil)
	runtime := services.ModelRuntime()
	services.Registry().SetRuntimeAPIKey("openrouter", "sk-or")
	images, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeImage)
	if err != nil || len(images) == 0 {
		t.Fatalf("available images = %d, %v", len(images), err)
	}
	for _, model := range images {
		if model.ProviderID() != "openrouter" || model.ModelType() != ai.ModelTypeImage {
			t.Fatalf("unexpected available image model %s/%s (%s)", model.ProviderID(), model.ModelID(), model.ModelType())
		}
	}
	for _, provider := range []string{"anthropic", "custom-compatible"} {
		if got := runtime.GetModelsOfType(ai.ModelTypeImage, provider); len(got) != 0 {
			t.Errorf("%s image models = %d", provider, len(got))
		}
	}
	if got := runtime.GetModelsOfType(ai.ModelTypeChat, "custom-compatible"); len(got) != 1 {
		t.Errorf("custom-compatible chat models = %d", len(got))
	}
}

// The image half of model-runtime-images.test.ts:129 for an extension registration on three provider shapes: an API-key provider, an OpenAI-compatible base URL, and a provider registered over a built-in provider. The classifier half waits for family 3.
func TestModelRuntimeExtensionImageModelsUseTheirImplementationHeadersAndBaseURL(t *testing.T) {
	for _, tc := range []struct {
		name, provider, baseURL, wantBaseURL string
	}{
		{"new provider", "extension-operations", "", "https://images.test/v1"},
		{"custom base URL", "extension-compatible", "https://proxy.test/v1", "https://proxy.test/v1"},
		{"over a built-in provider", "openrouter", "https://or-proxy.test/v1", "https://or-proxy.test/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services, _ := nativeCompatServices(t, "", nil)
			runtime := services.ModelRuntime()
			type observed struct {
				apiKey  string
				headers map[string]string
				baseURL string
			}
			var calls []observed
			image := imagesTestImageModel("ignored", "shared")
			image.Headers = map[string]string{"X-Operation": "image"}
			if tc.baseURL != "" {
				image.BaseURL = ""
			}
			err := runtime.RegisterProvider(tc.provider, ProviderConfigInput{
				APIKey:  "extension-secret",
				BaseURL: tc.baseURL,
				Models:  []ai.AnyModel{image},
				Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
					headers := map[string]string{}
					for name, value := range options.Headers {
						if value != nil {
							headers[name] = *value
						}
					}
					calls = append(calls, observed{options.APIKey, headers, model.BaseURL})
					return imagesTestOKResult(model), nil
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			model, _ := runtime.GetModelOfType(ai.ModelTypeImage, tc.provider, "shared").(*ai.ImageModel)
			if model == nil || model.Provider != tc.provider || model.BaseURL != tc.wantBaseURL || model.Headers != nil {
				t.Fatalf("registered model = %+v", model)
			}
			if result := runtime.GenerateImages(t.Context(), model, imagesTestContext); result.StopReason != ai.ImagesStopReasonStop {
				t.Fatalf("result = %+v", result)
			}
			if len(calls) != 1 || calls[0].apiKey != "extension-secret" || calls[0].headers["X-Operation"] != "image" || len(calls[0].headers) != 1 || calls[0].baseURL != tc.wantBaseURL {
				t.Fatalf("calls = %+v", calls)
			}
		})
	}
}

// The model-registry.ts facade (findOfType, getModelsOfType, getAvailableOfType, getModelOfType) reads the same typed lists as the runtime.
func TestModelRegistryTypedFacadeMatchesTheRuntime(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	registry, runtime := services.Registry(), services.ModelRuntime()
	images := registry.GetModelsOfType(ai.ModelTypeImage, "openrouter")
	if len(images) == 0 || len(images) != len(runtime.GetModelsOfType(ai.ModelTypeImage, "openrouter")) {
		t.Fatalf("images = %d", len(images))
	}
	id := images[0].ModelID()
	if registry.FindOfType(ai.ModelTypeImage, "openrouter", id) != images[0] || registry.GetModelOfType(ai.ModelTypeImage, "openrouter", id) != images[0] {
		t.Fatal("FindOfType/GetModelOfType did not return the listed model")
	}
	if registry.FindOfType(ai.ModelTypeImage, "openrouter", "missing") != nil || registry.FindOfType(ai.ModelTypeChat, "openrouter", id) != nil {
		t.Fatal("found a model of the wrong type or id")
	}
	if available, err := registry.GetAvailableOfType(t.Context(), ai.ModelTypeImage, "openrouter"); err != nil || len(available) != 0 {
		t.Fatalf("available without credentials = %d, %v", len(available), err)
	}
}
