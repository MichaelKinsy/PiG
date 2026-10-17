package coding

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// model-runtime.ts getAuth(model, overrides): the overrides reach the provider's resolution for a chat model and for an image model, and a
// runtime without services answers with ErrNoServices.
func TestModelRuntimeGetModelAuthPassesTheOverridesToTheProviderAndNeedsServices(t *testing.T) {
	if _, err := (*ModelRuntime)(nil).GetModelAuth(t.Context(), &ai.Model{}); !errors.Is(err, ErrNoServices) {
		t.Fatalf("a nil runtime = %v, want ErrNoServices", err)
	}
	if _, err := (&ModelRuntime{}).GetModelAuth(t.Context(), &ai.Model{}); !errors.Is(err, ErrNoServices) {
		t.Fatalf("a runtime without services = %v, want ErrNoServices", err)
	}
	services, _ := nativeCompatServices(t, `{"providers":{"openrouter":{"headers":{"X-Title":"pi"}}}}`, nil)
	runtime := services.ModelRuntime()
	services.Registry().SetRuntimeAPIKey("openrouter", "stored-key")
	chat := runtime.GetModel("openrouter", "google/gemini-3-pro-image")
	image := runtime.GetModelOfType(ai.ModelTypeImage, "openrouter", "google/gemini-3-pro-image")
	override := "override-key"
	for name, model := range map[string]ai.AnyModel{"chat": chat, "image": image} {
		stored, err := runtime.GetModelAuth(t.Context(), model)
		if err != nil || stored == nil || stored.Auth.APIKey != "stored-key" {
			t.Fatalf("%s: stored auth = %+v, %v", name, stored, err)
		}
		overridden, err := runtime.GetModelAuth(t.Context(), model, ai.AuthResolutionOverrides{APIKey: &override})
		if err != nil || overridden == nil || overridden.Auth.APIKey != override {
			t.Errorf("%s: the api key override did not reach the resolution: %+v, %v", name, overridden, err)
		}
	}
}

// model-registry.ts:210-218 registerProvider(provider) hands the Provider object to registerNativeProvider: a provider the runtime rejects
// (no id or getModels) is an error to the caller and registers nothing.
func TestModelRegistryRegisterProviderObjectReturnsTheRuntimesRejection(t *testing.T) {
	_, services := newAvailabilitySession(t)
	registry := services.Registry()
	before := len(services.ModelRuntime().GetProviders())
	if err := registry.RegisterProviderObject(&ai.ModelsProvider{}); err == nil {
		t.Fatal("a provider without an id or getModels was accepted")
	}
	if err := registry.RegisterProviderObject(nil); err == nil {
		t.Fatal("a nil provider was accepted")
	}
	if after := len(services.ModelRuntime().GetProviders()); after != before {
		t.Fatalf("a rejected provider changed the provider count from %d to %d", before, after)
	}
}
