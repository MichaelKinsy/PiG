package codingagent

// Pi: packages/coding-agent/src/core/provider-composer.ts

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

type composerRefreshProbe struct {
	mu     sync.Mutex
	events []string
}

func (probe *composerRefreshProbe) record(event string) {
	probe.mu.Lock()
	probe.events = append(probe.events, event)
	probe.mu.Unlock()
}

func (probe *composerRefreshProbe) seen() []string {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return append([]string(nil), probe.events...)
}

func composedRefresh(t *testing.T, probe *composerRefreshProbe, base func(ai.RefreshModelsContext) error, extension func(ai.RefreshModelsContext) ([]ai.AnyModel, error)) func(context.Context) error {
	t.Helper()
	provider := credentialTestProvider("composed")
	provider.RefreshModels = base
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	composed, err := registry.composeNativeProvider(provider, &ProviderConfigInput{Name: "x", RefreshModels: extension})
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context) error {
		return composed.RefreshModels(ai.RefreshModelsContext{Signal: ctx, Publish: func(ai.ModelsPublication) (bool, error) {
			probe.record("publish")
			return true, nil
		}})
	}
}

// provider-composer.ts:628-646 refreshModels awaits base.refreshModels, then extension.refreshModels, then (unless the signal aborted) context.publish: strictly sequential, a rejection of either step rejects the whole refresh, and nothing is published after it.
func TestComposedRefreshModelsRunsBaseThenExtensionThenPublishes(t *testing.T) {
	baseFailure, extensionFailure := errors.New("base refresh failed"), errors.New("extension refresh failed")
	t.Run("the extension starts after the base finished", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			probe := &composerRefreshProbe{}
			release := make(chan struct{})
			refresh := composedRefresh(t, probe, func(ai.RefreshModelsContext) error {
				probe.record("base:start")
				<-release
				probe.record("base:end")
				return nil
			}, func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
				probe.record("extension")
				return nil, nil
			})
			done := make(chan error, 1)
			go func() { done <- refresh(t.Context()) }()
			synctest.Wait()
			if got := probe.seen(); len(got) != 1 || got[0] != "base:start" {
				t.Fatalf("events while the base refresh runs = %v, want [base:start]", got)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			want := []string{"base:start", "base:end", "extension", "publish"}
			if got := probe.seen(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
				t.Fatalf("events = %v, want %v", got, want)
			}
		})
	})
	t.Run("a base failure skips the extension and the publication", func(t *testing.T) {
		probe := &composerRefreshProbe{}
		refresh := composedRefresh(t, probe, func(ai.RefreshModelsContext) error { return baseFailure }, func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
			probe.record("extension")
			return nil, nil
		})
		if err := refresh(t.Context()); !errors.Is(err, baseFailure) {
			t.Fatalf("refresh = %v, want %v", err, baseFailure)
		}
		if got := probe.seen(); len(got) != 0 {
			t.Fatalf("events = %v, want none", got)
		}
	})
	t.Run("an extension failure skips the publication", func(t *testing.T) {
		probe := &composerRefreshProbe{}
		refresh := composedRefresh(t, probe, func(ai.RefreshModelsContext) error { return nil }, func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
			return nil, extensionFailure
		})
		if err := refresh(t.Context()); !errors.Is(err, extensionFailure) {
			t.Fatalf("refresh = %v, want %v", err, extensionFailure)
		}
		if got := probe.seen(); len(got) != 0 {
			t.Fatalf("events = %v, want none", got)
		}
	})
	t.Run("an abort during the extension step publishes nothing", func(t *testing.T) {
		probe := &composerRefreshProbe{}
		cause := errors.New("refresh superseded")
		ctx, cancel := context.WithCancelCause(t.Context())
		refresh := composedRefresh(t, probe, func(ai.RefreshModelsContext) error { return nil }, func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
			cancel(cause)
			return nil, nil
		})
		// Pi returns from the callback without rejecting; Go reports the abort cause. ai.Models drops either once the signal aborted, so only another error is a failure here.
		if err := refresh(ctx); err != nil && !errors.Is(err, cause) {
			t.Fatalf("refresh = %v, want nil or %v", err, cause)
		}
		if got := probe.seen(); len(got) != 0 {
			t.Fatalf("events = %v, want none", got)
		}
	})
}

type composerSignalKey struct{}

// provider-composer.ts:358-376 adaptOAuth awaits config.login with callbacks.signal and config.refreshToken with the refresh signal; :448-481 the composed API-key check and resolve await inherited.resolve with the caller's input. Each composed method waits for its callback, hands it the caller's signal, and rejects with the callback's rejection.
func TestComposedProviderAuthAwaitsItsCallbacksWithTheCallersSignal(t *testing.T) {
	cause := errors.New("auth step failed")
	ctx := context.WithValue(t.Context(), composerSignalKey{}, "caller")
	var seen []string
	note := func(step string, signal context.Context) {
		owner, _ := signal.Value(composerSignalKey{}).(string)
		seen = append(seen, step+":"+owner)
	}
	provider := credentialTestProvider("composed-auth")
	provider.Auth.APIKey.Check = nil
	provider.Auth.APIKey.Resolve = func(signal context.Context, _ ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		note("resolve", signal)
		return nil, cause
	}
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	composed, err := registry.composeNativeProvider(provider, &ProviderConfigInput{Name: "x", OAuth: &ExtensionOAuthConfig{
		Name: "Extension OAuth",
		Login: func(signal context.Context, _ ai.OAuthLoginCallbacks) (ai.Credential, error) {
			note("login", signal)
			return ai.Credential{}, cause
		},
		RefreshToken: func(signal context.Context, _ ai.Credential) (ai.Credential, error) {
			note("refresh", signal)
			return ai.Credential{}, cause
		},
		GetAPIKey: func(credential ai.Credential) string { return credential.Access },
	}})
	if err != nil {
		t.Fatal(err)
	}
	// A stored credential without a key: check and resolve both go through the inherited resolve.
	input := ai.APIKeyAuthInput{Ctx: ai.DefaultProviderAuthContext(), Credential: &ai.Credential{Type: ai.CredentialAPIKey}}
	if result, err := composed.Auth.APIKey.Resolve(ctx, input); !errors.Is(err, cause) || result != nil {
		t.Fatalf("resolve = %+v, %v, want %v", result, err, cause)
	}
	if check, err := composed.Auth.APIKey.Check(ctx, input); !errors.Is(err, cause) || check != nil {
		t.Fatalf("check = %+v, %v, want %v", check, err, cause)
	}
	if _, err := composed.Auth.OAuth.Login(ctx, ai.AuthInteraction{}, ai.LoginOptions{}); !errors.Is(err, cause) {
		t.Fatalf("oauth login = %v, want %v", err, cause)
	}
	if _, err := composed.Auth.OAuth.Refresh(ctx, ai.Credential{Type: ai.CredentialOAuth}); !errors.Is(err, cause) {
		t.Fatalf("oauth refresh = %v, want %v", err, cause)
	}
	want := []string{"resolve:caller", "resolve:caller", "login:caller", "refresh:caller"}
	if !slices.Equal(seen, want) {
		t.Fatalf("callbacks and their signals = %v, want %v", seen, want)
	}
}
