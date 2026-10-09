package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Review regressions for the typed ModelRuntime surface (Pi 0.99.1).

// lazy.ts lazyStream reports every setup rejection, including assertChatModel, with reason "error"; a cancelled signal does not turn it into "aborted".
func TestModelRuntimeNonChatRejectionIsAnErrorUnderCancellation(t *testing.T) {
	for _, provider := range []string{"openrouter", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			services, _ := nativeCompatServices(t, "", nil)
			runtime := services.ModelRuntime()
			chat := &ai.Model{ID: "shared", Type: ai.ModelTypeImage, ProviderMeta: ai.ProviderMetadata{ProviderID: provider, API: ai.APIOpenAICompletions, BaseURL: "https://images.test/v1"}}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			request := ai.Context{Messages: []ai.Message{}}
			for name, result := range map[string]*ai.AssistantMessage{
				"Stream":       runtime.Stream(ctx, chat, request, ai.StreamOptions{}).Result(),
				"StreamSimple": runtime.StreamSimple(ctx, chat, request, ai.StreamOptions{}).Result(),
			} {
				if result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, "is not a chat model") {
					t.Errorf("%s: stopReason=%s error=%q", name, result.StopReason, result.ErrorMessage)
				}
			}
		})
	}
}

func typedModelIDs(models []ai.AnyModel) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, string(model.ModelType())+":"+model.ModelID())
	}
	return ids
}

// models.ts getAvailableOfType filters getAllAvailable: a provider's filterModels decides which chat models are available and every other model type is kept. getAvailableOfType("chat") therefore agrees with getAvailable.
func TestModelRuntimeAvailableOfTypeAppliesChatAvailability(t *testing.T) {
	t.Run("native provider filterModels", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		if err := runtime.RegisterNativeProvider(ai.CreateProvider(ai.CreateProviderOptions{
			ID:   "filtered",
			Auth: imagesTestKeyAuth("Filtered key"),
			Models: []ai.AnyModel{
				nativeCompatModel("kept", "filtered", "https://filtered.test/v1"),
				nativeCompatModel("hidden", "filtered", "https://filtered.test/v1"),
				imagesTestImageModel("filtered", "flux"),
			},
			API: &ai.ProviderStreams{Stream: nativeUnusedStream, StreamSimple: nativeUnusedStream},
			FilterModels: func(models []*ai.Model, _ *ai.Credential) []*ai.Model {
				return slices.DeleteFunc(slices.Clone(models), func(model *ai.Model) bool { return model.ID == "hidden" })
			},
		})); err != nil {
			t.Fatal(err)
		}
		services.Registry().SetRuntimeAPIKey("filtered", "sk-filtered")
		chat, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeChat, "filtered")
		if err != nil || !reflect.DeepEqual(typedModelIDs(chat), []string{"chat:kept"}) {
			t.Fatalf("available chat = %v, %v", typedModelIDs(chat), err)
		}
		images, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeImage, "filtered")
		if err != nil || !reflect.DeepEqual(typedModelIDs(images), []string{"image:flux"}) {
			t.Fatalf("available images = %v, %v", typedModelIDs(images), err)
		}
	})
	t.Run("github-copilot account models", func(t *testing.T) {
		models := ai.ListModels("github-copilot")
		if len(models) < 2 {
			t.Fatal("expected GitHub Copilot models")
		}
		ids, err := json.Marshal([]string{models[0].ID})
		if err != nil {
			t.Fatal(err)
		}
		services, _ := nativeCompatServices(t, "", map[string]ai.Credential{"github-copilot": {Type: ai.CredentialOAuth, Refresh: "github-access-token", Access: "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;", Expires: time.Now().Add(time.Minute).UnixMilli(), AvailableModelIDs: ids}})
		runtime := services.ModelRuntime()
		chat, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeChat, "github-copilot")
		if err != nil || !reflect.DeepEqual(typedModelIDs(chat), []string{"chat:" + models[0].ID}) {
			t.Fatalf("available chat = %v, %v", typedModelIDs(chat), err)
		}
		available, err := runtime.GetAvailable(t.Context(), "github-copilot")
		if err != nil || len(available) != 1 || available[0].ID != models[0].ID {
			t.Fatalf("GetAvailable = %v, %v", available, err)
		}
	})
}

