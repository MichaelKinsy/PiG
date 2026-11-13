package ai

import (
	"context"
	"testing"
)

// Models.generateImages (models.ts:1021-1038) is one try/catch: a throw of the provider's image function, such as the images registry's
// `Mismatched api: ${model.api} expected ${api}` (images-api-registry.ts:32), comes back as an error AssistantImages carrying that message, never as a throw.
func TestModelsGenerateImagesTurnsTheRegistrysMismatchedAPIThrowIntoAnErrorResult(t *testing.T) {
	const registered ImageAPI = "test-images-registered"
	const other ImageAPI = "test-images-other"
	t.Cleanup(func() {
		imagesAPIProviderMu.Lock()
		defer imagesAPIProviderMu.Unlock()
		delete(imagesAPIProviderRegistry, registered)
	})
	ran := 0
	RegisterImagesAPIProvider(ImagesAPIProvider{API: registered, GenerateImages: func(context.Context, ImageModel, ImagesContext, ProviderImagesOptions) AssistantImages {
		ran++
		return AssistantImages{ResponseID: "ran"}
	}})
	registry, ok := GetImagesAPIProvider(registered)
	if !ok {
		t.Fatal("the images provider is not registered")
	}
	model := &ImageModel{ID: "image-model", Provider: "p1", API: other}
	models := CreateModels()
	models.SetProvider(CreateProvider(CreateProviderOptions{ID: "p1", Models: []AnyModel{model}, Auth: typedNoAuth(), Images: ProviderImageAPIMap{
		// the provider wires the registry's function of another api for this model's api
		other: {GenerateImages: func(ctx context.Context, m *ImageModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
			return registry.GenerateImages(ctx, *m, request, options), nil
		}},
	}}))
	result := models.GenerateImages(t.Context(), model, ImagesContext{})
	if result.StopReason != ImagesStopReasonError || result.ErrorMessage != "Mismatched api: test-images-other expected test-images-registered" {
		t.Fatalf("result = %+v, want an error result with Pi's mismatched-api message", result)
	}
	if ran != 0 {
		t.Fatal("the registered provider ran for a model of another api")
	}
}
