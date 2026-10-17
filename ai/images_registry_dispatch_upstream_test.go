package ai

import (
	"context"
	"testing"
)

func restoreImagesAPIProvider(t *testing.T, api ImageAPI) {
	t.Helper()
	imagesAPIProviderMu.Lock()
	previous, existed := imagesAPIProviderRegistry[api]
	imagesAPIProviderMu.Unlock()
	t.Cleanup(func() {
		imagesAPIProviderMu.Lock()
		defer imagesAPIProviderMu.Unlock()
		if existed {
			imagesAPIProviderRegistry[api] = previous
		} else {
			delete(imagesAPIProviderRegistry, api)
		}
	})
}

// images-api-registry.ts getImagesApiProvider: an unregistered api has no provider; a registered one is returned for exactly its api.
// images.ts generateImages dispatches a request to the provider registered for the model's api and passes the model, context and
// options through; a later registration of the same api replaces the earlier one; an unregistered api fails with
// "No API provider registered for api: <api>".
func TestImagesAPIRegistryDispatchesByModelAPI(t *testing.T) {
	const a, b, missing ImageAPI = "images-dispatch-a", "images-dispatch-b", "images-dispatch-missing"
	restoreImagesAPIProvider(t, a)
	restoreImagesAPIProvider(t, b)
	restoreImagesAPIProvider(t, missing)

	if _, ok := GetImagesAPIProvider(missing); ok {
		t.Fatal("an unregistered api has a provider")
	}

	var gotModel ImageModel
	var gotContext ImagesContext
	var gotOptions ProviderImagesOptions
	RegisterImagesAPIProvider(ImagesAPIProvider{API: a, GenerateImages: func(_ context.Context, model ImageModel, request ImagesContext, options ProviderImagesOptions) AssistantImages {
		gotModel, gotContext, gotOptions = model, request, options
		return AssistantImages{ResponseID: "from-a"}
	}})
	RegisterImagesAPIProvider(ImagesAPIProvider{API: b, GenerateImages: func(context.Context, ImageModel, ImagesContext, ProviderImagesOptions) AssistantImages {
		return AssistantImages{ResponseID: "from-b"}
	}})

	if provider, ok := GetImagesAPIProvider(a); !ok || provider.API != a {
		t.Fatalf("provider for a = %+v, %t", provider, ok)
	}
	prompt := TextContent{Text: "draw"}
	result, err := GenerateImages(context.Background(), ImageModel{ID: "m", API: a}, ImagesContext{Input: []ImagesInputContent{prompt}}, ProviderImagesOptions{APIKey: "k"})
	if err != nil || result.ResponseID != "from-a" {
		t.Fatalf("a model of api a reached %q, %v", result.ResponseID, err)
	}
	if gotModel.ID != "m" || len(gotContext.Input) != 1 || gotContext.Input[0] != ImagesInputContent(prompt) || gotOptions.APIKey != "k" {
		t.Fatalf("the provider saw %+v %+v %+v", gotModel, gotContext, gotOptions)
	}
	if result, err := GenerateImages(context.Background(), ImageModel{API: b}, ImagesContext{}, ProviderImagesOptions{}); err != nil || result.ResponseID != "from-b" {
		t.Fatalf("a model of api b reached %q, %v", result.ResponseID, err)
	}

	RegisterImagesAPIProvider(ImagesAPIProvider{API: a, GenerateImages: func(context.Context, ImageModel, ImagesContext, ProviderImagesOptions) AssistantImages {
		return AssistantImages{ResponseID: "replacement"}
	}})
	if result, _ := GenerateImages(context.Background(), ImageModel{API: a}, ImagesContext{}, ProviderImagesOptions{}); result.ResponseID != "replacement" {
		t.Fatalf("a later registration replaces the earlier one: %q", result.ResponseID)
	}

	_, err = GenerateImages(context.Background(), ImageModel{API: missing}, ImagesContext{}, ProviderImagesOptions{})
	if err == nil || err.Error() != "No API provider registered for api: "+string(missing) {
		t.Fatalf("err = %v", err)
	}
}
