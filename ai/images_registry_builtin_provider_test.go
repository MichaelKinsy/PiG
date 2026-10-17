package ai

import "testing"

// upstream: packages/ai/src/image-models.ts:12-14,23-26,40-42: BuiltinImageProvider names exactly the catalog providers with at least one image model, getImageProviders lists those, and getImageModel(provider, id) reads the model by that provider.
func TestGetImageProvidersAreBuiltinImageProvidersWithModels(t *testing.T) {
	providers := GetImageProviders()
	if len(providers) == 0 {
		t.Fatal("no built-in image providers")
	}
	seen := map[string]bool{}
	for _, provider := range providers {
		named := provider
		models := GetImageModels(named)
		if len(models) == 0 {
			t.Errorf("%s listed as a built-in image provider without an image model", named)
		}
		for _, model := range models {
			got, ok := GetImageModel(named, model.ID)
			if !ok || got.Provider != string(named) {
				t.Errorf("GetImageModel(%s, %s) = %+v, %v", named, model.ID, got, ok)
			}
		}
		seen[string(provider)] = true
	}
	if !seen["openrouter"] {
		t.Errorf("openrouter missing from %v", providers)
	}
	if len(GetImageModels("no-such-provider")) != 0 {
		t.Error("a provider outside the catalog must have no image models")
	}
}
