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

// model-runtime.ts:613-631 setRuntimeApiKey and removeRuntimeApiKey queue behind the provider's other credential operations, change the runtime credential, then synchronize local state. A synchronization failure is a CredentialSynchronizationError that names the operation and carries the credential (an api_key for set, none for remove) while the runtime change stays committed.
func TestModelRuntimeRuntimeAPIKeyOperationsReportSynchronizationFailure(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	provider := nativeCompatProvider(nativeCompatModel("one", "runtimekey", "https://custom.invalid/v1"))
	cause := errors.New("cached catalog unavailable")
	provider.RefreshModels = func(ai.RefreshModelsContext) error { return cause }
	runtime := services.ModelRuntime()
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	err := runtime.SetRuntimeApiKey(t.Context(), provider.ID, "sk-runtime")
	committed, ok := errors.AsType[*CredentialSynchronizationError](err)
	if !ok || committed.Operation != CredentialSynchronizationSetRuntimeAPIKey || committed.ProviderID != provider.ID || committed.Credential == nil || committed.Credential.Type != ai.CredentialAPIKey || committed.Credential.Key != "sk-runtime" || !errors.Is(err, cause) {
		t.Fatalf("set error = %#v", err)
	}
	if key, ok := services.Registry().RuntimeAPIKey(provider.ID); !ok || key != "sk-runtime" {
		t.Fatalf("runtime key after a failed synchronization = %q, %v; the change stays committed", key, ok)
	}
	err = runtime.RemoveRuntimeApiKey(t.Context(), provider.ID)
	committed, ok = errors.AsType[*CredentialSynchronizationError](err)
	if !ok || committed.Operation != CredentialSynchronizationRemoveRuntimeAPIKey || committed.Credential != nil || !errors.Is(err, cause) {
		t.Fatalf("remove error = %#v", err)
	}
	if _, ok := services.Registry().RuntimeAPIKey(provider.ID); ok {
		t.Fatal("the runtime key must be removed")
	}
}

// An operation whose context already ended returns its cause before it changes anything (operationSignal, enqueueCredentialOperation).
func TestModelRuntimeRuntimeAPIKeyOperationHonorsACancelledContext(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	provider := nativeCompatProvider(nativeCompatModel("one", "runtimekey", "https://custom.invalid/v1"))
	runtime := services.ModelRuntime()
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	stop := errors.New("stop")
	cancel(stop)
	if err := runtime.SetRuntimeApiKey(ctx, provider.ID, "sk-never"); !errors.Is(err, stop) {
		t.Fatalf("SetRuntimeAPIKey on a cancelled context = %v, want the cancel cause", err)
	}
	if _, ok := services.Registry().RuntimeAPIKey(provider.ID); ok {
		t.Fatal("a cancelled operation must not set the key")
	}
}
