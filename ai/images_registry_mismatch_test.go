package ai

import (
	"context"
	"testing"
)

// images-api-registry.ts wrapGenerateImages throws `Mismatched api: <model.api> expected <api>` before the provider runs when the model belongs to another api;
// images.ts generateImages (an async function) turns a throw into a rejection, so the dispatch itself never reaches a provider with a foreign model.
func TestRegisteredImagesProviderRefusesAModelOfAnotherAPIBeforeRunning(t *testing.T) {
	const api ImageAPI = "test-images-mismatch"
	t.Cleanup(func() {
		imagesAPIProviderMu.Lock()
		defer imagesAPIProviderMu.Unlock()
		delete(imagesAPIProviderRegistry, api)
	})
	ran := 0
	RegisterImagesAPIProvider(ImagesAPIProvider{API: api, GenerateImages: func(context.Context, ImageModel, ImagesContext, ProviderImagesOptions) AssistantImages {
		ran++
		return AssistantImages{ResponseID: "ran"}
	}})
	provider, ok := GetImagesAPIProvider(api)
	if !ok {
		t.Fatal("the provider is not registered")
	}
	func() {
		defer func() {
			err, _ := recover().(error)
			if err == nil || err.Error() != "Mismatched api: other expected "+string(api) {
				t.Fatalf("panic = %v, want Mismatched api: other expected %s", err, api)
			}
		}()
		provider.GenerateImages(t.Context(), ImageModel{API: "other"}, ImagesContext{}, ProviderImagesOptions{})
	}()
	if ran != 0 {
		t.Fatal("the provider ran for a model of another api")
	}
	if got := provider.GenerateImages(t.Context(), ImageModel{API: api}, ImagesContext{}, ProviderImagesOptions{}); got.ResponseID != "ran" || ran != 1 {
		t.Fatalf("a model of the registered api: result %+v, ran %d", got, ran)
	}
	if result, err := GenerateImages(t.Context(), ImageModel{API: api}, ImagesContext{}, ProviderImagesOptions{}); err != nil || result.ResponseID != "ran" {
		t.Fatalf("GenerateImages = %+v, %v", result, err)
	}
}
