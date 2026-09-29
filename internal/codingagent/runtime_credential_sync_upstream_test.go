package codingagent

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

func credentialTestProvider(id string) *ai.ModelsProvider {
	return &ai.ModelsProvider{ID: id, Name: id, GetModels: func() ([]*ai.Model, error) {
		return []*ai.Model{{ID: "dynamic", DisplayName: "Dynamic", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: id, API: ai.APIOpenAICompletions, BaseURL: "https://example.test/v1"}, Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}}, nil
	}, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "API key", Login: func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
		return ai.Credential{Type: ai.CredentialAPIKey, Key: id + "-key"}, nil
	}, Check: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
		if input.Credential == nil {
			return nil, nil
		}
		return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored"}, nil
	}, Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		if input.Credential == nil {
			return nil, nil
		}
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Source: "stored"}, nil
	}}}}
}
func credentialTestRuntime(t *testing.T, store ai.CredentialStore, providers ...*ai.ModelsProvider) *ModelRegistry {
	t.Helper()
	registry := NewModelRegistry(t.TempDir())
	registry.runtimeCredentials = ai.NewRuntimeCredentials(store)
	for _, provider := range providers {
		if err := registry.RegisterNativeModelsProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	// Each registration queues Pi's unawaited local refresh (model-runtime.ts:750). Pi's test awaits before arming failures, so run it here.
	registry.YieldToRegistrationRefresh(t.Context())
	ids := make([]string, len(providers))
	for i, provider := range providers {
		ids[i] = provider.ID
	}
	result := registry.RefreshNativeProviders(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: ids})
	if result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("initial refresh = %+v", result)
	}
	t.Cleanup(func() { registry.NativeModels().Close() })
	return registry
}

// Check the promised error shape without adding a test-only production error type.
func requireCredentialSynchronizationError(t *testing.T, err error, id string) {
	t.Helper()
	if err == nil {
		t.Fatal("missing CredentialSynchronizationError")
	}
	v := reflect.ValueOf(err)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Type().Name() != "CredentialSynchronizationError" {
		t.Fatalf("error = %T: %v, want CredentialSynchronizationError", err, err)
	}
	v = v.Elem()
	for field, want := range map[string]string{"ProviderID": id, "Operation": "login"} {
		got := v.FieldByName(field)
		if !got.IsValid() || got.Kind() != reflect.String || got.String() != want {
			t.Fatalf("%s in %#v, want %q", field, err, want)
		}
	}
	credential := v.FieldByName("Credential")
	if !credential.IsValid() || !credential.CanInterface() {
		t.Fatalf("missing credential in %#v", err)
	}
	if got, ok := credential.Interface().(*ai.Credential); !ok || got == nil || got.Type != ai.CredentialAPIKey || got.Key != id+"-key" {
		t.Fatalf("committed credential = %v", credential)
	}
}

