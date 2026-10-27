package coding

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/model-runtime.ts:882-917 (registerNativeProvider, markProvisionallyConfigured) and :300-304 (composeProvider).
// The Session's registry: a Provider object an extension registered whose model has no baseUrl is composed untouched and its model is in the catalog; a stored
// credential marks it configured until the availability refresh the registration launched replaces the mark, and its check finding no key leaves it unconfigured.
func TestExtensionNativeProviderWithoutBaseURLComposesAndSettlesLikePi(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		const id = "bare-ext-native"
		if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}); err != nil {
			t.Fatal(err)
		}
		runtime := services.ModelRuntime()
		if _, err := runtime.GetAvailable(t.Context()); err != nil {
			t.Fatal(err)
		}
		provider := &extension.NativeProvider{ID: id, Name: "Bare Ext", Models: []extension.ProviderModelConfig{{ID: "model", API: ai.APIOpenAICompletions}},
			CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) { return nil, nil },
			ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
				return nil, nil, nil
			},
			ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
			Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return ai.NewAssistantMessageEventStream(), nil
			},
		}
		if err := services.Registry().RegisterNativeProvider(t.Context(), provider); err != nil {
			t.Fatal(err)
		}
		if models := runtime.GetModels(id); len(models) != 1 || models[0].ID != "model" {
			t.Fatalf("catalog of %s = %v, want its model", id, models)
		}
		if !runtime.HasConfiguredAuth(id) {
			t.Fatal("a stored credential must mark the provider configured right after registration (provisional mark)")
		}
		services.Registry().YieldToRegistrationRefresh(t.Context())
		synctest.Wait()
		if runtime.HasConfiguredAuth(id) {
			t.Fatal("the settled state is configured, but the provider's check found no key (Pi's refresh replaces the provisional mark)")
		}
		requireAvailableIDs(t, runtime, id)
		if models := runtime.GetModels(id); len(models) != 1 {
			t.Fatalf("settled catalog of %s = %v, want its model", id, models)
		}
	})
}
