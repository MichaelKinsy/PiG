package codingagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

type requestCredentialStore struct {
	ai.CredentialStore
	read func(context.Context, string) (*ai.Credential, error)
}

func (store requestCredentialStore) Read(ctx context.Context, id string) (*ai.Credential, error) {
	return store.read(ctx, id)
}

type postRefreshCredentialStore struct {
	ai.CredentialStore
	current *ai.Credential
	wrote   bool
}

func (store *postRefreshCredentialStore) Modify(_ context.Context, _ string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	next, err := fn(store.current)
	if err != nil {
		return nil, err
	}
	store.wrote = next != nil
	if next != nil {
		store.current = next
	}
	return store.current, err
}

// Pi packages/ai/src/models.ts:471-483 returns the store's post-modify OAuth credential and leaves an already refreshed or replaced credential unchanged.
func TestRadiusRefreshDoesNotRewriteConcurrentCredentialChange(t *testing.T) {
	for _, current := range []*ai.Credential{nil, {Type: ai.CredentialAPIKey, Key: "replacement"}, {Type: ai.CredentialOAuth, Access: "refreshed", Expires: time.Now().Add(time.Hour).UnixMilli()}} {
		store := &postRefreshCredentialStore{CredentialStore: ai.NewInMemoryAuthStorage(nil), current: current}
		registry := NewModelRegistryWithModelsPath("")
		registry.SetCredentialStore(store)
		post, err := registry.refreshRadiusOAuth(t.Context(), ai.NewRadiusGatewayProvider(ai.RadiusProviderOptions{}))
		registry.CloseModelTasks()
		if err != nil || store.wrote {
			t.Fatalf("concurrent credential was rewritten: wrote=%v err=%v", store.wrote, err)
		}
		if current != nil && current.Type == ai.CredentialOAuth {
			if post != current {
				t.Fatal("did not return the store's post-modify OAuth credential")
			}
		} else if post != nil {
			t.Fatal("returned a non-OAuth credential")
		}
	}
}

// Pi packages/ai/src/models.ts:489-498 and auth/resolve.ts read credentials with the caller signal and surface store failures, rather than treating them as absent credentials.
func TestRadiusInjectedCredentialReadPreservesContextAndError(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "")
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "request")
	readFailure := errors.New("injected store failure")
	registry := NewModelRegistryWithModelsPath("")
	defer registry.CloseModelTasks()
	registry.SetCredentialStore(requestCredentialStore{
		CredentialStore: ai.NewInMemoryAuthStorage(nil),
		read: func(readContext context.Context, id string) (*ai.Credential, error) {
			if readContext.Value(contextKey{}) != "request" || id != "radius" {
				t.Errorf("credential read lost request context or provider: value=%v provider=%s", readContext.Value(contextKey{}), id)
			}
			return nil, readFailure
		},
	})
	if _, err := registry.RadiusAPIKey(ctx, "radius"); !errors.Is(err, readFailure) {
		t.Fatalf("RadiusAPIKey error = %v, want store failure", err)
	}
}
