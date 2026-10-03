package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The public ModelRuntime reports successful persistence separately from failed synchronization, as Pi model-runtime.ts:94-111,734-751 does.
func TestModelRuntimeCredentialSynchronizationErrorRetainsCommit(t *testing.T) {
	for _, kind := range []ai.AuthType{ai.CredentialAPIKey, ai.CredentialOAuth} {
		t.Run(string(kind), func(t *testing.T) {
			services, _ := nativeCompatServices(t, "", nil)
			provider := nativeCompatProvider(nativeCompatModel("one", "committed", "https://custom.invalid/v1"))
			credential := ai.Credential{Type: kind, Key: "key"}
			if kind == ai.CredentialOAuth {
				credential = ai.Credential{Type: kind, Access: "access", Refresh: "refresh", Expires: 123}
			}
			login := func(context.Context, ai.AuthInteraction) (ai.Credential, error) { return credential, nil }
			provider.Auth.APIKey.Login = login
			oauthLogin := func(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
				return login(ctx, interaction)
			}
			// Pi's OAuthAuth requires refresh and toAuth (ai/src/auth/types.ts:206-230); the registration's scheduled availability check may resolve the committed OAuth credential.
			provider.Auth.OAuth = &ai.OAuthAuth{Login: oauthLogin, Refresh: func(_ context.Context, current ai.Credential) (ai.Credential, error) { return current, nil }, ToAuth: func(current ai.Credential) (ai.ModelAuth, error) {
				return ai.ModelAuth{APIKey: current.Access}, nil
			}}
			cause := errors.New("cached catalog unavailable")
			provider.RefreshModels = func(ai.RefreshModelsContext) error { return cause }
			runtime := services.ModelRuntime()
			if err := runtime.RegisterNativeProvider(provider); err != nil {
				t.Fatal(err)
			}
			_, err := runtime.Login(t.Context(), provider.ID, kind, ai.AuthInteraction{})
			committed, ok := errors.AsType[*CredentialSynchronizationError](err)
			if !ok || committed.Operation != CredentialSynchronizationLogin || committed.ProviderID != provider.ID || committed.Credential == nil || committed.Credential.Type != kind || !errors.Is(err, cause) {
				t.Fatalf("login error = %#v", err)
			}
			store, err := ai.NewAuthStorage(services.Auth().Path())
			if err != nil {
				t.Fatal(err)
			}
			if saved, err := store.Read(t.Context(), provider.ID); err != nil || saved == nil || saved.Type != kind {
				t.Fatalf("committed credential = %+v, %v", saved, err)
			}
			err = runtime.Logout(t.Context(), provider.ID)
			committed, ok = errors.AsType[*CredentialSynchronizationError](err)
			if !ok || committed.Operation != CredentialSynchronizationLogout || committed.Credential != nil || !errors.Is(err, cause) {
				t.Fatalf("logout error = %#v", err)
			}
			if saved, err := store.Read(t.Context(), provider.ID); err != nil || saved != nil {
				t.Fatalf("credential retained after logout = %+v, %v", saved, err)
			}
		})
	}
}
