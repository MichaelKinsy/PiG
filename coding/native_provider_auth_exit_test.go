package coding

// Pi resolves a provider's auth with resolveProviderAuth (auth/resolve.ts:46-117): the caller's signal reaches the provider's resolvers and toAuth, a runtime key is resolved before the stored credential is read, a still-valid credential takes no lock, and only an expiring OAuth credential's re-check and refresh run under credentials.modify (resolve.ts:119-155), where a started refresh finishes, bounded by the 15 s refresh timeout. So Pi's exit waits for that refresh and for nothing else. NativeProviderAuth runs the provider's ResolveAuth over the registry's credential store, whose tracked modifications (RuntimeCredentials.Settled) are what Close waits for.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// nativeAuthProvider is a subprocess extension's provider: its ResolveAuth is resolveProviderAuth over the store it is given, with the provider's own auth methods.
func nativeAuthProvider(id string, auth func(ctx context.Context) ai.ProviderAuth) *extension.NativeProvider {
	return &extension.NativeProvider{
		ID: id, Name: id, BaseURL: "https://example.test/v1",
		Models:    []extension.ProviderModelConfig{{ID: "model", Name: "model"}},
		Auth:      ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: id}},
		CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) { return nil, nil },
		ResolveAuth: func(ctx context.Context, store ai.CredentialStore, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
			return ai.ResolveProviderAuth(ctx, id, auth(ctx), store, ai.DefaultProviderAuthContext(), overrides)
		},
		ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
		Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return nil, errors.New("unused")
		},
	}
}

func rotated() ai.Credential {
	return ai.Credential{Type: ai.CredentialOAuth, Access: "new-access", Refresh: "new-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
}

func expiredOAuth() ai.Credential {
	return ai.Credential{Type: ai.CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Second).UnixMilli()}
}

// authExitFixture is the process's Runtime over services with the native provider id registered, and a gate to release what the test blocks.
type authExitFixture struct {
	runtime  *Runtime
	services *AgentSessionServices
	release  chan struct{}
	once     sync.Once
}

func newAuthExitFixture(t *testing.T, id string, stored *ai.Credential, auth func(ctx context.Context) ai.ProviderAuth) *authExitFixture {
	t.Helper()
	runtime, services, _, respond := rotatingOAuthRuntime(t)
	t.Cleanup(respond)
	f := &authExitFixture{runtime: runtime, services: services, release: make(chan struct{})}
	t.Cleanup(f.unblock)
	if stored != nil {
		if err := services.Auth().Set(id, *stored); err != nil {
			t.Fatal(err)
		}
	}
	if err := services.Registry().RegisterNativeProvider(t.Context(), nativeAuthProvider(id, auth)); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *authExitFixture) unblock() { f.once.Do(func() { close(f.release) }) }

// closesWithin fails the test unless Close returns within a few seconds.
func (f *authExitFixture) closesWithin(t *testing.T, what string) {
	t.Helper()
	closed := make(chan error, 1)
	go func() { closed <- f.runtime.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Close waits for %s, which Pi's exit does not wait for", what)
	}
}

func (f *authExitFixture) auth(t *testing.T, id string) *ai.Credential {
	t.Helper()
	got, err := f.services.Registry().ReadCredential(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The caller that stopped waiting (its request was cancelled, as at an exit) returns at once with the abort; the refresh that had started goes on and is saved, and Close waits for it.
func TestNativeProviderAuthSavesTheRotatedTokenAfterTheCallerWasCancelled(t *testing.T) {
	started := make(chan struct{})
	var f *authExitFixture
	f = newAuthExitFixture(t, "native-oauth", new(expiredOAuth()), func(context.Context) ai.ProviderAuth {
		return ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "Native",
			Refresh: func(context.Context, ai.Credential) (ai.Credential, error) {
				close(started)
				<-f.release
				return rotated(), nil
			},
			ToAuth: func(ai.Credential) (ai.ModelAuth, error) { return ai.ModelAuth{APIKey: "key"}, nil }}}
	})
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	returned := make(chan error, 1)
	go func() {
		_, err := f.services.Registry().NativeProviderAuth(ctx, "native-oauth", ai.AuthResolutionOverrides{})
		returned <- err
	}()
	<-started
	cancel(errors.New("exit"))
	if err := <-returned; err == nil {
		t.Fatal("NativeProviderAuth returned no error to a caller whose request was cancelled")
	}
	closed := make(chan error, 1)
	go func() { closed <- f.runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while the refresh was in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	f.unblock()
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if got := f.auth(t, "native-oauth"); got == nil || got.Refresh != "new-refresh" {
		t.Fatalf("auth.json after Close = %+v; want the rotated token", got)
	}
}

// The provider's resolvers get the caller's signal, so the abort reaches them and the caller returns at once; no lock is held past it.
func TestNativeProviderAuthGivesTheCallersCancellationToTheResolver(t *testing.T) {
	waiting := make(chan struct{})
	var calls atomic.Int32
	f := newAuthExitFixture(t, "native-wait", new(ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}), func(context.Context) ai.ProviderAuth {
		return ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Key", Resolve: func(ctx context.Context, _ ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			if calls.Add(1) > 1 {
				return &ai.AuthResult{}, nil
			}
			close(waiting)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		}}}
	})
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	first := make(chan error, 1)
	go func() {
		_, err := f.services.Registry().NativeProviderAuth(ctx, "native-wait", ai.AuthResolutionOverrides{})
		first <- err
	}()
	<-waiting
	cancel(errors.New("abort"))
	if err := <-first; err == nil {
		t.Fatal("the cancelled caller got no error")
	}
	second := make(chan error, 1)
	go func() {
		_, err := f.services.Registry().NativeProviderAuth(t.Context(), "native-wait", ai.AuthResolutionOverrides{})
		second <- err
	}()
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("second NativeProviderAuth = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next NativeProviderAuth is queued behind the cancelled one")
	}
}

