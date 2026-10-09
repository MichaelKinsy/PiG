package ai

import (
	"context"
	"testing"
)

// .upstream/current/packages/ai/src/providers/images/register-builtins.ts:48-53 registerBuiltInImagesApiProviders registers the "openrouter-images" API with
// generateImagesOpenRouter, which settles a failure as an AssistantImages with stopReason "error" instead of throwing (register-builtins.ts:35-45).
func TestRegisterBuiltInImagesAPIProvidersRegistersOpenRouter(t *testing.T) {
	RegisterBuiltInImagesAPIProviders()
	provider, ok := GetImagesAPIProvider(APIImagesOpenRouter)
	if !ok || provider.API != APIImagesOpenRouter || provider.GenerateImages == nil {
		t.Fatalf("openrouter-images provider after registration = %+v, registered %v", provider, ok)
	}
	// Registering again replaces the entry (registerImagesApiProvider keys by api).
	RegisterBuiltInImagesAPIProviders()
	if again, ok := GetImagesAPIProvider(APIImagesOpenRouter); !ok || again.GenerateImages == nil {
		t.Fatalf("second registration lost the provider: %+v", again)
	}

	// Dispatch through GenerateImages reaches the registered function; with no API key it returns the error result, not a Go error.
	model := ImageModel{ID: "m", Provider: "openrouter", API: APIImagesOpenRouter}
	result, err := GenerateImages(context.Background(), model, ImagesContext{}, ProviderImagesOptions{})
	if err != nil {
		t.Fatalf("a failed generation must settle as a result, got error %v", err)
	}
	if result.StopReason != ImagesStopReasonError || result.API != APIImagesOpenRouter || result.Provider != "openrouter" || result.Model != "m" || len(result.Output) != 0 || result.ErrorMessage == "" {
		t.Fatalf("result = %+v, want the openrouter error result for model m", result)
	}
}
