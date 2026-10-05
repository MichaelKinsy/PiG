package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// deadlineOAuthProvider records the context its refresh receives.
type deadlineOAuthProvider struct {
	spyOAuthProvider
	deadline *time.Time
	bounded  *bool
	received *context.Context
}

func (p deadlineOAuthProvider) RefreshTokenContext(ctx context.Context, _ OAuthCredentials) (OAuthCredentials, error) {
	*p.refreshCalls++
	*p.deadline, *p.bounded = ctx.Deadline()
	if p.received != nil {
		*p.received = ctx
	}
	return p.refreshed, nil
}

// cancellingOAuthProvider cancels the caller's context from inside its refresh and waits for that cancellation to reach the refresh.
type cancellingOAuthProvider struct {
	spyOAuthProvider
	cancel context.CancelFunc
}

// RefreshTokenContext cancels the caller mid-refresh, after the provider has rotated the refresh token.
func (p cancellingOAuthProvider) RefreshTokenContext(ctx context.Context, _ OAuthCredentials) (OAuthCredentials, error) {
	*p.refreshCalls++
	p.cancel()
	return OAuthCredentials{Access: "rotated-access", Refresh: "rotated-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}

// Pi resolves a stored OAuth credential for a request with resolveStoredOAuth: when it expires it calls credentials.modify, checks the credential again under the lock, refreshes only when it still expires, and bounds the refresh with AbortSignal.timeout(15_000) (resolve.ts:143-167; 0.99.1 resolve.ts:126-150). credentials.modify is withLockAsync, which waits up to 30 seconds for another process's lock. So while another process holds the lock and refreshes the same credential, Pi waits, finds the new credential and uses it without refreshing again. TestCopilotTokenRefreshWaitsForAnotherProcessUpstream runs Pi's resolveProviderAuth on this state. PiG's request auth for registry OAuth providers must do the same: refreshing outside the lock lets two processes both refresh and one overwrite the other's rotated refresh token.
func TestRegistryOAuthRefreshWaitsForAnotherProcessUpstream(t *testing.T) {
	expired := time.Now().Add(-10 * time.Minute).UnixMilli()
	fresh := time.Now().Add(time.Hour).UnixMilli()
	calls := 0
	useOAuthProvider(t, spyOAuthProvider{
		id:           "spy-oauth-lock",
		refreshCalls: &calls,
		refreshed:    OAuthCredentials{Access: "refreshed-here", Refresh: "refresh-here", Expires: fresh},
	})
	path := filepath.Join(t.TempDir(), "auth.json")
	stored := fmt.Sprintf(`{"spy-oauth-lock":{"type":"oauth","access":"old-access","refresh":"old-refresh","expires":%d}}`, expired)
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	// Another process holds the lock, and after 300 ms, longer than the synchronous lock's ten 20 ms attempts, writes its refreshed credential and releases the lock.
	if err := os.Mkdir(path+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(300 * time.Millisecond)
		holder := fmt.Sprintf(`{"spy-oauth-lock":{"type":"oauth","access":"holder-access","refresh":"holder-refresh","expires":%d}}`, fresh)
		if err := os.WriteFile(path, []byte(holder), 0o600); err != nil {
			t.Error(err)
		}
		if err := os.Remove(path + ".lock"); err != nil {
			t.Error(err)
		}
	}()
	key, ok, err := ResolveStoredAPIKeyFromStorageContext(t.Context(), storage, "spy-oauth-lock")
	<-released
	if err != nil || !ok || key != "holder-access" || calls != 0 {
		t.Fatalf("request auth while another process refreshes = %q, %v, %v with %d refreshes; want the other process's token and no refresh", key, ok, err, calls)
	}
	var data map[string]map[string]any
	if raw, err := os.ReadFile(path); err != nil || json.Unmarshal(raw, &data) != nil || data["spy-oauth-lock"]["refresh"] != "holder-refresh" {
		t.Fatalf("store after the wait = %v, %v; want the other process's credential kept", data, err)
	}
}

// The refresh gets Pi's signal, AbortSignal.any([signal, AbortSignal.timeout(15_000)]) (resolve.ts:149-153): the caller's context with a 15 s deadline, marked so an extension provider forwards the timeout instead of treating the signal as the caller's own, and not cancelled when the refresh settles, because settling aborts nothing in Pi.
func TestRegistryOAuthRefreshIsBoundedUpstream(t *testing.T) {
	calls := 0
	var deadline time.Time
	var bounded bool
	var received context.Context
	useOAuthProvider(t, deadlineOAuthProvider{
		spyOAuthProvider: spyOAuthProvider{
			id:           "spy-oauth-bounded",
			refreshCalls: &calls,
			refreshed:    OAuthCredentials{Access: "refreshed", Refresh: "refresh-2", Expires: time.Now().Add(time.Hour).UnixMilli()},
		},
		deadline: &deadline,
		bounded:  &bounded,
		received: &received,
	})
	path := filepath.Join(t.TempDir(), "auth.json")
	stored := fmt.Sprintf(`{"spy-oauth-bounded":{"type":"oauth","access":"old-access","refresh":"old-refresh","expires":%d}}`, time.Now().Add(-time.Minute).UnixMilli())
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	key, _, err := ResolveStoredAPIKeyFromStorageContext(t.Context(), storage, "spy-oauth-bounded")
	if err != nil || key != "refreshed" || calls != 1 {
		t.Fatalf("request auth = %q, %v with %d refreshes", key, err, calls)
	}
	if !bounded || deadline.After(start.Add(defaultOAuthRefreshTimeout+time.Second)) || deadline.Before(start.Add(defaultOAuthRefreshTimeout-time.Second)) {
		t.Fatalf("refresh deadline = %v (bounded %v), want about %v after the call", deadline.Sub(start), bounded, defaultOAuthRefreshTimeout)
	}
	if timeout, marked := OAuthRefreshTimeout(received); !marked || timeout != defaultOAuthRefreshTimeout {
		t.Fatalf("refresh signal = %v, %v; want the 15 s timeout alone", timeout, marked)
	}
	if err := received.Err(); err != nil {
		t.Fatalf("refresh context after the refresh settled = %v; settling must abort nothing", err)
	}
}

// Pi refreshes a stored OAuth credential that expires within five minutes (DEFAULT_OAUTH_MINIMUM_VALIDITY_MS, resolve.ts:135-136), not only an expired one.
func TestRegistryOAuthRefreshesWithinTheMinimumValidityUpstream(t *testing.T) {
	calls := 0
	useOAuthProvider(t, spyOAuthProvider{
		id:           "spy-oauth-window",
		refreshCalls: &calls,
		refreshed:    OAuthCredentials{Access: "refreshed", Refresh: "refresh-2", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	path := filepath.Join(t.TempDir(), "auth.json")
	stored := fmt.Sprintf(`{"spy-oauth-window":{"type":"oauth","access":"old-access","refresh":"old-refresh","expires":%d}}`, time.Now().Add(2*time.Minute).UnixMilli())
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if key, _, err := ResolveStoredAPIKeyFromStorageContext(t.Context(), storage, "spy-oauth-window"); err != nil || key != "refreshed" || calls != 1 {
		t.Fatalf("request auth two minutes before expiry = %q, %v with %d refreshes; want a refresh", key, err, calls)
	}
}

// A refresh that has started survives its caller's cancellation: the caller gets the cancellation, and the rotated credential is persisted, because the provider may already have invalidated the old refresh token (auth/resolve.ts:refreshStoredOAuthCredential; Pi's "persists an OAuth refresh that started before the request was cancelled").
func TestRegistryOAuthRefreshCancelledDuringTheRefreshPersistsTheCredentialUpstream(t *testing.T) {
	calls := 0
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	useOAuthProvider(t, cancellingOAuthProvider{
		spyOAuthProvider: spyOAuthProvider{id: "spy-oauth-cancel", refreshCalls: &calls},
		cancel:           cancel,
	})
	path := filepath.Join(t.TempDir(), "auth.json")
	stored := fmt.Sprintf(`{"spy-oauth-cancel":{"type":"oauth","access":"old-access","refresh":"old-refresh","expires":%d}}`, time.Now().Add(-time.Minute).UnixMilli())
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := ResolveStoredAPIKeyFromStorageContext(ctx, storage, "spy-oauth-cancel")
	if !errors.Is(err, context.Canceled) || ok || key != "" {
		t.Fatalf("request auth cancelled during its refresh = %q, %v, %v; want the cancellation", key, ok, err)
	}
	oauthRefreshWork.Wait()
	if calls != 1 {
		t.Fatalf("refreshes = %d, want 1", calls)
	}
	var data map[string]map[string]any
	if raw, err := os.ReadFile(path); err != nil || json.Unmarshal(raw, &data) != nil || data["spy-oauth-cancel"]["refresh"] != "rotated-refresh" {
		t.Fatalf("store after the cancelled refresh = %v, %v; want the rotated credential", data, err)
	}
}

// failingKeyOAuthProvider's getApiKey fails, as an extension's can across its process boundary.
type failingKeyOAuthProvider struct {
	spyOAuthProvider
}

var errKeyBridgeDown = errors.New("key bridge down")

func (failingKeyOAuthProvider) GetAPIKeyContext(context.Context, OAuthCredentials) (string, error) {
	return "", errKeyBridgeDown
}

// Pi's resolveStoredOAuth wraps a failure to derive auth from the credential as "OAuth auth derivation failed for <provider>" (resolve.ts:174-178).
func TestRegistryOAuthKeyDerivationFailureIsWrappedUpstream(t *testing.T) {
	calls := 0
	useOAuthProvider(t, failingKeyOAuthProvider{spyOAuthProvider{id: "spy-oauth-keyfail", refreshCalls: &calls}})
	path := filepath.Join(t.TempDir(), "auth.json")
	stored := fmt.Sprintf(`{"spy-oauth-keyfail":{"type":"oauth","access":"live","refresh":"r","expires":%d}}`, time.Now().Add(time.Hour).UnixMilli())
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ResolveStoredAPIKeyFromStorageContext(t.Context(), storage, "spy-oauth-keyfail")
	if err == nil || err.Error() != "OAuth auth derivation failed for spy-oauth-keyfail: key bridge down" || calls != 0 {
		t.Fatalf("request auth with a failing getApiKey = %v with %d refreshes", err, calls)
	}
	if modelsErr, ok := errors.AsType[*ModelsError](err); !ok || modelsErr.Code != ModelsErrorOAuth || !errors.Is(err, errKeyBridgeDown) {
		t.Fatalf("error kind = %#v (ModelsError %v); want ModelsErrorOAuth wrapping the provider's error", err, ok)
	}
}

// Pi reads the provider once per resolution (Models.getAuth, models.ts:564-566) and refreshes and derives auth through that one object. Extensions unregister providers on their own goroutines while requests resolve auth, so a second registry lookup could return nothing, or a replacement, in the middle of a resolution. A resolution that races an unregister must neither panic nor mix two registrations.
func TestRegistryOAuthResolveSurvivesAConcurrentUnregister(t *testing.T) {
	calls := 0
	provider := spyOAuthProvider{id: "spy-oauth-race", refreshCalls: &calls, refreshed: OAuthCredentials{Access: "fresh", Refresh: "r2", Expires: time.Now().Add(-time.Hour).UnixMilli()}}
	path := filepath.Join(t.TempDir(), "auth.json")
	stored := fmt.Sprintf(`{"spy-oauth-race":{"type":"oauth","access":"old","refresh":"r","expires":%d}}`, time.Now().Add(-time.Minute).UnixMilli())
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { UnregisterOAuthProvider(provider.ID()) })
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			RegisterOAuthProvider(provider.ID(), provider)
			UnregisterOAuthProvider(provider.ID())
		}
	}()
	defer func() { close(stop); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for i := 0; i < 300 && time.Now().Before(deadline); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iteration %d: resolution panicked while the provider was unregistered: %v", i, r)
				}
			}()
			_, _ = resolveStoredOAuthAPIKey(t.Context(), storage, provider.ID(), Credential{Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: time.Now().Add(-time.Minute).UnixMilli()})
		}()
	}
}

