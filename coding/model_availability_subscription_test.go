package coding

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/model-runtime-auth-options.test.ts:126. OAuth usage alone does not imply subscription billing.
func TestModelRuntimeDistinguishesSubscriptionOAuthUpstream(t *testing.T) {
	session, services := newAvailabilitySession(t)
	runtime := session.ModelRuntime()
	for id, credential := range map[string]ai.Credential{
		"anthropic":  {Type: ai.CredentialOAuth, Access: "anthropic-access", Refresh: "anthropic-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
		"openrouter": {Type: ai.CredentialOAuth, Access: "openrouter-key", Expires: 9007199254740991},
		"radius":     {Type: ai.CredentialOAuth, Access: "radius-access", Refresh: "radius-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
	} {
		if err := services.Auth().Set(id, credential); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"anthropic", "openrouter", "radius"} {
		if !runtime.IsUsingOAuth(id) || runtime.IsUsingSubscription(id) != (id == "anthropic") {
			t.Fatalf("%s OAuth=%v subscription=%v", id, runtime.IsUsingOAuth(id), runtime.IsUsingSubscription(id))
		}
	}
}

// Pi model-runtime.ts:472-477 reads the published AuthCheck and the current provider capability, not live credential storage.
func TestModelRuntimeSubscriptionUsesSnapshotAndCurrentCapability(t *testing.T) {
	synctest.Test(t, modelRuntimeSubscriptionUsesSnapshotAndCurrentCapability)
}

func modelRuntimeSubscriptionUsesSnapshotAndCurrentCapability(t *testing.T) {
	t.Helper()
	session, services := newAvailabilitySession(t)
	runtime := session.ModelRuntime()
	const id = "native-subscription"
	var holdAuth atomic.Bool
	authGate := make(chan struct{})
	releaseAuth := sync.OnceFunc(func() { close(authGate) })
	defer releaseAuth()
	provider := &ai.ModelsProvider{ID: id, GetModels: func() ([]*ai.Model, error) { return nil, nil }, Auth: ai.ProviderAuth{
		OAuth: &ai.OAuthAuth{IsSubscription: true},
		APIKey: &ai.APIKeyAuth{Check: func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if holdAuth.Load() {
				select {
				case <-authGate:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if input.Credential == nil {
				return nil, nil
			}
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored key"}, nil
		}},
	}}
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	if runtime.IsUsingOAuth(id) || runtime.IsUsingSubscription(id) {
		t.Fatal("provider capability alone became active OAuth usage")
	}
	if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialOAuth, Access: "oauth"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !runtime.IsUsingOAuth(id) || !runtime.IsUsingSubscription(id) {
		t.Fatal("native OAuth subscription was not published")
	}
	if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialAPIKey, Key: "key"}); err != nil {
		t.Fatal(err)
	}
	if !runtime.IsUsingOAuth(id) || !runtime.IsUsingSubscription(id) {
		t.Fatal("unrefreshed credential changed the published auth type")
	}
	replacement := *provider
	replacement.Auth.OAuth = &ai.OAuthAuth{}
	// Hold automatic reauthentication so the immediate cached-OAuth/current-capability assertion remains observable.
	holdAuth.Store(true)
	if err := runtime.RegisterNativeProvider(&replacement); err != nil {
		t.Fatal(err)
	}
	if !runtime.IsUsingOAuth(id) || runtime.IsUsingSubscription(id) {
		t.Fatal("subscription did not use the current provider capability")
	}
	releaseAuth()
	synctest.Wait()
	if _, err := runtime.GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runtime.IsUsingOAuth(id) || runtime.IsUsingSubscription(id) {
		t.Fatal("API-key refresh retained OAuth usage")
	}
}