// A refresh that finished is saved even when toAuth, which runs after it and outside the store's modify, fails with the abort.
func TestNativeProviderAuthSavesARefreshedCredentialWhenToAuthFailsWithTheAbort(t *testing.T) {
	refreshed := make(chan struct{})
	f := newAuthExitFixture(t, "native-late", new(expiredOAuth()), func(ctx context.Context) ai.ProviderAuth {
		return ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "Native",
			Refresh: func(context.Context, ai.Credential) (ai.Credential, error) {
				defer close(refreshed)
				return rotated(), nil
			},
			ToAuth: func(ai.Credential) (ai.ModelAuth, error) {
				<-ctx.Done()
				return ai.ModelAuth{}, context.Cause(ctx)
			}}}
	})
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	returned := make(chan error, 1)
	go func() {
		_, err := f.services.Registry().NativeProviderAuth(ctx, "native-late", ai.AuthResolutionOverrides{})
		returned <- err
	}()
	<-refreshed
	cancel(errors.New("abort"))
	<-returned
	if err := f.runtime.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if got := f.auth(t, "native-late"); got == nil || got.Refresh != "new-refresh" {
		t.Fatalf("auth.json after Close = %+v; want the rotated token", got)
	}
}

// An API-key resolver runs outside credentials.modify. Red when the whole resolution is tracked: a resolver that never returns, called with a context exit does not cancel (an extension's getModelAuth), keeps Close waiting.
func TestRuntimeCloseDoesNotWaitForAnAPIKeyResolverOfANativeProvider(t *testing.T) {
	started := make(chan struct{})
	var f *authExitFixture
	f = newAuthExitFixture(t, "native-key", new(ai.Credential{Type: ai.CredentialAPIKey, Key: "stored"}), func(context.Context) ai.ProviderAuth {
		return ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			close(started)
			<-f.release
			return &ai.AuthResult{}, nil
		}}}
	})
	go func() {
		_, _ = f.services.Registry().NativeProviderAuth(t.Context(), "native-key", ai.AuthResolutionOverrides{})
	}()
	<-started
	f.closesWithin(t, "an API-key resolver")
}

// toAuth runs after the refresh, outside credentials.modify (resolve.ts:175-188): the refresh is saved, and a toAuth that never returns does not keep Close waiting.
func TestRuntimeCloseDoesNotWaitForToAuthAfterARefresh(t *testing.T) {
	started := make(chan struct{})
	var f *authExitFixture
	f = newAuthExitFixture(t, "native-toauth", new(expiredOAuth()), func(context.Context) ai.ProviderAuth {
		return ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "Native",
			Refresh: func(context.Context, ai.Credential) (ai.Credential, error) { return rotated(), nil },
			ToAuth: func(ai.Credential) (ai.ModelAuth, error) {
				close(started)
				<-f.release
				return ai.ModelAuth{APIKey: "key"}, nil
			}}}
	})
	go func() {
		_, _ = f.services.Registry().NativeProviderAuth(t.Context(), "native-toauth", ai.AuthResolutionOverrides{})
	}()
	<-started
	f.closesWithin(t, "toAuth")
	if got := f.auth(t, "native-toauth"); got == nil || got.Refresh != "new-refresh" {
		t.Fatalf("auth.json = %+v; want the refreshed credential saved before toAuth ran", got)
	}
}

