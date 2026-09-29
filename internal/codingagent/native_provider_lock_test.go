package codingagent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A credential transaction can overlap a registry writer. It must consult the
// operation's RuntimeCredentials overlay without acquiring the registry lock.
// Pi retains one credentials object for the operation (model-runtime.ts:133,
// models.ts:460-490); RuntimeCredentials.modify delegates to its own base store.
// Holding the writer here makes the old nested RLock deterministically deadlock,
// without a timing assertion, sleep, or a leaked goroutine.
func TestNativeRefreshCredentialTransactionDoesNotReenterRegistry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		credential ai.Credential
		runtimeKey string
		fail       bool
		missing    bool
	}{
		{name: "missing", missing: true},
		{name: "api-key", credential: ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"}},
		{name: "oauth", credential: ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: 1}},
		{name: "runtime-key", credential: ai.Credential{Type: ai.CredentialOAuth, Access: "access"}, runtimeKey: "runtime-key"},
		{name: "failure", credential: ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewModelRegistry(t.TempDir())
			const id = "corner-native"
			initial := map[string]ai.Credential{}
			if !tc.missing {
				initial[id] = tc.credential
			}
			store := &registryWriterCredentialStore{registry: r, base: ai.NewInMemoryAuthStorage(initial)}
			r.runtimeCredentials = ai.NewRuntimeCredentials(store)
			if tc.runtimeKey != "" {
				r.SetRuntimeAPIKey(id, tc.runtimeKey)
			}
			called := false
			failure := errors.New("credential resolution failed")
			p := &extension.NativeProvider{ID: id, Name: "Corner native", Models: []extension.ProviderModelConfig{},
				Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions, bool) (*ai.AssistantMessageEventStream, error) {
					return nil, errors.New("unused stream")
				},
				ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
					return nil, nil, errors.New("unused auth")
				},
				CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) { return nil, nil },
				RefreshModels: func(_ context.Context, _ *ai.Credential, _ *ai.ModelsStoreEntry, _ bool, _ *bool, _ func(extension.NativeProviderPublication) error) ([]extension.ProviderModelConfig, error) {
					return []extension.ProviderModelConfig{}, nil
				},
				ResolveRefreshCredential: func(_ context.Context, current *ai.Credential) (*ai.Credential, *ai.Credential, error) {
					called = true
					if tc.missing {
						if current != nil {
							t.Fatal("missing credential was fabricated")
						}
						return nil, nil, nil
					}
					if current == nil {
						t.Fatal("transaction lost its credential")
					}
					if tc.runtimeKey != "" {
						if current.Type != ai.CredentialAPIKey || current.Key != tc.runtimeKey {
							t.Fatalf("runtime overlay = %+v", current)
						}
					} else if current.Type != tc.credential.Type || current.Key != tc.credential.Key || current.Access != tc.credential.Access {
						t.Fatalf("stored credential = %+v", current)
					}
					if tc.fail {
						return nil, nil, failure
					}
					return current, nil, nil
				},
			}
			if err := r.RegisterNativeProvider(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			// refreshNativeProviders is the joined startup/ExtensionRefresh caller.
			errs := r.refreshNativeProviders(t.Context(), []string{id}, true, nil)
			if !called || tc.fail != errors.Is(errs[id], failure) {
				t.Fatalf("called=%v errors=%v", called, errs)
			}
			stored, err := store.base.Read(t.Context(), id)
			if tc.missing {
				if err != nil || stored != nil {
					t.Fatalf("missing credential was persisted: %+v, %v", stored, err)
				}
				return
			}
			if err != nil || stored == nil || stored.Type != tc.credential.Type || stored.Key != tc.credential.Key {
				t.Fatalf("non-persistent resolution changed the store: %+v, %v", stored, err)
			}
		})
	}
}

type registryWriterCredentialStore struct {
	registry *ModelRegistry
	base     *ai.InMemoryAuthStorage
}

func (s *registryWriterCredentialStore) Read(ctx context.Context, id string) (*ai.Credential, error) {
	return s.base.Read(ctx, id)
}
func (s *registryWriterCredentialStore) List(ctx context.Context) ([]ai.CredentialInfo, error) {
	return s.base.List(ctx)
}
func (s *registryWriterCredentialStore) Delete(ctx context.Context, id string) error {
	return s.base.Delete(ctx, id)
}
func (s *registryWriterCredentialStore) Modify(ctx context.Context, id string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	s.registry.mu.Lock()
	defer s.registry.mu.Unlock()
	return s.base.Modify(ctx, id, fn)
}
