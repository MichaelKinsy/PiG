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