// A still-valid OAuth credential needs no refresh, so its resolution takes no lock and is not tracked: toAuth that never returns does not keep Close waiting, and another credential operation is not queued behind it.
func TestRuntimeCloseDoesNotWaitForTheResolutionOfAValidOAuthCredential(t *testing.T) {
	started := make(chan struct{})
	var f *authExitFixture
	valid := ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
	f = newAuthExitFixture(t, "native-valid", &valid, func(context.Context) ai.ProviderAuth {
		return ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "Native",
			Refresh: func(context.Context, ai.Credential) (ai.Credential, error) {
				return ai.Credential{}, errors.New("no refresh is due")
			},
			ToAuth: func(ai.Credential) (ai.ModelAuth, error) {
				close(started)
				<-f.release
				return ai.ModelAuth{APIKey: "key"}, nil
			}}}
	})
	go func() {
		_, _ = f.services.Registry().NativeProviderAuth(t.Context(), "native-valid", ai.AuthResolutionOverrides{})
	}()
	<-started
	other := make(chan error, 1)
	go func() { other <- f.services.Auth().Set("other", ai.Credential{Type: ai.CredentialAPIKey, Key: "k"}) }()
	select {
	case err := <-other:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("another credential write is queued behind the resolution of a valid credential")
	}
	f.closesWithin(t, "the resolution of a valid OAuth credential")
}

// A runtime API key is resolved before the stored credential is read (resolve.ts:55-62): with an expired OAuth credential stored, no refresh starts and no lock is taken, so a resolver that never returns does not keep Close waiting.
func TestRuntimeCloseDoesNotWaitForARuntimeKeyResolverWithAStoredOAuthCredential(t *testing.T) {
	started := make(chan struct{})
	var refreshes atomic.Int32
	var f *authExitFixture
	f = newAuthExitFixture(t, "native-runtime-key", new(expiredOAuth()), func(context.Context) ai.ProviderAuth {
		return ai.ProviderAuth{
			OAuth: &ai.OAuthAuth{Name: "Native",
				Refresh: func(context.Context, ai.Credential) (ai.Credential, error) { refreshes.Add(1); return rotated(), nil },
				ToAuth:  func(ai.Credential) (ai.ModelAuth, error) { return ai.ModelAuth{APIKey: "key"}, nil }},
			APIKey: &ai.APIKeyAuth{Name: "Key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
				close(started)
				<-f.release
				return &ai.AuthResult{}, nil
			}}}
	})
	f.services.Registry().SetRuntimeAPIKey("native-runtime-key", "runtime-key")
	go func() {
		_, _ = f.services.Registry().NativeProviderAuth(t.Context(), "native-runtime-key", ai.AuthResolutionOverrides{})
	}()
	<-started
	f.closesWithin(t, "a runtime key's resolver")
	if refreshes.Load() != 0 {
		t.Fatal("the stored OAuth credential was refreshed although a runtime key applies")
	}
}

// abortingStore cancels the caller once the store's lock is held, just before the transaction's callback: the abort that arrives between the read of the stored credential and the transaction.
type abortingStore struct {
	ai.CredentialStore
	cancel context.CancelCauseFunc
}

func (s abortingStore) Modify(ctx context.Context, id string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	return s.CredentialStore.Modify(ctx, id, func(current *ai.Credential) (*ai.Credential, error) {
		s.cancel(errors.New("abort"))
		return fn(current)
	})
}

// A refresh that the abort beat never starts: the in-transaction check (Pi's signal.throwIfAborted() at the top of the modify callback, resolve.ts:136) finds the abort, no token request is made and the credential is untouched.
func TestNativeProviderAuthNeverStartsARefreshTheAbortBeatInsideTheTransaction(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var refreshes atomic.Int32
	runtime, services, _, respond := rotatingOAuthRuntime(t)
	defer respond()
	if err := services.Auth().Set("native-beat", expiredOAuth()); err != nil {
		t.Fatal(err)
	}
	provider := nativeAuthProvider("native-beat", func(context.Context) ai.ProviderAuth {
		return ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "Native",
			Refresh: func(context.Context, ai.Credential) (ai.Credential, error) { refreshes.Add(1); return rotated(), nil },
			ToAuth:  func(ai.Credential) (ai.ModelAuth, error) { return ai.ModelAuth{APIKey: "key"}, nil }}}
	})
	inner := provider.ResolveAuth
	provider.ResolveAuth = func(ctx context.Context, store ai.CredentialStore, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
		return inner(ctx, abortingStore{store, cancel}, overrides)
	}
	if err := services.Registry().RegisterNativeProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	if _, err := services.Registry().NativeProviderAuth(ctx, "native-beat", ai.AuthResolutionOverrides{}); err == nil {
		t.Fatal("an aborted caller got no error")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if refreshes.Load() != 0 {
		t.Fatal("a refresh the abort beat was started")
	}
	got, err := services.Registry().ReadCredential(t.Context(), "native-beat")
	if err != nil || got == nil || got.Refresh != "old-refresh" {
		t.Fatalf("stored credential = %+v, %v; want it untouched", got, err)
	}
}