// provider-composer.ts composeModelProvider: an extension definition takes its missing baseUrl from the models.json-composed model list (applyExtension over applyModelsJson), so models.json's provider baseUrl reaches an extension image model without one.
func TestModelRuntimeExtensionImageDefaultsComeFromModelsJSON(t *testing.T) {
	services, _ := nativeCompatServices(t, `{"providers":{"openrouter":{"baseUrl":"https://or-config.test/v1"}}}`, nil)
	runtime := services.ModelRuntime()
	image := &ai.ImageModel{ID: "google/gemini-3-pro-image", Name: "Proxy image", Input: []string{"text"}, Output: []string{"image"}}
	if err := runtime.RegisterProvider("openrouter", ProviderConfigInput{APIKey: "extension-secret", Models: []ai.AnyModel{image}}); err != nil {
		t.Fatal(err)
	}
	model, _ := runtime.GetModelOfType(ai.ModelTypeImage, "openrouter", image.ID).(*ai.ImageModel)
	if model == nil || model.BaseURL != "https://or-config.test/v1" || model.API == "" {
		t.Fatalf("image model = %+v", model)
	}
}

// provider-composer.ts getAllModels: with an OAuth credential, modifyModels projects the chat models and the other model types follow them, so a chat model modifyModels adds is listed by getAllModels and getModelOfType("chat").
func TestModelRuntimeModifyModelsProjectionReachesTypedLists(t *testing.T) {
	services, _ := nativeCompatServices(t, "", map[string]ai.Credential{"extension-oauth": {Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}})
	runtime := services.ModelRuntime()
	model := nativeCompatModel("base", "extension-oauth", "https://example.test/v1")
	image := imagesTestImageModel("extension-oauth", "flux")
	if err := runtime.RegisterProvider("extension-oauth", ProviderConfigInput{BaseURL: model.ProviderMeta.BaseURL, API: model.ProviderMeta.API, Models: []ai.AnyModel{image, model}, Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, _ ai.ImagesOptions) (ai.AssistantImages, error) {
		return imagesTestOKResult(model), nil
	}}}, OAuth: &ExtensionOAuthConfig{Name: "Extension OAuth", RefreshToken: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil }, GetAPIKey: func(credential ai.Credential) string { return credential.Access }, ModifyModels: func(models []*ai.Model, credential ai.Credential) []*ai.Model {
		if credential.Access == "access" {
			return append(models, nativeCompatModel("credential-model", "extension-oauth", "https://example.test/v1"))
		}
		return models
	}}}); err != nil {
		t.Fatal(err)
	}
	if result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) > 0 {
		t.Fatalf("refresh=%+v", result)
	}
	if got, want := typedModelIDs(runtime.GetAllModels("extension-oauth")), []string{"chat:base", "chat:credential-model", "image:flux"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetAllModels = %v, want %v", got, want)
	}
	if runtime.GetModelOfType(ai.ModelTypeChat, "extension-oauth", "credential-model") == nil {
		t.Fatal("GetModelOfType misses the modifyModels projection")
	}
}

// provider-composer.ts composeModelProvider: a composed provider whose extension lacks the model's image API resolves an error result instead of rejecting.
func TestComposedProviderMissingImageImplementationResolvesAnErrorResult(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	image := imagesTestImageModel("extension-images", "flux")
	image.API = "other-images"
	if err := runtime.RegisterProvider("extension-images", ProviderConfigInput{APIKey: "k", Models: []ai.AnyModel{image}, Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, _ ai.ImagesOptions) (ai.AssistantImages, error) {
		return imagesTestOKResult(model), nil
	}}}}); err != nil {
		t.Fatal(err)
	}
	provider := runtime.GetProvider("extension-images")
	if provider == nil || provider.GenerateImages == nil {
		t.Fatalf("provider = %+v", provider)
	}
	model, _ := runtime.GetModelOfType(ai.ModelTypeImage, "extension-images", "flux").(*ai.ImageModel)
	if model == nil {
		t.Fatal("missing image model")
	}
	result, err := provider.GenerateImages(t.Context(), model, imagesTestContext, ai.ImagesOptions{})
	if err != nil || result.StopReason != ai.ImagesStopReasonError || result.ErrorMessage != `Provider extension-images has no image implementation for "other-images"` || result.Model != "flux" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