func requireStoredRuntimeKey(t *testing.T, store ai.CredentialStore, id, key string) {
	t.Helper()
	got, err := store.Read(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if key == "" {
		if got != nil {
			t.Fatalf("stored=%#v", got)
		}
	} else if !reflect.DeepEqual(got, &ai.Credential{Type: ai.CredentialAPIKey, Key: key}) {
		t.Fatalf("stored=%#v", got)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:63
func TestCredentialRuntimePublishesBeforeLoginLogoutResolveUpstream(t *testing.T) {
	store := ai.NewInMemoryAuthStorage(nil)
	r := credentialTestRuntime(t, store, credentialTestProvider("dynamic"))
	if _, err := r.LoginNativeProvider(t.Context(), "dynamic", ai.CredentialAPIKey, ai.AuthInteraction{}); err != nil {
		t.Fatal(err)
	}
	if !r.HasConfiguredAuth("dynamic") || len(r.GetAvailable()) != 1 || r.GetAvailable()[0].ModelID != "dynamic" {
		t.Fatal("login snapshot not published")
	}
	requireStoredRuntimeKey(t, store, "dynamic", "dynamic-key")
	if err := r.LogoutNativeProvider(t.Context(), "dynamic"); err != nil {
		t.Fatal(err)
	}
	if r.HasConfiguredAuth("dynamic") || len(r.GetAvailable()) != 0 {
		t.Fatal("logout snapshot not published")
	}
	requireStoredRuntimeKey(t, store, "dynamic", "")
}

// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:78
func TestCredentialRuntimeOrdersSameProviderOperationsUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, finish := make(chan struct{}), make(chan struct{})
		provider := credentialTestProvider("ordered")
		provider.Auth.APIKey.Login = func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
			close(started)
			<-finish
			return ai.Credential{Type: ai.CredentialAPIKey, Key: "ordered-key"}, nil
		}
		store := ai.NewInMemoryAuthStorage(nil)
		r := credentialTestRuntime(t, store, provider)
		login, logout := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := r.LoginNativeProvider(t.Context(), "ordered", ai.CredentialAPIKey, ai.AuthInteraction{})
			login <- err
		}()
		<-started
		go func() { logout <- r.LogoutNativeProvider(t.Context(), "ordered") }()
		synctest.Wait()
		requireStoredRuntimeKey(t, store, "ordered", "")
		close(finish)
		if err := <-login; err != nil {
			t.Fatal(err)
		}
		if err := <-logout; err != nil {
			t.Fatal(err)
		}
		requireStoredRuntimeKey(t, store, "ordered", "")
		if r.HasConfiguredAuth("ordered") {
			t.Fatal("stale snapshot")
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:111
func TestCredentialRuntimeDifferentProvidersConcurrentUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		finish := make(chan struct{})
		started := make(chan string, 2)
		one, two := credentialTestProvider("one"), credentialTestProvider("two")
		for _, p := range []*ai.ModelsProvider{one, two} {
			id := p.ID
			p.Auth.APIKey.Login = func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
				started <- id
				<-finish
				return ai.Credential{Type: ai.CredentialAPIKey, Key: id}, nil
			}
		}
		r := credentialTestRuntime(t, ai.NewInMemoryAuthStorage(nil), one, two)
		done := make(chan error, 2)
		for _, id := range []string{"one", "two"} {
			go func() {
				_, err := r.LoginNativeProvider(t.Context(), id, ai.CredentialAPIKey, ai.AuthInteraction{})
				done <- err
			}()
		}
		<-started
		<-started
		close(finish)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:152
func TestCredentialRuntimeDoesNotWaitForUnrelatedAvailabilityUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var stall atomic.Bool
		finish := make(chan struct{})
		defer close(finish)
		unrelated := credentialTestProvider("unrelated")
		unrelated.Auth.APIKey.Check = func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if stall.Load() {
				<-finish
			}
			return nil, nil
		}
		r := credentialTestRuntime(t, ai.NewInMemoryAuthStorage(nil), credentialTestProvider("target"), unrelated)
		stall.Store(true)
		login := make(chan error, 1)
		go func() {
			_, err := r.LoginNativeProvider(t.Context(), "target", ai.CredentialAPIKey, ai.AuthInteraction{})
			login <- err
		}()
		synctest.Wait()
		select {
		case err := <-login:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("login waited for unrelated provider availability")
		}
		if !r.HasConfiguredAuth("target") {
			t.Fatal("target unavailable")
		}
		refresh := make(chan ai.ModelsRefreshResult, 1)
		go func() {
			refresh <- r.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{"target"}})
		}()
		synctest.Wait()
		select {
		case result := <-refresh:
			if result.Aborted {
				t.Fatal("scoped refresh aborted")
			}
		default:
			t.Fatal("scoped refresh waited for unrelated provider availability")
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:174
func TestCredentialRuntimeReportsAvailabilityCancellationUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var block atomic.Bool
		started, finish := make(chan struct{}), make(chan struct{})
		// Pi's check stays pending after abort (model-runtime-credential-sync.test.ts:185). Release it only after the assertions so the Models owner can drain it.
		defer close(finish)
		p := credentialTestProvider("cancelled-availability")
		p.Auth.APIKey.Check = func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if block.Load() {
				close(started)
				<-finish
			}
			if input.Credential != nil {
				return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored"}, nil
			}
			return nil, nil
		}
		r := credentialTestRuntime(t, ai.NewInMemoryAuthStorage(nil), p)
		r.SetRuntimeAPIKey(p.ID, "key")
		block.Store(true)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan ai.ModelsRefreshResult, 1)
		go func() {
			done <- r.RefreshModelRuntime(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{p.ID}})
		}()
		<-started
		cancel()
		synctest.Wait()
		select {
		case result := <-done:
			if !result.Aborted {
				t.Fatal("cancelled availability did not report aborted")
			}
			if len(result.Errors) != 0 {
				t.Fatalf("cancellation became a provider error: %v", result.Errors)
			}
		default:
			t.Fatal("cancelled availability waited for the non-cooperative check callback")
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:205
func TestCredentialRuntimeNoNetworkInsideCredentialChainUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var network atomic.Int32
		finish := make(chan struct{})
		defer close(finish)
		p := credentialTestProvider("local-only")
		p.RefreshModels = func(c ai.RefreshModelsContext) error {
			if c.AllowNetwork {
				network.Add(1)
				<-finish
			}
			return nil
		}
		r := credentialTestRuntime(t, ai.NewInMemoryAuthStorage(nil), p)
		done := make(chan error, 1)
		go func() {
			_, err := r.LoginNativeProvider(t.Context(), p.ID, ai.CredentialAPIKey, ai.AuthInteraction{})
			done <- err
		}()
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("credential operation waited for network")
		}
		if network.Load() != 0 || !r.HasConfiguredAuth(p.ID) {
			t.Fatal("credential operation ran network refresh or did not publish availability")
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:220
func TestCredentialRuntimeIndependentScopedRefreshesUpstream(t *testing.T) {
	firstSignal := make(chan context.Context, 1)
	finish := make(chan error, 1)
	one := credentialTestProvider("one")
	one.RefreshModels = func(c ai.RefreshModelsContext) error {
		ctx := c.Signal
		if c.AllowNetwork {
			firstSignal <- ctx
			return <-finish
		}
		return nil
	}
	r := credentialTestRuntime(t, ai.NewInMemoryAuthStorage(nil), one, credentialTestProvider("two"))
	r.SetRuntimeAPIKey("one", "one-key")
	r.SetRuntimeAPIKey("two", "two-key")
	done := make(chan ai.ModelsRefreshResult, 1)
	go func() {
		done <- r.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(true), Providers: []string{"one"}})
	}()
	signal := <-firstSignal
	r.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(true), Providers: []string{"two"}})
	if signal.Err() != nil {
		t.Fatal("unrelated refresh cancelled first")
	}
	finish <- nil
	<-done
}

