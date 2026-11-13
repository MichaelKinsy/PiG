package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func configuredCatalogProvider(id string, oauthRefreshes *int, networkCredentials *[]*ai.Credential) *ai.ModelsProvider {
	provider := credentialTestProvider(id)
	provider.Auth = ai.ProviderAuth{OAuth: &ai.OAuthAuth{
		Name: "Test OAuth",
		Login: func(context.Context, ai.AuthInteraction, ai.LoginOptions) (ai.Credential, error) {
			return ai.Credential{}, errors.New("not used")
		},
		Refresh: func(context.Context, ai.Credential) (ai.Credential, error) {
			*oauthRefreshes++
			return ai.Credential{}, errors.New("invalid_grant")
		},
		ToAuth: func(c ai.Credential) (ai.ModelAuth, error) { return ai.ModelAuth{APIKey: c.Access}, nil },
	}}
	provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
		if refresh.AllowNetwork {
			*networkCredentials = append(*networkCredentials, refresh.Credential)
		}
		return nil
	}
	provider.RefreshModelsAuth = ai.RefreshModelsAuthConfigured
	return provider
}

// pi-mono 6382d870 packages/ai/src/models.ts Provider.refreshModelsAuth: a native Provider object a compiled-in extension registers is used as it
// is (model-runtime.ts registerNativeProvider), so a "configured" provider keeps its stale OAuth login unrefreshed and its catalog refresh runs
// without a credential. The carrier a compiled-in extension registers through must carry the setting.
func TestRegisterNativeProviderKeepsConfiguredRefreshModelsAuth(t *testing.T) {
	store := ai.NewInMemoryCredentialStore()
	if _, err := store.Modify(t.Context(), "stale-catalog", func(*ai.Credential) (*ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialOAuth, Access: "expired", Refresh: "revoked", Expires: 0}, nil
	}); err != nil {
		t.Fatal(err)
	}
	var oauthRefreshes int
	var networkCredentials []*ai.Credential
	registry := NewModelRegistry(t.TempDir())
	registry.runtimeCredentials.Store(ai.NewRuntimeCredentials(store))
	t.Cleanup(func() { registry.CloseModelTasks(); registry.NativeModels().Close() })
	if err := registry.RegisterNativeProvider(t.Context(), extension.NativeProviderOf(configuredCatalogProvider("stale-catalog", &oauthRefreshes, &networkCredentials))); err != nil {
		t.Fatal(err)
	}
	registry.YieldToRegistrationRefresh(t.Context())

	result := registry.RefreshNativeProviders(t.Context(), ai.ModelsRefreshOptions{Providers: []string{"stale-catalog"}, Force: new(true)})

	if result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("refresh = %+v", result)
	}
	if oauthRefreshes != 0 {
		t.Fatalf("OAuth refreshes = %d, want 0", oauthRefreshes)
	}
	if len(networkCredentials) != 1 || networkCredentials[0] != nil {
		t.Fatalf("network refresh credentials = %v, want one nil credential", networkCredentials)
	}
}

