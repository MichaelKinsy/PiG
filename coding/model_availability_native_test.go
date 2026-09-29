package coding

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi model-runtime.ts:286-312 queries the Models collection and every provider's auth, including providers without models. Models applies the provider's credential-dependent filter before publication.
func TestAvailabilityUsesNativeAuthAndAccountFilters(t *testing.T) {
	for _, shape := range []string{"api-key-custom-url", "oauth", "no-default-model"} {
		t.Run(shape, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session, services := newAvailabilitySession(t)
				runtime := session.ModelRuntime()
				id := "availability-" + shape
				models := []*ai.Model{nativeCompatModel("visible", id, "https://custom.invalid/v1"), nativeCompatModel("hidden", id, "https://custom.invalid/v1")}
				if shape == "no-default-model" {
					models = nil
				}
				kind := ai.CredentialAPIKey
				auth := ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Check: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
					if input.Credential == nil {
						return nil, nil
					}
					return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored key"}, nil
				}}}
				if shape == "oauth" {
					kind = ai.CredentialOAuth
					auth = ai.ProviderAuth{OAuth: &ai.OAuthAuth{}}
				}
				if err := services.Auth().Set(id, ai.Credential{Type: kind, Key: "configured", Access: "access", Refresh: "refresh"}); err != nil {
					t.Fatal(err)
				}
				provider := &ai.ModelsProvider{ID: id, Auth: auth, GetModels: func() ([]*ai.Model, error) { return models, nil }, FilterModels: func(models []*ai.Model, credential *ai.Credential) []*ai.Model {
					if credential == nil || credential.Type != kind {
						t.Error("account filter did not receive the stored credential")
					}
					return slices.DeleteFunc(slices.Clone(models), func(model *ai.Model) bool { return model.ID == "hidden" })
				}}
				if err := runtime.RegisterNativeProvider(provider); err != nil {
					t.Fatal(err)
				}
				// Registration owns background setup; its publication must finish before the observation under test.
				synctest.Wait()
				for _, scope := range []string{"", id} {
					available, err := runtime.GetAvailable(t.Context(), scope)
					if err != nil {
						t.Fatal(err)
					}
					var got []string
					for _, model := range available {
						if model.ProviderMeta.ProviderID == id {
							got = append(got, model.ID)
							if model.ProviderMeta.BaseURL != "https://custom.invalid/v1" {
								t.Fatalf("lost endpoint: %+v", model.ProviderMeta)
							}
						}
					}
					var want []string
					if shape != "no-default-model" {
						want = []string{"visible"}
					}
					if !slices.Equal(got, want) {
						t.Fatalf("scope %q models=%v, want %v", scope, got, want)
					}
				}
				requireAvailabilityAuth(t, runtime, id, ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored})
				if runtime.availability.snapshot.auth[id] == nil {
					t.Fatal("provider auth was omitted from the snapshot")
				}
			})
		})
	}
}

// Pi model-runtime.ts:338-375,537-557 invalidates an older full pass inside credential synchronization, before login/logout resolves.
func TestAvailabilityCredentialSynchronizationRejectsStalledFullPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, _ := newAvailabilitySession(t)
		runtime := session.ModelRuntime()
		const id = "availability-login"
		model := nativeCompatModel("visible", id, "https://custom.invalid/v1")
		provider := &ai.ModelsProvider{ID: id, GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{
			Login: func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
				return ai.Credential{Type: ai.CredentialAPIKey, Key: "configured"}, nil
			},
			Check: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
				if input.Credential == nil {
					return nil, nil
				}
				return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored key"}, nil
			},
		}}}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		synctest.Wait() // Settle setup before installing the deliberately stalled availability pass.
		stalled := stallAvailabilityCredentials(t, runtime)
		old := startAvailability(t, runtime, "")
		defer func() { stalled.release(nil); <-old }()
		waitAvailabilityStarted(t, stalled.started)
		var observedMu sync.Mutex
		var observed []bool
		detach := runtime.SetChangeListener(func() {
			available := slices.ContainsFunc(runtime.GetAvailableSnapshot(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == id })
			observedMu.Lock()
			observed = append(observed, available)
			observedMu.Unlock()
		})
		defer detach()
		if _, err := runtime.Login(t.Context(), id, ai.CredentialAPIKey, ai.AuthInteraction{}); err != nil {
			t.Fatal(err)
		}
		requireAvailabilityAuth(t, runtime, id, ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored})
		stalled.release(nil)
		if err := <-old; err != nil {
			t.Fatal(err)
		}
		requireAvailabilityAuth(t, runtime, id, ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored})
		if !slices.ContainsFunc(runtime.GetAvailableSnapshot(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == id }) {
			t.Fatal("login returned before model availability publication")
		}
		observedMu.Lock()
		loginObservations := slices.Clone(observed)
		observedMu.Unlock()
		if len(loginObservations) == 0 || slices.Contains(loginObservations, false) {
			t.Fatalf("login notified consumers before availability publication: %v", loginObservations)
		}
		if err := runtime.Logout(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		requireAvailabilityAuth(t, runtime, id, ai.AuthStatus{})
		if slices.ContainsFunc(runtime.GetAvailableSnapshot(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == id }) {
			t.Fatal("logout retained an available model")
		}
	})
}

func TestAvailabilityNativeAuthFailureSurfacesWithoutRequestResolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, _ := newAvailabilitySession(t)
		runtime := session.ModelRuntime()
		failure := errors.New("auth check unavailable")
		provider := &ai.ModelsProvider{ID: "availability-error", GetModels: func() ([]*ai.Model, error) { return nil, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{
			Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) { return nil, failure },
			Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
				t.Error("availability resolved request-only auth")
				return nil, nil
			},
		}}}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		synctest.Wait() // Only the registration setup precedes the original fresh-error observation below.
		if _, err := runtime.GetAvailable(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("availability error=%v, want %v", err, failure)
		}
		if runtime.GetError() == "" {
			t.Fatal("current native auth failure was not published")
		}
	})
}
