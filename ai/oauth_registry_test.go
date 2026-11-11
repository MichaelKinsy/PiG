package ai

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeOAuthProvider struct{}

func (fakeOAuthProvider) ID() string               { return "fake-oauth" }
func (fakeOAuthProvider) Name() string             { return "Fake OAuth" }
func (fakeOAuthProvider) UsesCallbackServer() bool { return false }
func (fakeOAuthProvider) Login(OAuthLoginCallbacks) (OAuthCredentials, error) {
	return OAuthCredentials{}, nil
}
func (fakeOAuthProvider) RefreshToken(OAuthCredentials) (OAuthCredentials, error) {
	return OAuthCredentials{Refresh: "new-refresh", Access: "new-access", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}
func (fakeOAuthProvider) GetAPIKey(creds OAuthCredentials) string { return creds.Access }

func TestGetOAuthAPIKey_AnthropicPassThrough(t *testing.T) {
	next, apiKey, err := GetOAuthAPIKey("anthropic", map[string]OAuthCredentials{
		"anthropic": {Access: "access-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	if err != nil {
		t.Fatalf("GetOAuthAPIKey: %v", err)
	}
	if next == nil {
		t.Fatal("next = nil")
	}
	if apiKey != "access-token" {
		t.Fatalf("apiKey = %q, want access-token", apiKey)
	}
}

func TestGetOAuthProvider_GoogleProvidersRemoved(t *testing.T) {
	for _, id := range []string{"google-gemini-cli", "google-antigravity"} {
		if _, ok := GetOAuthProvider(id); ok {
			t.Fatalf("GetOAuthProvider(%q) unexpectedly found removed provider", id)
		}
	}
}

func TestBuiltInOAuthProvidersExposeContextOperations(t *testing.T) {
	type contextLogin interface {
		LoginContext(context.Context, OAuthLoginCallbacks) (OAuthCredentials, error)
	}
	for id, provider := range builtInOAuthProviders {
		if _, ok := provider.(contextLogin); !ok {
			t.Errorf("%s does not expose LoginContext", id)
		}
		if _, ok := provider.(oauthContextRefresh); !ok {
			t.Errorf("%s does not expose RefreshTokenContext", id)
		}
	}
}

func TestResolveOAuthAPIKeyFromStorage_PersistsRefresh(t *testing.T) {
	old := builtInOAuthProviders["fake-oauth"]
	builtInOAuthProviders["fake-oauth"] = fakeOAuthProvider{}
	defer func() {
		if old != nil {
			builtInOAuthProviders["fake-oauth"] = old
		} else {
			delete(builtInOAuthProviders, "fake-oauth")
		}
	}()

	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("fake-oauth", Credential{
		Type:    CredentialOAuth,
		Refresh: "old-refresh",
		Access:  "old-access",
		Expires: time.Now().Add(-time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	apiKey, err := ResolveOAuthAPIKeyFromStorage(store, "fake-oauth")
	if err != nil {
		t.Fatalf("ResolveOAuthAPIKeyFromStorage: %v", err)
	}
	if apiKey != "new-access" {
		t.Fatalf("apiKey = %q, want new-access", apiKey)
	}
	cred, ok, err := store.GetRaw("fake-oauth")
	if err != nil || !ok {
		t.Fatalf("GetRaw = (%v,%v)", ok, err)
	}
	if cred.Access != "new-access" || cred.Refresh != "new-refresh" {
		t.Fatalf("stored cred = %+v", cred)
	}
}

func TestResolveOAuthAPIKeyFromStorage_MissingProviderReturnsEmpty(t *testing.T) {
	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	apiKey, err := ResolveOAuthAPIKeyFromStorage(store, "anthropic")
	if err != nil {
		t.Fatalf("ResolveOAuthAPIKeyFromStorage: %v", err)
	}
	if apiKey != "" {
		t.Fatalf("apiKey = %q, want empty", apiKey)
	}
}

func TestResolveStoredAPIKeyFromStorageResolvesAPIKeyCredential(t *testing.T) {
	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("anthropic", Credential{Type: CredentialAPIKey, Key: "$ANTHROPIC_STORED_KEY", Env: map[string]string{"ANTHROPIC_STORED_KEY": "stored-key"}}); err != nil {
		t.Fatal(err)
	}
	key, ok, err := ResolveStoredAPIKeyFromStorage(store, "anthropic")
	if err != nil || !ok || key != "stored-key" {
		t.Fatalf("ResolveStoredAPIKeyFromStorage = (%q, %t, %v)", key, ok, err)
	}
}

func TestAuthStorage_ProjectIDRoundTrip(t *testing.T) {
	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := Credential{Type: CredentialOAuth, Refresh: "r", Access: "a", Expires: 123, ProjectID: "proj-123"}
	if err := store.Set("custom-provider", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetRaw("custom-provider")
	if err != nil || !ok {
		t.Fatalf("GetRaw = (%v,%v)", ok, err)
	}
	if got.ProjectID != want.ProjectID {
		t.Fatalf("ProjectID = %q, want %q", got.ProjectID, want.ProjectID)
	}
}

func TestCodexOAuthProvider_GetAPIKeyAccessToken(t *testing.T) {
	p, ok := GetOAuthProvider("openai-codex")
	if !ok {
		t.Fatal("missing openai-codex provider")
	}
	got := p.GetAPIKey(OAuthCredentials{Access: "access.jwt"})
	if got != "access.jwt" {
		t.Fatalf("GetAPIKey = %q", got)
	}
}

func TestOAuthCredentialJSON_ProjectIDKey(t *testing.T) {
	b, err := json.Marshal(OAuthCredentials{Access: "a", Refresh: "r", Expires: 1, ProjectID: "proj"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"projectId":"proj"`) {
		t.Fatalf("json = %s", b)
	}
}
