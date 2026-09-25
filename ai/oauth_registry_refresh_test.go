package ai

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// spyOAuthProvider records RefreshToken invocations and can be configured to
// fail the refresh, so tests can assert both the failure path and that the
// expiry guard skips refresh entirely on a live credential.
type spyOAuthProvider struct {
	id           string
	refreshCalls *int
	refreshErr   error
	refreshed    OAuthCredentials
}

func (s spyOAuthProvider) ID() string               { return s.id }
func (s spyOAuthProvider) Name() string             { return "Spy OAuth" }
func (s spyOAuthProvider) UsesCallbackServer() bool { return false }
func (s spyOAuthProvider) Login(OAuthLoginCallbacks) (OAuthCredentials, error) {
	return OAuthCredentials{}, nil
}
func (s spyOAuthProvider) RefreshToken(OAuthCredentials) (OAuthCredentials, error) {
	*s.refreshCalls++
	if s.refreshErr != nil {
		return OAuthCredentials{}, s.refreshErr
	}
	return s.refreshed, nil
}
func (s spyOAuthProvider) GetAPIKey(creds OAuthCredentials) string { return creds.Access }

type contextRefreshOAuthProvider struct {
	spyOAuthProvider
	contextCalls *int
}

func (p contextRefreshOAuthProvider) RefreshTokenContext(ctx context.Context, _ OAuthCredentials) (OAuthCredentials, error) {
	*p.contextCalls++
	return OAuthCredentials{}, ctx.Err()
}

// useOAuthProvider registers a provider for the duration of the test and removes
// it on cleanup. Custom providers take priority over built-ins, so this shadows
// any real provider with the same id.
func useOAuthProvider(t *testing.T, provider OAuthProviderInterface) {
	t.Helper()
	RegisterOAuthProvider(provider.ID(), provider)
	t.Cleanup(func() {
		customOAuthProvidersMu.Lock()
		delete(customOAuthProviders, provider.ID())
		customOAuthProvidersMu.Unlock()
	})
}

// TestResolveOAuthAPIKeyFromStorage_RefreshFailurePreservesStoredCredential
// closes the failure half of the auth-refresh risk-core surface. Upstream throws
// when a token refresh fails; it never returns a silent key and never persists a
// broken credential. pig must surface the error and leave the stored credential
// untouched so a transient refresh outage does not corrupt auth.json.
func TestResolveOAuthAPIKeyFromStorage_RefreshFailurePreservesStoredCredential(t *testing.T) {
	calls := 0
	useOAuthProvider(t, spyOAuthProvider{
		id:           "spy-oauth-fail",
		refreshCalls: &calls,
		refreshErr:   errors.New("token endpoint unreachable"),
	})

	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	orig := Credential{
		Type:    CredentialOAuth,
		Refresh: "old-refresh",
		Access:  "old-access",
		Expires: time.Now().Add(-time.Minute).UnixMilli(),
	}
	if err := store.Set("spy-oauth-fail", orig); err != nil {
		t.Fatal(err)
	}

	apiKey, err := ResolveOAuthAPIKeyFromStorage(store, "spy-oauth-fail")
	if err == nil {
		t.Fatal("refresh failure was swallowed; expected a surfaced error")
	}
	if apiKey != "" {
		t.Fatalf("apiKey = %q, want empty on refresh failure", apiKey)
	}
	if calls != 1 {
		t.Fatalf("RefreshToken called %d times, want exactly 1 (expired credential)", calls)
	}

	cred, ok, err := store.GetRaw("spy-oauth-fail")
	if err != nil || !ok {
		t.Fatalf("GetRaw = (ok=%v, err=%v); stored credential lost after refresh failure", ok, err)
	}
	if cred.Access != orig.Access || cred.Refresh != orig.Refresh || cred.Expires != orig.Expires {
		t.Fatalf("stored credential corrupted after refresh failure: got %+v, want %+v", cred, orig)
	}
}

