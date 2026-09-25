package main

// Ports upstream packages/coding-agent/test/auth-check.test.ts.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// isolateAuthEnv clears the ambient credentials the built-in providers read so
// a developer's environment cannot satisfy a check.
func isolateAuthEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"OPENAI_API_KEY", "KIMI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "COPILOT_GITHUB_TOKEN", "MISSING_AUTH_CHECK_KEY"} {
		t.Setenv(name, "")
	}
}

func createAuthTestRuntime(t *testing.T, credentials ai.CredentialStore, refreshOnCreate bool) *codingagent.RequestAuthRuntime {
	t.Helper()
	runtime, err := codingagent.NewRequestAuthRuntime(context.Background(), codingagent.RequestAuthRuntimeOptions{
		Credentials:     credentials,
		RefreshOnCreate: refreshOnCreate,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func checkAuthArgs(t *testing.T, runtime *codingagent.RequestAuthRuntime, refresh bool, args ...string) AuthCheckResult {
	t.Helper()
	result, err := CheckProviderAuth(context.Background(), parseFlags(args), args, runtime, refresh)
	if err != nil {
		t.Fatalf("CheckProviderAuth(%v) error: %v", args, err)
	}
	return result
}

func replaceOAuthRefresh(t *testing.T, runtime *codingagent.RequestAuthRuntime, providerID string, refresh func(context.Context, ai.Credential) (ai.Credential, error)) {
	t.Helper()
	provider := runtime.GetProvider(providerID)
	if provider == nil || provider.Auth.OAuth == nil {
		t.Fatalf("%s OAuth provider is not registered", providerID)
	}
	provider.Auth.OAuth.Refresh = refresh
}

func TestAuthCheckReportsConfiguredProviderAsReady(t *testing.T) {
	isolateAuthEnv(t)
	runtime := createAuthTestRuntime(t, ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai": {Type: ai.CredentialAPIKey, Key: "test-key"}}), false)
	got := checkAuthArgs(t, runtime, false, "--provider", "openai")
	want := AuthCheckResult{Status: AuthCheckReady, Provider: "openai", AuthType: ai.CredentialAPIKey}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAuthCheckResolvesProviderFromModel(t *testing.T) {
	isolateAuthEnv(t)
	runtime := createAuthTestRuntime(t, ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai": {Type: ai.CredentialAPIKey, Key: "test-key"}}), false)
	got := checkAuthArgs(t, runtime, false, "--model", "openai/gpt-5.5")
	want := AuthCheckResult{Status: AuthCheckReady, Provider: "openai", AuthType: ai.CredentialAPIKey}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got = checkAuthArgs(t, runtime, false, "--provider", "openai", "--model", "gpt-5.5")
	if got.Status != AuthCheckReady || got.Provider != "openai" {
		t.Fatalf("got %+v, want ready openai", got)
	}
}

func TestAuthCheckReadsCredentialsWithoutRefreshingOAuthWhenRequested(t *testing.T) {
	isolateAuthEnv(t)
	ctx := context.Background()
	apiCredentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai": {Type: ai.CredentialAPIKey, Key: "test-key"}})
	apiRuntime := createAuthTestRuntime(t, apiCredentials, false)
	if value, err := GetProviderCredential(ctx, "openai", apiRuntime, apiCredentials, false); err != nil || value != "test-key" {
		t.Fatalf("api credential = %q, %v", value, err)
	}

	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai-codex": {Type: ai.CredentialOAuth, Access: "old-token", Refresh: "refresh-token", Expires: 0}})
	oauthRuntime := createAuthTestRuntime(t, credentials, false)
	var refreshes atomic.Int32
	replaceOAuthRefresh(t, oauthRuntime, "openai-codex", func(context.Context, ai.Credential) (ai.Credential, error) {
		refreshes.Add(1)
		return ai.Credential{}, errors.New("unexpected refresh")
	})
	if value, err := GetProviderCredential(ctx, "openai-codex", oauthRuntime, credentials, false); err != nil || value != "old-token" {
		t.Fatalf("oauth credential = %q, %v", value, err)
	}
	if refreshes.Load() != 0 {
		t.Fatalf("refresh called %d times", refreshes.Load())
	}
}

func TestAuthCheckRefreshesOAuthByDefault(t *testing.T) {
	isolateAuthEnv(t)
	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai-codex": {Type: ai.CredentialOAuth, Access: "old-token", Refresh: "refresh-token", Expires: 0}})
	runtime := createAuthTestRuntime(t, credentials, false)
	var refreshes atomic.Int32
	replaceOAuthRefresh(t, runtime, "openai-codex", func(context.Context, ai.Credential) (ai.Credential, error) {
		refreshes.Add(1)
		return ai.Credential{Type: ai.CredentialOAuth, Access: "fresh-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
	})
	if got := checkAuthArgs(t, runtime, true, "--provider", "openai-codex"); got.Status != AuthCheckReady {
		t.Fatalf("got %+v, want ready", got)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh called %d times, want once", refreshes.Load())
	}
}

func TestAuthCheckReportsUnknownProviderAsNotReady(t *testing.T) {
	isolateAuthEnv(t)
	runtime := createAuthTestRuntime(t, ai.NewInMemoryAuthStorage(nil), false)
	got := checkAuthArgs(t, runtime, false, "--provider", "not-installed")
	want := AuthCheckResult{Status: AuthCheckNotReady, Provider: "not-installed", Reason: AuthCheckProviderNotFound}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAuthCheckDoesNotTreatUnresolvedStoredEnvironmentReferenceAsConfigured(t *testing.T) {
	isolateAuthEnv(t)
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"openai":{"type":"api_key","key":"$MISSING_AUTH_CHECK_KEY"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := createAuthTestRuntime(t, ai.NewReadOnlyAuthStorage(authPath), false)
	got := checkAuthArgs(t, runtime, false, "--provider", "openai")
	want := AuthCheckResult{Status: AuthCheckNotReady, Provider: "openai", Reason: AuthCheckCredentialsNotConfigured}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAuthCheckReportsMalformedAuthStateAsInvalid(t *testing.T) {
	isolateAuthEnv(t)
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte("{invalid-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := createAuthTestRuntime(t, ai.NewReadOnlyAuthStorage(authPath), false)
	got := checkAuthArgs(t, runtime, false, "--provider", "openai")
	want := AuthCheckResult{Status: AuthCheckInvalid, Provider: "openai", Reason: AuthCheckInvalidState}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAuthCheckDoesNotCreateAuthFileOrParentDirectory(t *testing.T) {
	isolateAuthEnv(t)
	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "agent", "auth.json")
	runtime := createAuthTestRuntime(t, ai.NewReadOnlyAuthStorage(authPath), false)
	got := checkAuthArgs(t, runtime, false, "--provider", "openai")
	if got.Status != AuthCheckNotReady || got.Reason != AuthCheckCredentialsNotConfigured {
		t.Fatalf("got %+v", got)
	}
	if _, err := os.Stat(authPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("auth file exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "agent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("agent dir exists: %v", err)
	}
}

func TestAuthCheckAcceptsJSONCredentialsAndNoRefresh(t *testing.T) {
	got, err := ParseAuthCommand([]string{"auth", "check", "--provider", "openai"})
	if err != nil {
		t.Fatal(err)
	}
	want := &AuthCommand{Kind: AuthCommandCheck, Args: []string{"--provider", "openai"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, err = ParseAuthCommand([]string{"auth", "check", "--json", "--credentials", "--no-refresh", "--provider", "openai"})
	if err != nil {
		t.Fatal(err)
	}
	want = &AuthCommand{Kind: AuthCommandCheck, Args: []string{"--provider", "openai"}, JSON: true, Credentials: true, NoRefresh: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAuthCheckRuntimeHasNoCatalogStorage(t *testing.T) {
	runtime, err := CreateAuthCheckModelRuntime(context.Background(), ai.NewInMemoryAuthStorage(nil), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GetProvider("openai") == nil {
		t.Fatal("openai provider missing")
	}
}

func TestAuthCheckCredentialReadRefreshesExpiredOAuth(t *testing.T) {
	isolateAuthEnv(t)
	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai-codex": {Type: ai.CredentialOAuth, Access: "old-token", Refresh: "refresh-token", Expires: 0}})
	runtime := createAuthTestRuntime(t, credentials, false)
	replaceOAuthRefresh(t, runtime, "openai-codex", func(context.Context, ai.Credential) (ai.Credential, error) {
		return ai.Credential{Type: ai.CredentialOAuth, Access: "fresh-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
	})
	value, err := GetProviderCredential(context.Background(), "openai-codex", runtime, credentials, true)
	if err != nil || value != "fresh-token" {
		t.Fatalf("credential = %q, %v; want the refreshed token", value, err)
	}
}