// provider-composer.ts composeModelProvider: refreshModelsAuth is the base provider's unless an extension supplies refreshModels or
// oauth.modifyModels, which read context.credential and so need the default credential refresh.
func TestComposeProviderRefreshModelsAuth(t *testing.T) {
	var refreshes int
	var credentials []*ai.Credential
	hook := func(ai.RefreshModelsContext) ([]ai.AnyModel, error) { return nil, nil }
	for _, tc := range []struct {
		name      string
		extension *ProviderConfigInput
		want      ai.RefreshModelsAuthMode
	}{
		{"an extension without refresh hooks keeps the base's", &ProviderConfigInput{Name: "x"}, ai.RefreshModelsAuthConfigured},
		{"an extension's refreshModels needs the credential", &ProviderConfigInput{Name: "x", RefreshModels: hook}, ""},
		{"an extension's oauth.modifyModels needs the credential", &ProviderConfigInput{Name: "x", OAuth: &extension.ExtensionOAuthConfig{Name: "o", ModifyModels: func(models []*ai.Model, _ ai.Credential) ([]*ai.Model, error) { return models, nil }}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewModelRegistry(t.TempDir())
			t.Cleanup(registry.CloseModelTasks)
			composed, err := registry.composeNativeProvider(configuredCatalogProvider("composed", &refreshes, &credentials), tc.extension)
			if err != nil {
				t.Fatal(err)
			}
			if composed.RefreshModelsAuth != tc.want {
				t.Fatalf("RefreshModelsAuth = %q, want %q", composed.RefreshModelsAuth, tc.want)
			}
		})
	}
	t.Run("a models.json override keeps the base's", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "models.json")
		if err := os.WriteFile(path, []byte(`{"providers":{"composed":{"headers":{"X-Config":"c"}}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		registry := NewModelRegistryWithModelsPath(path)
		t.Cleanup(registry.CloseModelTasks)
		composed, err := registry.composeNativeProvider(configuredCatalogProvider("composed", &refreshes, &credentials), nil)
		if err != nil {
			t.Fatal(err)
		}
		if composed.RefreshModelsAuth != ai.RefreshModelsAuthConfigured {
			t.Fatalf("RefreshModelsAuth = %q, want %q", composed.RefreshModelsAuth, ai.RefreshModelsAuthConfigured)
		}
	})
}

// pi-mono ai/src/models.ts Provider.refreshModelsAuth through the callback carrier a subprocess extension's Provider object registers as: with
// "configured" the network phase runs without resolving a credential, so a stale OAuth login is not refreshed and the refresh receives none; the
// default resolves (and refreshes) the credential first and hands it to the refresh.
func TestSubprocessCarrierHonoursRefreshModelsAuth(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		mode                  ai.RefreshModelsAuthMode
		wantResolutions       int
		wantNetworkCredential bool
	}{
		{"configured", ai.RefreshModelsAuthConfigured, 0, false},
		{"credential", ai.RefreshModelsAuthCredential, 1, true},
		{"default", "", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var resolutions int
			var networkCredentials []*ai.Credential
			provider := &extension.NativeProvider{
				ID: "stale-carrier", Name: "Stale Carrier", BaseURL: "https://carrier.invalid", RefreshModelsAuth: tc.mode,
				CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
					return &ai.AuthCheck{Type: ai.CredentialOAuth, Source: "OAuth"}, nil
				},
				Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					return nil, nil
				},
				ResolveAuth: func(context.Context, ai.CredentialStore, ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
					return nil, nil
				},
				ResolveRefreshCredential: func(_ context.Context, current *ai.Credential) (*ai.Credential, *ai.Credential, error) {
					resolutions++
					return current, nil, nil
				},
				RefreshModels: func(refresh ai.RefreshModelsContext) error {
					if refresh.AllowNetwork {
						networkCredentials = append(networkCredentials, refresh.Credential)
					}
					return nil
				},
			}
			registry := NewModelRegistry(t.TempDir())
			registry.SetCredentialStore(ai.NewInMemoryAuthStorage(map[string]ai.Credential{"stale-carrier": {Type: ai.CredentialOAuth, Access: "expired", Refresh: "revoked"}}))
			t.Cleanup(func() { registry.CloseModelTasks(); registry.NativeModels().Close() })
			if err := registry.RegisterNativeProvider(t.Context(), provider); err != nil {
				t.Fatal(err)
			}
			registry.YieldToRegistrationRefresh(t.Context())
			networkCredentials, resolutions = nil, 0

			result := registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{Providers: []string{"stale-carrier"}, AllowNetwork: true, Force: new(true)})

			if result.Aborted || len(result.Errors) != 0 {
				t.Fatalf("refresh = %+v", result)
			}
			if resolutions != tc.wantResolutions {
				t.Fatalf("credential resolutions = %d, want %d", resolutions, tc.wantResolutions)
			}
			if len(networkCredentials) != 1 || (networkCredentials[0] != nil) != tc.wantNetworkCredential {
				t.Fatalf("network refresh credentials = %v, want one, present = %v", networkCredentials, tc.wantNetworkCredential)
			}
		})
	}
}