// TestGetOAuthAPIKey_LiveCredentialSkipsRefresh locks the expiry guard: a
// credential that has not expired must be used as-is, without a refresh RPC.
// Refreshing a live token is wasted work and would misclassify a healthy
// credential as needing re-auth.
func TestGetOAuthAPIKey_LiveCredentialSkipsRefresh(t *testing.T) {
	calls := 0
	useOAuthProvider(t, spyOAuthProvider{
		id:           "spy-oauth-live",
		refreshCalls: &calls,
		refreshErr:   errors.New("RefreshToken must not be called for a live credential"),
	})

	live := OAuthCredentials{
		Access:  "live-access",
		Refresh: "live-refresh",
		Expires: time.Now().Add(time.Hour).UnixMilli(),
	}
	next, apiKey, err := GetOAuthAPIKey("spy-oauth-live", map[string]OAuthCredentials{"spy-oauth-live": live})
	if err != nil {
		t.Fatalf("GetOAuthAPIKey on live credential: %v", err)
	}
	if calls != 0 {
		t.Fatalf("RefreshToken called %d times on a live credential, want 0", calls)
	}
	if apiKey != "live-access" {
		t.Fatalf("apiKey = %q, want live-access", apiKey)
	}
	if next == nil || next.Access != "live-access" || next.Refresh != "live-refresh" {
		t.Fatalf("returned credential = %+v, want the live credential unchanged", next)
	}
}

func TestResolveStoredAPIKeyFromStorageContextCancellationPreservesCredential(t *testing.T) {
	legacyCalls, contextCalls := 0, 0
	useOAuthProvider(t, contextRefreshOAuthProvider{
		spyOAuthProvider: spyOAuthProvider{id: "stored-context-refresh", refreshCalls: &legacyCalls},
		contextCalls:     &contextCalls,
	})
	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := Credential{Type: CredentialOAuth, Refresh: "old-refresh", Access: "old-access", Expires: 1}
	if err := store.Set("stored-context-refresh", original); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key, ok, err := ResolveStoredAPIKeyFromStorageContext(ctx, store, "stored-context-refresh")
	if err == nil || ok || key != "" {
		t.Fatalf("ResolveStoredAPIKeyFromStorageContext = %q, %t, %v; want cancellation", key, ok, err)
	}
	if contextCalls != 1 || legacyCalls != 0 {
		t.Fatalf("refresh calls: contextual=%d legacy=%d, want 1 and 0", contextCalls, legacyCalls)
	}
	stored, found, err := store.GetRaw("stored-context-refresh")
	if err != nil || !found || stored.Type != original.Type || stored.Refresh != original.Refresh || stored.Access != original.Access || stored.Expires != original.Expires {
		t.Fatalf("stored credential = %#v, found=%v, err=%v; want %#v", stored, found, err, original)
	}
}

func TestResolveOAuthAPIKeyFromStorageContextCancellationPreservesCredential(t *testing.T) {
	legacyCalls, contextCalls := 0, 0
	useOAuthProvider(t, contextRefreshOAuthProvider{
		spyOAuthProvider: spyOAuthProvider{id: "context-refresh", refreshCalls: &legacyCalls},
		contextCalls:     &contextCalls,
	})
	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := Credential{Type: CredentialOAuth, Refresh: "old-refresh", Access: "old-access", Expires: 1}
	if err := store.Set("context-refresh", original); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if key, err := ResolveOAuthAPIKeyFromStorageContext(ctx, store, "context-refresh"); err == nil || key != "" {
		t.Fatalf("ResolveOAuthAPIKeyFromStorageContext = %q, %v; want cancellation", key, err)
	}
	if contextCalls != 1 || legacyCalls != 0 {
		t.Fatalf("refresh calls: contextual=%d legacy=%d, want 1 and 0", contextCalls, legacyCalls)
	}
	stored, ok, err := store.GetRaw("context-refresh")
	if err != nil || !ok || stored.Type != original.Type || stored.Refresh != original.Refresh || stored.Access != original.Access || stored.Expires != original.Expires {
		t.Fatalf("stored credential = %#v, ok=%v, err=%v; want %#v", stored, ok, err, original)
	}
}
