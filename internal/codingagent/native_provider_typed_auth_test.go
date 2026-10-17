package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// model-runtime.ts composeProvider takes `nativeExtensionProviders.get(id) ?? builtins.get(id)` as the base, so the
// provider getProvider returns carries the provider object's own OAuth method (with its loginLabel) and API-key login,
// whether the extension also registered a legacy configuration for the id or not.
func TestGetTypedProviderKeepsTheProviderObjectsAuth(t *testing.T) {
	login := func(context.Context, ai.AuthInteraction) (ai.Credential, error) { return ai.Credential{}, nil }
	oauthLogin := func(context.Context, ai.AuthInteraction, ai.LoginOptions) (ai.Credential, error) {
		return ai.Credential{}, nil
	}
	carrier := func() *extension.NativeProvider {
		return &extension.NativeProvider{
			ID: "typed-auth", Name: "Typed Auth", BaseURL: "https://typed.invalid",
			Models: []extension.ProviderModelConfig{{ID: "m", Name: "M", API: ai.APIOpenAICompletions, BaseURL: "https://typed.invalid"}},
			Auth: ai.ProviderAuth{
				APIKey: &ai.APIKeyAuth{Name: "Typed key", Login: login},
				OAuth:  &ai.OAuthAuth{Name: "Typed account", LoginLabel: "Sign in with Typed", IsSubscription: true, Login: oauthLogin},
			},
			CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) { return nil, nil },
			ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
				return nil, nil, nil
			},
			ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) {
				return nil, nil, nil
			},
			Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return nil, nil
			},
		}
	}
	for _, withLegacyRegistration := range []bool{false, true} {
		name := "carrier only"
		if withLegacyRegistration {
			name = "carrier and legacy registration"
		}
		t.Run(name, func(t *testing.T) {
			registry := NewModelRegistry(t.TempDir())
			registry.SetCredentialStore(ai.NewInMemoryAuthStorage(map[string]ai.Credential{}))
			if withLegacyRegistration {
				if err := registry.RegisterExtensionProvider("typed-auth", extension.ProviderConfig{Name: "Typed Auth", BaseURL: "https://typed.invalid", API: ai.APIOpenAICompletions, APIKey: "TYPED_KEY"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := registry.RegisterNativeProvider(context.Background(), carrier()); err != nil {
				t.Fatal(err)
			}
			provider := registry.GetTypedProvider("typed-auth", nil)
			if provider == nil {
				t.Fatal("GetTypedProvider returned no provider")
			}
			oauth := provider.Auth.OAuth
			if oauth == nil || oauth.Name != "Typed account" || oauth.LoginLabel != "Sign in with Typed" || !oauth.IsSubscription || oauth.Login == nil {
				t.Errorf("OAuth method = %+v, want the provider object's own (name, loginLabel, subscription, login)", oauth)
			}
			// The name tells the object's own method from the generic "API key" method a composition fabricates, whose login is not nil either.
			if apiKey := provider.Auth.APIKey; apiKey == nil || apiKey.Name != "Typed key" || apiKey.Login == nil {
				t.Errorf("API-key method = %+v, want the provider object's own (name, login)", apiKey)
			}
		})
	}
}
