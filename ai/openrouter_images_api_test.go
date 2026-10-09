package ai

import (
	"context"
	"testing"
)

// api/openrouter-images.lazy.ts openrouterImagesApi: the accessor's generateImages forwards the model, context and options to the OpenRouter implementation, so a keyless request fails the way the direct call does.
func TestOpenRouterImagesAPIForwardsToTheImplementation(t *testing.T) {
	model := ImageModel{ID: "m", API: APIImagesOpenRouter, Provider: "openrouter", Output: []string{"image"}}
	request := ImagesContext{Input: []ImagesInputContent{TextContent{Text: "draw"}}}
	viaAccessor, err := OpenRouterImagesAPI().GenerateImages(context.Background(), &model, request, ImagesOptions{Env: ProviderEnv{}})
	if err != nil {
		t.Fatal(err)
	}
	direct := GenerateImagesOpenRouter(context.Background(), model, request, ImagesOptions{Env: ProviderEnv{}})
	if viaAccessor.StopReason != ImagesStopReasonError || viaAccessor.StopReason != direct.StopReason || viaAccessor.ErrorMessage != direct.ErrorMessage || viaAccessor.ErrorMessage == "" {
		t.Fatalf("accessor %+v direct %+v", viaAccessor, direct)
	}
	for _, provider := range BuiltinProviders() {
		if images := GetImageModels(BuiltinImageProvider(provider.ID)); len(images) > 0 && provider.GenerateImages == nil {
			t.Errorf("%s lists image models but has no image implementation", provider.ID)
		}
	}
}