type delayedCommitStore struct {
	*ai.InMemoryAuthStorage
	committed, finish chan struct{}
}

func (s *delayedCommitStore) Modify(ctx context.Context, id string, update func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	value, err := s.InMemoryAuthStorage.Modify(ctx, id, update)
	close(s.committed)
	<-s.finish
	return value, err
}

// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:255
func TestCredentialRuntimeWaitsForCommittedMutationSettlementUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &delayedCommitStore{InMemoryAuthStorage: ai.NewInMemoryAuthStorage(nil), committed: make(chan struct{}), finish: make(chan struct{})}
		r := credentialTestRuntime(t, store, credentialTestProvider("delayed-commit"))
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := r.LoginNativeProvider(ctx, "delayed-commit", ai.CredentialAPIKey, ai.AuthInteraction{})
			done <- err
		}()
		<-store.committed
		cancel()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("returned before commit settled: %v", err)
		default:
		}
		close(store.finish)
		requireCredentialSynchronizationError(t, <-done, "delayed-commit")
		requireStoredRuntimeKey(t, store, "delayed-commit", "delayed-commit-key")
	})
}

func TestCredentialRuntimePostCommitSynchronizationErrorsUpstream(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		name, id := "reports committed credentials when local synchronization fails", "broken-sync"
		if cancelled {
			name, id = "reports a typed error when cancellation interrupts post-commit synchronization", "cancelled-sync"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-credential-sync.test.ts:312,352
				var block atomic.Bool
				started, finish := make(chan struct{}), make(chan struct{})
				// Pi's cache refresh never settles on abort (line 324). Cleanup releases it after login's independent cancellation and committed credential are checked.
				defer close(finish)
				cacheError := errors.New("cache restore failed")
				p := credentialTestProvider(id)
				p.RefreshModels = func(c ai.RefreshModelsContext) error {
					if !c.AllowNetwork && block.Load() {
						close(started)
						if cancelled {
							<-finish
							return nil
						}
						return cacheError
					}
					return nil
				}
				store := ai.NewInMemoryAuthStorage(nil)
				r := credentialTestRuntime(t, store, p)
				block.Store(true)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					_, err := r.LoginNativeProvider(ctx, id, ai.CredentialAPIKey, ai.AuthInteraction{})
					done <- err
				}()
				<-started
				if cancelled {
					cancel()
				}
				synctest.Wait()
				select {
				case err := <-done:
					requireCredentialSynchronizationError(t, err, id)
					cause := cacheError
					if cancelled {
						cause = context.Canceled
					}
					if !errors.Is(err, cause) {
						t.Fatalf("synchronization error = %v, want cause %v", err, cause)
					}
				default:
					t.Fatal("login waited for the non-cooperative cache refresh callback")
				}
				requireStoredRuntimeKey(t, store, id, id+"-key")
			})
		})
	}
}
