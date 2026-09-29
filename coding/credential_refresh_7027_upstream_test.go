package coding

import (
	"context"
	"github.com/MichaelKinsy/PiG/ai"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

// Public ModelRuntime caller boundary for upstream 7027:40. The exact in-memory credential-store variant is TestLoginSupersedesOlderStalledCatalogRefreshUpstream in internal/codingagent.
// Login synchronizes locally and supersedes the older network refresh without awaiting its provider callback.
func TestCredentialRefreshHang7027StalledLogin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Services created in the bubble own the registration refresh admitted there.
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		started, release := make(chan struct{}), make(chan struct{})
		var callers sync.WaitGroup
		// Pi leaves an unresolved Promise. Release only during cleanup so Go can drain the owned callback.
		defer func() {
			close(release)
			callers.Wait()
			services.Registry().NativeModels().Close()
		}()
		model := nativeCompatModel("dynamic", "stalled-login", "https://example.test/v1")
		model.DisplayName = "Dynamic"
		provider := nativeCompatProvider(model)
		provider.Name = "Stalled Login"
		provider.Auth.APIKey = &ai.APIKeyAuth{
			Name: "API key",
			Login: func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
				return ai.Credential{Type: ai.CredentialAPIKey, Key: "secret"}, nil
			},
			Check: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
				if input.Credential != nil && input.Credential.Key != "" {
					return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored key"}, nil
				}
				return nil, nil
			},
			Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
				key, source := "ambient-key", "ambient key"
				if input.Credential != nil {
					key = input.Credential.Key
					if key != "" {
						source = "stored key"
					}
				}
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: key}, Source: source}, nil
			},
		}
		provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
			if refresh.AllowNetwork {
				close(started)
				<-release
			}
			return nil
		}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		if result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{provider.ID}}); result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("initial refresh = %+v", result)
		}
		stalled := make(chan ai.ModelsRefreshResult, 1)
		callers.Go(func() {
			stalled <- runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(true), Providers: []string{provider.ID}})
		})
		<-started
		type loginResult struct {
			credential ai.Credential
			err        error
		}
		login := make(chan loginResult, 1)
		callers.Go(func() {
			credential, err := runtime.Login(t.Context(), provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{
				Prompt: func(context.Context, ai.AuthPrompt) (string, error) { return "unused", nil },
				Notify: func(ai.AuthEvent) {},
			})
			login <- loginResult{credential, err}
		})
		synctest.Wait()
		want := ai.Credential{Type: ai.CredentialAPIKey, Key: "secret"}
		select {
		case got := <-login:
			if got.err != nil || !reflect.DeepEqual(got.credential, want) {
				t.Fatalf("login = %+v", got)
			}
		default:
			t.Fatal("login waited for the older stalled network refresh")
		}
		if !slices.ContainsFunc(runtime.GetAvailableSnapshot(), func(m *ai.Model) bool { return m.ID == model.ID }) {
			t.Fatal("dynamic model is missing from the available snapshot after login")
		}
		if credential, err := services.Auth().Read(t.Context(), provider.ID); err != nil || !reflect.DeepEqual(credential, &want) {
			t.Fatalf("stored credential = %+v, error = %v", credential, err)
		}
		select {
		case result := <-stalled:
			if result.Aborted {
				t.Fatalf("superseded refresh = %+v, want aborted false", result)
			}
		default:
			t.Fatal("superseded refresh did not settle while the provider callback remained stalled")
		}
	})
}
