package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The extension bridge encodes the typed models once per registry change: a provider registered between two reads of
// the registry state must reach the second read (model-registry.ts:145-161: the typed accessors read every model type).
func TestExtensionRegistryStateTypedModelsFollowRegistrations(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	probe := wireTypedModelOperations(t, services)
	if before := typedWireState(t, probe); typedWireFind(typedWireModels(before, "typedModels"), "late-provider", "late-image") != nil {
		t.Fatal("the provider exists before it is registered")
	}
	if err := services.ModelRuntime().RegisterProvider("late-provider", ProviderConfigInput{
		APIKey: "late-secret",
		Models: []ai.AnyModel{imagesTestImageModel("ignored", "late-image")},
		Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, _ ai.ImagesOptions) (ai.AssistantImages, error) {
			return imagesTestOKResult(model), nil
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	after := typedWireState(t, probe)
	if typedWireFind(typedWireModels(after, "typedModels"), "late-provider", "late-image") == nil {
		t.Fatal("the registered image model is missing from the state read after the registration")
	}
	services.Registry().UnregisterProvider("late-provider")
	if gone := typedWireState(t, probe); typedWireFind(typedWireModels(gone, "typedModels"), "late-provider", "late-image") != nil {
		t.Fatal("the unregistered image model is still in the state")
	}
}