// model-runtime.ts getProvider returns a usable Provider; a built-in chat provider keeps its stream functions.
func TestModelRuntimeGetProviderKeepsStreamFunctions(t *testing.T) {
	services, _ := nativeCompatServices(t, `{"providers":{"anthropic":{"headers":{"X-Title":"pi"}},"custom-compatible":{"baseUrl":"https://compat.test/v1","apiKey":"k","api":"openai-completions","models":[{"id":"m"}]}}}`, nil)
	runtime := services.ModelRuntime()
	for _, id := range []string{"anthropic", "openai", "openrouter", "custom-compatible"} {
		provider := runtime.GetProvider(id)
		if provider == nil || provider.Stream == nil || provider.StreamSimple == nil {
			t.Fatalf("%s provider = %+v", id, provider)
		}
	}
}

// Models.getAllModels returns the provider's list without changing it; binding the chat models for the runtime must not write into a native provider's own slice.
func TestModelRuntimeGetAllModelsLeavesTheProviderListUnchanged(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	chat := nativeCompatModel("owned", "owned-list", "https://owned.test/v1")
	owned := []ai.AnyModel{chat, imagesTestImageModel("owned-list", "flux")}
	provider := nativeCompatProvider(chat)
	provider.ID = "owned-list"
	provider.GetAllModels = func() ([]ai.AnyModel, error) { return owned, nil }
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := typedModelIDs(runtime.GetAllModels("owned-list")); !reflect.DeepEqual(got, []string{"chat:owned", "image:flux"}) {
			t.Fatalf("GetAllModels = %v", got)
		}
	}
	if owned[0] != ai.AnyModel(chat) {
		t.Fatalf("provider list was rewritten: %p != %p", owned[0], chat)
	}
}

// model-runtime.ts getAllAvailable(providerId?) and getModels(providerId?): every available model of every type for one provider or all of them, and the chat models of one provider.
func TestModelRuntimeGetAllAvailableAndGetModelsTakeAProviderFilter(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	if err := runtime.RegisterNativeProvider(ai.CreateProvider(ai.CreateProviderOptions{
		ID:   "filtered",
		Auth: imagesTestKeyAuth("Filtered key"),
		Models: []ai.AnyModel{
			nativeCompatModel("kept", "filtered", "https://filtered.test/v1"),
			nativeCompatModel("hidden", "filtered", "https://filtered.test/v1"),
			imagesTestImageModel("filtered", "flux"),
		},
		API: &ai.ProviderStreams{Stream: nativeUnusedStream, StreamSimple: nativeUnusedStream},
		FilterModels: func(models []*ai.Model, _ *ai.Credential) []*ai.Model {
			return slices.DeleteFunc(slices.Clone(models), func(model *ai.Model) bool { return model.ID == "hidden" })
		},
	})); err != nil {
		t.Fatal(err)
	}
	services.Registry().SetRuntimeAPIKey("filtered", "sk-filtered")

	all, err := runtime.GetAllAvailable(t.Context(), "filtered")
	if err != nil || !reflect.DeepEqual(typedModelIDs(all), []string{"chat:kept", "image:flux"}) {
		t.Fatalf("GetAllAvailable(filtered) = %v, %v; want the filtered chat model and the image model", typedModelIDs(all), err)
	}
	if everything, err := runtime.GetAllAvailable(t.Context()); err != nil || len(everything) < len(all) {
		t.Fatalf("GetAllAvailable() = %d models, %v; want at least the provider's", len(everything), err)
	}
	models := runtime.GetModels("filtered")
	var ids []string
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	if !reflect.DeepEqual(ids, []string{"kept", "hidden"}) {
		t.Fatalf("GetModels(filtered) = %v, want that provider's chat models, unfiltered by availability", ids)
	}
	if len(runtime.GetModels()) <= len(models) {
		t.Fatalf("GetModels() must list every provider's chat models")
	}
}
