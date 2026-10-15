package ai

import (
	"context"
	"slices"
	"testing"
)

// images-api-registry.ts registerImagesApiProvider/getImagesApiProvider and images.ts generateImages: a provider is keyed by its api, registering the api again
// replaces it, an unregistered api has none, and generateImages hands the model, context and options to the registered function and returns its result.
func TestImagesAPIRegistryKeysReplacesAndDispatchesByModelAPI(t *testing.T) {
	const api ImageAPI = "registry-semantics-images"
	if _, ok := GetImagesAPIProvider(api); ok {
		t.Fatal("the test api is registered before the test registered it")
	}
	t.Cleanup(func() {
		imagesAPIProviderMu.Lock()
		defer imagesAPIProviderMu.Unlock()
		delete(imagesAPIProviderRegistry, api)
	})
	model := ImageModel{ID: "m", Provider: "p", API: api}
	if _, err := GenerateImages(context.Background(), model, ImagesContext{}, ProviderImagesOptions{}); err == nil || err.Error() != "No API provider registered for api: "+string(api) {
		t.Fatalf("GenerateImages without a provider = %v", err)
	}
	var seenModel ImageModel
	var seenOptions ProviderImagesOptions
	var first ImagesFunction = func(_ context.Context, m ImageModel, _ ImagesContext, o ProviderImagesOptions) AssistantImages {
		seenModel, seenOptions = m, o
		return AssistantImages{Model: "first", API: api}
	}
	RegisterImagesAPIProvider(ImagesAPIProvider{API: api, GenerateImages: first}, "source-one")
	provider, ok := GetImagesAPIProvider(api)
	if !ok || provider.API != api {
		t.Fatalf("GetImagesAPIProvider = %+v, %v", provider, ok)
	}
	if _, ok := GetImagesAPIProvider(api + "-other"); ok {
		t.Fatal("another api resolved to the registered provider")
	}
	result, err := GenerateImages(context.Background(), model, ImagesContext{}, ProviderImagesOptions{APIKey: "key"})
	if err != nil || result.Model != "first" || seenModel.ID != "m" || seenOptions.APIKey != "key" {
		t.Fatalf("GenerateImages = %+v, %v (model %+v, options %+v)", result, err, seenModel, seenOptions)
	}
	RegisterImagesAPIProvider(ImagesAPIProvider{API: api, GenerateImages: func(context.Context, ImageModel, ImagesContext, ProviderImagesOptions) AssistantImages {
		return AssistantImages{Model: "second", API: api}
	}})
	if result, err := GenerateImages(context.Background(), model, ImagesContext{}, ProviderImagesOptions{}); err != nil || result.Model != "second" {
		t.Fatalf("after registering the api again GenerateImages = %+v, %v; want the replacement", result, err)
	}
}

// image-models.ts getImageProviders/getImageModels: providers and each provider's models are listed in sorted order, and only the generated catalog's providers appear.
func TestImageProvidersAndModelsAreListedSorted(t *testing.T) {
	providers := GetImageProviders()
	var want []BuiltinImageProvider
	for _, model := range GeneratedImageModels {
		if !slices.Contains(want, BuiltinImageProvider(model.Provider)) {
			want = append(want, BuiltinImageProvider(model.Provider))
		}
	}
	slices.Sort(want)
	if !slices.Equal(providers, want) {
		t.Fatalf("GetImageProviders = %v, want the catalog's providers sorted %v", providers, want)
	}
	for _, provider := range providers {
		var ids []string
		for _, model := range GetImageModels(provider) {
			ids = append(ids, model.ID)
		}
		var wantIDs []string
		for _, model := range GeneratedImageModels {
			if BuiltinImageProvider(model.Provider) == provider {
				wantIDs = append(wantIDs, model.ID)
			}
		}
		slices.Sort(wantIDs)
		if !slices.Equal(ids, wantIDs) {
			t.Fatalf("GetImageModels(%s) = %v, want %v", provider, ids, wantIDs)
		}
	}
	if model, ok := GetImageModel("no-such-provider", "x"); ok || model.ID != "" {
		t.Fatalf("GetImageModel of an unknown provider = %+v, %v", model, ok)
	}
	if _, ok := GetImageModel(providers[0], "no-such-model"); ok {
		t.Fatal("GetImageModel found a model that is not in the catalog")
	}
}
