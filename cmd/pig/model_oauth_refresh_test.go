package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// failingRefreshOAuthProvider is an OAuth provider whose stored token is expired
// and whose refresh RPC always fails. It stands in for a real provider (anthropic,
// openai-codex) when the refresh network call errors.
type failingRefreshOAuthProvider struct{ id string }

func (p failingRefreshOAuthProvider) ID() string               { return p.id }
func (p failingRefreshOAuthProvider) Name() string             { return p.id }
func (p failingRefreshOAuthProvider) UsesCallbackServer() bool { return false }
func (p failingRefreshOAuthProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("login not supported in test")
}
func (p failingRefreshOAuthProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("token refresh request failed")
}
func (p failingRefreshOAuthProvider) RefreshTokenContext(ctx context.Context, credentials ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	if err := ctx.Err(); err != nil {
		return ai.OAuthCredentials{}, err
	}
	return p.RefreshToken(credentials)
}
func (p failingRefreshOAuthProvider) GetAPIKey(creds ai.OAuthCredentials) string { return creds.Access }
func (p failingRefreshOAuthProvider) OAuthCredentialStatus() (ai.OAuthCredentialStatus, bool) {
	return ai.OAuthCredentialStatus{}, false
}
func (p failingRefreshOAuthProvider) StoreOAuthCredentials(ai.OAuthCredentials) (string, error) {
	return "", errors.New("store not supported in test")
}
func (p failingRefreshOAuthProvider) DeleteOAuthCredentials() (bool, error) { return false, nil }

// A stored OAuth credential whose refresh genuinely fails must surface that
// failure to the caller, not silently degrade to an empty API key. Upstream
// resolves OAuth lazily and lets a refresh throw reach the user; pig resolves
// eagerly in buildModel, so the specific refresh failure must be preserved.
func TestBuildModel_OAuthRefreshFailureSurfacesWhenNoFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		spec     string
		envKey   string
	}{
		{"anthropic", "anthropic", "anthropic/claude-sonnet-4-20250514", "ANTHROPIC_API_KEY"},
		{"openai-codex", "openai-codex", "openai-codex/gpt-5-codex", "OPENAI_API_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			agentDirForModelOverride = dir
			t.Cleanup(func() { agentDirForModelOverride = "" })
			// Guarantee no env-key fallback masks the refresh failure.
			t.Setenv(tc.envKey, "")

			auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
			if err != nil {
				t.Fatalf("NewAuthStorage: %v", err)
			}
			if err := auth.Set(tc.provider, ai.Credential{
				Type:    ai.CredentialOAuth,
				Refresh: "stale-refresh",
				Access:  "stale-access",
				Expires: time.Now().UnixMilli() - 60_000, // expired → forces refresh
			}); err != nil {
				t.Fatalf("auth.Set: %v", err)
			}

			ai.RegisterOAuthProvider(tc.provider, failingRefreshOAuthProvider{id: tc.provider})
			t.Cleanup(func() { ai.UnregisterOAuthProvider(tc.provider) })

			registry := codingagent.NewModelRegistry(t.TempDir())
			_, _, _, err = buildModel(tc.spec, registry)
			if err == nil {
				t.Fatalf("buildModel(%q) returned nil error; a genuine OAuth refresh failure was swallowed", tc.spec)
			}
			if !strings.Contains(err.Error(), "refresh") {
				t.Fatalf("buildModel error %q does not surface the refresh failure", err)
			}
		})
	}
}

// A stored credential owns the provider in upstream resolveProviderAuth, so a
// failed refresh must surface even when ambient auth is configured. Falling
// back would silently switch identities after a credential failure.
func TestBuildModel_StoredOAuthRefreshFailureBlocksEnvFallback(t *testing.T) {
	dir := t.TempDir()
	agentDirForModelOverride = dir
	t.Cleanup(func() { agentDirForModelOverride = "" })
	t.Setenv("ANTHROPIC_API_KEY", "sk-env-fallback")

	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("NewAuthStorage: %v", err)
	}
	if err := auth.Set("anthropic", ai.Credential{
		Type:    ai.CredentialOAuth,
		Refresh: "stale-refresh",
		Access:  "stale-access",
		Expires: time.Now().UnixMilli() - 60_000,
	}); err != nil {
		t.Fatalf("auth.Set: %v", err)
	}
	ai.RegisterOAuthProvider("anthropic", failingRefreshOAuthProvider{id: "anthropic"})
	t.Cleanup(func() { ai.UnregisterOAuthProvider("anthropic") })

	registry := codingagent.NewModelRegistry(t.TempDir())
	_, _, _, err = buildModel("anthropic/claude-sonnet-4-20250514", registry)
	if err == nil || !strings.Contains(err.Error(), "refresh") {
		t.Fatalf("buildModel error = %v, want stored credential refresh failure", err)
	}
}

func TestBuildModelContextCancelsOAuthRefresh(t *testing.T) {
	dir := t.TempDir()
	agentDirForModelOverride = dir
	t.Cleanup(func() { agentDirForModelOverride = "" })
	t.Setenv("ANTHROPIC_API_KEY", "")
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("anthropic", ai.Credential{Type: ai.CredentialOAuth, Refresh: "stale", Access: "old", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	ai.RegisterOAuthProvider("anthropic", failingRefreshOAuthProvider{id: "anthropic"})
	t.Cleanup(func() { ai.UnregisterOAuthProvider("anthropic") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err = buildModelContext(ctx, "anthropic/claude-sonnet-4-20250514", codingagent.NewModelRegistry(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("buildModelContext error = %v, want cancellation", err)
	}
	stored, ok, err := auth.GetRaw("anthropic")
	if err != nil || !ok || stored.Access != "old" || stored.Refresh != "stale" {
		t.Fatalf("stored credential = %#v, ok=%v, err=%v; cancellation must not write", stored, ok, err)
	}
}
