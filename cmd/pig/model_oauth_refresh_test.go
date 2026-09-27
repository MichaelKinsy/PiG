package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// failingRefreshOAuthProvider rejects refresh by default and allows tests to control refresh completion and count auth derivations.
type failingRefreshOAuthProvider struct {
	id       string
	refresh  func(context.Context) (ai.OAuthCredentials, error)
	keyCalls *int
}

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
	if p.refresh != nil {
		return p.refresh(ctx)
	}
	if err := ctx.Err(); err != nil {
		return ai.OAuthCredentials{}, err
	}
	return p.RefreshToken(credentials)
}
func (p failingRefreshOAuthProvider) GetAPIKey(creds ai.OAuthCredentials) string {
	if p.keyCalls != nil {
		(*p.keyCalls)++
	}
	return creds.Access
}
func (p failingRefreshOAuthProvider) OAuthCredentialStatus() (ai.OAuthCredentialStatus, bool) {
	return ai.OAuthCredentialStatus{}, false
}
func (p failingRefreshOAuthProvider) StoreOAuthCredentials(ai.OAuthCredentials) (string, error) {
	return "", errors.New("store not supported in test")
}
func (p failingRefreshOAuthProvider) DeleteOAuthCredentials() (bool, error) { return false, nil }

// Pi 0.87.1 packages/coding-agent/src/core/model-resolver.ts:419 selects a model without resolving auth. packages/ai/src/models.ts:657 and auth/resolve.ts:87 resolve stored OAuth on the request and propagate refresh failures.
func TestBuildModel_OAuthRefreshFailureSurfacesWhenNoFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		spec     string
		envKey   string
	}{
		{"anthropic", "anthropic", "anthropic/claude-sonnet-4-20250514", "ANTHROPIC_API_KEY"},
		{"openai-completions", "openai", "openai/gpt-4o-mini", "OPENAI_API_KEY"},
		{"openai-responses", "openai", "openai/gpt-5.5", "OPENAI_API_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			agentDirForModelOverride = dir
			t.Cleanup(func() { agentDirForModelOverride = "" })
			// Guarantee no env-key fallback masks the refresh failure.
			for _, name := range []string{tc.envKey, "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN"} {
				t.Setenv(name, "")
			}
			if tc.name == "openai-completions" {
				config := `{"providers":{"openai":{"models":[{"id":"gpt-4o-mini","api":"openai-completions"}]}}}`
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
			}

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

			model, _, _, err := buildModel(tc.spec, testServices(t, dir))
			if err != nil {
				t.Fatalf("buildModel(%q) resolved OAuth before a request: %v", tc.spec, err)
			}
			_, err = model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), ai.StreamOptions{})
			if err == nil || !strings.Contains(err.Error(), "token refresh request failed") {
				t.Fatalf("first request error = %v, want stored credential refresh failure", err)
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
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")

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

	model, _, _, err := buildModel("anthropic/claude-sonnet-4-20250514", testServices(t, dir))
	if err != nil {
		t.Fatalf("buildModel resolved OAuth before a request: %v", err)
	}
	_, err = model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), ai.StreamOptions{})
	if err == nil || !strings.Contains(err.Error(), "token refresh request failed") {
		t.Fatalf("first request error = %v, want stored credential refresh failure, not env fallback", err)
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
	started := make(chan context.Context, 1)
	ai.RegisterOAuthProvider("anthropic", failingRefreshOAuthProvider{
		id: "anthropic",
		refresh: func(ctx context.Context) (ai.OAuthCredentials, error) {
			started <- ctx
			<-ctx.Done()
			return ai.OAuthCredentials{}, ctx.Err()
		},
	})
	t.Cleanup(func() { ai.UnregisterOAuthProvider("anthropic") })
	// Startup's lifetime must not be captured by the per-request callback.
	buildCtx, cancelBuild := context.WithCancel(t.Context())
	cancelBuild()
	model, _, _, err := buildModelContext(buildCtx, "anthropic/claude-sonnet-4-20250514", testServices(t, dir))
	if err != nil {
		t.Fatalf("buildModelContext resolved OAuth before a request: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, streamErr := model.Provider.Stream(ctx, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), ai.StreamOptions{})
		result <- streamErr
	}()
	select {
	case refreshCtx := <-started:
		if refreshCtx != ctx {
			t.Error("refresh did not receive the request context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start OAuth refresh")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("request error = %v, want cancellation", err)
	}
	stored, ok, err := auth.GetRaw("anthropic")
	if err != nil || !ok || stored.Access != "old" || stored.Refresh != "stale" {
		t.Fatalf("stored credential = %#v, ok=%v, err=%v; cancellation must not write", stored, ok, err)
	}
}
