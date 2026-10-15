package ai

import (
	"slices"
	"testing"
)

// upstream: packages/ai/src/image-models.ts:13-15,40-42 BuiltinImageProvider is each provider of the generated image catalog that has an image model, and getImageProviders lists the providers of imageModelsByProvider (those with at least one model).
func TestBuiltinImageProviderIsTheCatalogsImageProviders(t *testing.T) {
	want := map[BuiltinImageProvider]bool{}
	for _, model := range GeneratedImageModels {
		want[BuiltinImageProvider(model.Provider)] = true
	}
	got := GetImageProviders()
	if len(got) != len(want) || !slices.IsSorted(got) {
		t.Fatalf("GetImageProviders() = %v, want the %d catalog providers in stable order", got, len(want))
	}
	for _, provider := range got {
		if !want[provider] || len(GetImageModels(provider)) == 0 {
			t.Errorf("provider %q has no image model in the catalog", provider)
		}
	}
	if models := GetImageModels(BuiltinImageProvider("no-such-provider")); len(models) != 0 {
		t.Errorf("an unknown provider lists %d models, want none", len(models))
	}
}