// registryMixProvider tags its refresh result and its key with its own name, so a key one registration derives from another registration's refresh is visible.
type registryMixProvider struct{ tag string }

func (registryMixProvider) ID() string               { return "spy-oauth-mix" }
func (p registryMixProvider) Name() string           { return "Mix " + p.tag }
func (registryMixProvider) UsesCallbackServer() bool { return false }
func (registryMixProvider) Login(OAuthLoginCallbacks) (OAuthCredentials, error) {
	return OAuthCredentials{}, nil
}
func (p registryMixProvider) RefreshToken(OAuthCredentials) (OAuthCredentials, error) {
	// Expired on return, so every resolution refreshes again.
	return OAuthCredentials{Access: p.tag + "-fresh", Refresh: "r", Expires: time.Now().Add(-time.Hour).UnixMilli()}, nil
}
func (p registryMixProvider) GetAPIKey(creds OAuthCredentials) string {
	return p.tag + ":" + creds.Access
}

// A replacement racing a resolution must not mix registrations: the refresh and the key come from the one provider Pi reads per resolution (models.ts:564-566).
func TestRegistryOAuthResolveDoesNotMixReplacedRegistrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	stored := fmt.Sprintf(`{"spy-oauth-mix":{"type":"oauth","access":"old","refresh":"r","expires":%d}}`, time.Now().Add(-time.Minute).UnixMilli())
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := registryMixProvider{"A"}, registryMixProvider{"B"}
	RegisterOAuthProvider(a.ID(), a)
	t.Cleanup(func() { UnregisterOAuthProvider(a.ID()) })
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			RegisterOAuthProvider(a.ID(), a)
			RegisterOAuthProvider(b.ID(), b)
		}
	}()
	defer func() { close(stop); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for i := 0; i < 400 && time.Now().Before(deadline); i++ {
		key, err := resolveStoredOAuthAPIKey(t.Context(), storage, a.ID(), Credential{Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: time.Now().Add(-time.Minute).UnixMilli()})
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if owner, access, _ := strings.Cut(key, ":"); access != owner+"-fresh" {
			t.Fatalf("iteration %d: key %q mixes two registrations", i, key)
		}
	}
}
