package cli

// pi: packages/coding-agent/src/cli/auth-check.ts

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

type authCheckCtxKey struct{}

func expiredCodexStore() ai.CredentialStore {
	return ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai-codex": {Type: ai.CredentialOAuth, Access: "old-token", Refresh: "refresh-token", Expires: 0}})
}

// Pi :46 getAuth runs only when options.refresh is set: a plain check of an
// expired OAuth credential never refreshes.
func TestAuthCheckWithoutRefreshNeverResolvesAuth(t *testing.T) {
	isolateAuthEnv(t)
	runtime := createAuthTestRuntime(t, expiredCodexStore(), false)
	var refreshes atomic.Int32
	replaceOAuthRefresh(t, runtime, "openai-codex", func(context.Context, ai.Credential) (ai.Credential, error) {
		refreshes.Add(1)
		return ai.Credential{}, errors.New("unexpected refresh")
	})
	got := checkAuthArgs(t, runtime, false, "--provider", "openai-codex")
	if got.Status != AuthCheckReady || got.AuthType != ai.CredentialOAuth {
		t.Fatalf("got %+v, want ready oauth", got)
	}
	if refreshes.Load() != 0 {
		t.Fatalf("refresh called %d times without refresh=true", refreshes.Load())
	}
}

// Pi :46-56: a rejection of getAuth becomes status invalid, never a thrown
// error, and the caller context reaches the refresh.
func TestAuthCheckRefreshFailureBecomesInvalidAndCarriesCallerContext(t *testing.T) {
	isolateAuthEnv(t)
	runtime := createAuthTestRuntime(t, expiredCodexStore(), false)
	var sawCaller atomic.Bool
	replaceOAuthRefresh(t, runtime, "openai-codex", func(ctx context.Context, _ ai.Credential) (ai.Credential, error) {
		sawCaller.Store(ctx.Value(authCheckCtxKey{}) == "caller")
		return ai.Credential{}, errors.New("refresh rejected")
	})
	ctx := context.WithValue(context.Background(), authCheckCtxKey{}, "caller")
	args := []string{"--provider", "openai-codex"}
	got, err := CheckProviderAuth(ctx, parseArgs(args), args, runtime, true)
	if err != nil {
		t.Fatalf("CheckProviderAuth returned %v; Pi folds the rejection into the result", err)
	}
	want := AuthCheckResult{Status: AuthCheckInvalid, Provider: "openai-codex", Reason: AuthCheckInvalidState}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if !sawCaller.Load() {
		t.Fatal("refresh did not receive the caller context")
	}
}

// Pi :63 getProviderCredential awaits credentials.read and then getAuth for
// the same call: the refresh runs once, under the caller context, and its
// token is the result.
func TestAuthCheckCredentialReadRefreshesUnderCallerContext(t *testing.T) {
	isolateAuthEnv(t)
	credentials := expiredCodexStore()
	runtime := createAuthTestRuntime(t, credentials, false)
	var sawCaller atomic.Bool
	replaceOAuthRefresh(t, runtime, "openai-codex", func(ctx context.Context, _ ai.Credential) (ai.Credential, error) {
		sawCaller.Store(ctx.Value(authCheckCtxKey{}) == "caller")
		return ai.Credential{Type: ai.CredentialOAuth, Access: "fresh-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
	})
	ctx := context.WithValue(context.Background(), authCheckCtxKey{}, "caller")
	value, err := GetProviderCredential(ctx, "openai-codex", runtime, credentials, true)
	if err != nil || value != "fresh-token" {
		t.Fatalf("credential = %q, %v; want the refreshed token", value, err)
	}
	if !sawCaller.Load() {
		t.Fatal("refresh did not receive the caller context")
	}
}
