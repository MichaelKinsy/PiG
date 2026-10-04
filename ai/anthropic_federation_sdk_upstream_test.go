package ai

// Ports .upstream/v0.99.2/packages/ai/test/anthropic-federation-sdk.test.ts (the real SDK against a fake fetch:
// how often the workload identity federation token exchange happens, #10177) and pins the Anthropic SDK
// behaviors pi inherits from @anthropic-ai/sdk@0.129.0 (lib/credentials/*.mjs, client.mjs) for a federation
// config: exchange errors, redaction, the 401 reactive refresh and the client cache keyed by config and fetch.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func federationFetch() *http.Client {
	return &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
}

// anthropic-federation-sdk.test.ts:93-108
func TestAnthropicFederationExchangesOnceAcrossRequests(t *testing.T) {
	env := federationEnvFor(t)
	wire := &federationWire{}
	baseURL := wire.serve(t)
	fetch := federationFetch()
	for range 3 {
		message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
		if message.StopReason != StopReasonStop {
			t.Fatalf("stop reason = %q (%s)", message.StopReason, message.ErrorMessage)
		}
	}
	if got := len(wire.byPath("/v1/oauth/token")); got != 1 {
		t.Fatalf("token exchanges = %d, want 1", got)
	}
	messages := wire.byPath("/v1/messages")
	if len(messages) != 3 {
		t.Fatalf("message requests = %d, want 3", len(messages))
	}
	for _, request := range messages {
		if got := request.header.Get("Authorization"); got != "Bearer federated-token" {
			t.Errorf("Authorization = %q", got)
		}
	}
}

// anthropic-federation-sdk.test.ts:110-119: the SDK's own credential chain (ANTHROPIC_PROFILE config files and
// the federation environment) never runs behind pi's auth resolver.
func TestAnthropicFederationSkippedForHeaderOwnedAuth(t *testing.T) {
	env := federationEnvFor(t)
	for name, value := range env {
		t.Setenv(name, value)
	}
	wire := &federationWire{}
	message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: wire.serve(t)}, StreamOptions{Headers: ProviderHeaders{"Authorization": new("Bearer auth-token")}})
	if message.StopReason != StopReasonStop {
		t.Fatalf("stop reason = %q (%s)", message.StopReason, message.ErrorMessage)
	}
	requests := wire.requests
	if len(requests) != 1 || requests[0].path != "/v1/messages" || requests[0].header.Get("Authorization") != "Bearer auth-token" {
		t.Fatalf("requests = %+v, want one /v1/messages with the header-owned bearer token", requests)
	}
}

// anthropic-messages.ts:1040-1053: one client is kept for the current federation config and fetch; a different
// config, base URL or fetch builds a client with its own token cache.
func TestAnthropicFederationClientCache(t *testing.T) {
	t.Run("a different fetch exchanges again", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{}
		baseURL := wire.serve(t)
		for _, fetch := range []*http.Client{federationFetch(), federationFetch(), nil} {
			streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
		}
		if got := len(wire.byPath("/v1/oauth/token")); got != 3 {
			t.Fatalf("token exchanges = %d, want 3", got)
		}
	})
	t.Run("a changed federation config exchanges again", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{}
		baseURL := wire.serve(t)
		fetch := federationFetch()
		streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
		changed := withoutEnv(env)
		changed[AnthropicWorkspaceIDEnv] = "wrkspc_other"
		streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: changed, Fetch: fetch})
		streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: changed, Fetch: fetch})
		exchanges := wire.byPath("/v1/oauth/token")
		if len(exchanges) != 2 || !strings.Contains(exchanges[1].body, `"workspace_id":"wrkspc_other"`) {
			t.Fatalf("exchanges = %+v, want one per config", exchanges)
		}
	})
	t.Run("the identity token file is read on every exchange", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{token: func(n int, w http.ResponseWriter) {
			_, _ = fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":5}`, n)
		}}
		baseURL := wire.serve(t)
		fetch := federationFetch()
		streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
		if err := os.WriteFile(env[AnthropicIdentityTokenFileEnv], []byte("  rotated.jwt.value \n\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
		exchanges := wire.byPath("/v1/oauth/token")
		if len(exchanges) != 2 || !strings.Contains(exchanges[0].body, `"assertion":"header.payload.signature"`) || !strings.Contains(exchanges[1].body, `"assertion":"rotated.jwt.value"`) {
			t.Fatalf("exchanges = %+v", exchanges)
		}
		messages := wire.byPath("/v1/messages")
		if len(messages) != 2 || messages[0].header.Get("Authorization") != "Bearer token-0" || messages[1].header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("messages = %+v", messages)
		}
	})
}

// client.mjs:669-681 (shouldRetry) and token-cache.mjs:54-63: a 401 on a request that used the federated token
// invalidates the cache, so the next request exchanges again.
func TestAnthropicFederationRefreshesAfter401(t *testing.T) {
	env := federationEnvFor(t)
	wire := &federationWire{
		token: func(n int, w http.ResponseWriter) {
			_, _ = fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":3600}`, n)
		},
		messageStatus: func(n int) int { return map[int]int{0: http.StatusUnauthorized}[n] },
	}
	baseURL := wire.serve(t)
	fetch := federationFetch()
	first := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
	if first.StopReason != StopReasonError {
		t.Fatalf("first stop reason = %q, want error", first.StopReason)
	}
	second := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
	if second.StopReason != StopReasonStop {
		t.Fatalf("second stop reason = %q (%s)", second.StopReason, second.ErrorMessage)
	}
	third := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
	if third.StopReason != StopReasonStop {
		t.Fatalf("third stop reason = %q (%s)", third.StopReason, third.ErrorMessage)
	}
	messages := wire.byPath("/v1/messages")
	if got := len(wire.byPath("/v1/oauth/token")); got != 2 {
		t.Fatalf("token exchanges = %d, want 2 (initial, then after the 401)", got)
	}
	if len(messages) != 3 || messages[0].header.Get("Authorization") != "Bearer token-0" || messages[1].header.Get("Authorization") != "Bearer token-1" || messages[2].header.Get("Authorization") != "Bearer token-1" {
		t.Fatalf("messages = %+v", messages)
	}
}

// token-cache.mjs:39-44: a token with under 30 seconds left is refreshed before the request.
func TestAnthropicFederationRefreshesExpiringToken(t *testing.T) {
	env := federationEnvFor(t)
	wire := &federationWire{token: func(n int, w http.ResponseWriter) {
		_, _ = fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":10}`, n)
	}}
	baseURL := wire.serve(t)
	fetch := federationFetch()
	for range 2 {
		streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env, Fetch: fetch})
	}
	messages := wire.byPath("/v1/messages")
	if got := len(wire.byPath("/v1/oauth/token")); got != 2 || len(messages) != 2 || messages[1].header.Get("Authorization") != "Bearer token-1" {
		t.Fatalf("exchanges = %d, messages = %+v", got, messages)
	}
}

// oidc-federation.mjs:19-78, types.mjs:40-108: exchange failures surface as the stream error, and no message
// request is sent.
func TestAnthropicFederationExchangeErrors(t *testing.T) {
	workspaceHint := "If your federation rule is scoped to multiple workspaces, set the ANTHROPIC_WORKSPACE_ID environment variable, the 'workspace_id' config key, or the `workspaceId` option. "
	for _, tc := range []struct {
		name   string
		token  func(n int, w http.ResponseWriter)
		mutate func(t *testing.T, env ProviderEnv, baseURL *string)
		want   string
	}{
		{
			name: "401 redacts the body and adds the federation hint",
			token: func(_ int, w http.ResponseWriter) {
				w.Header().Set("Request-Id", "req_123")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprint(w, `{"error":"invalid_grant","error_description":"no rule","assertion":"secret.jwt","extra":1,"error_uri":"https://e.test"}`)
			},
			want: `Token exchange failed with status 401 (request-id req_123): {"error":"invalid_grant","error_description":"no rule","error_uri":"https://e.test"} Ensure your federation rule matches your identity token. ` + workspaceHint + `View your authentication events in the Workload identity page of Claude Console for more details.`,
		},
		{
			name: "401 with a workspace omits the workspace hint",
			token: func(_ int, w http.ResponseWriter) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
			},
			mutate: func(_ *testing.T, env ProviderEnv, _ *string) { env[AnthropicWorkspaceIDEnv] = "wrkspc_test" },
			want:   `Token exchange failed with status 401: {"error":"invalid_grant"} Ensure your federation rule matches your identity token. View your authentication events in the Workload identity page of Claude Console for more details.`,
		},
		{
			name: "a plain-text error body is kept",
			token: func(_ int, w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = fmt.Fprint(w, `upstream down`)
			},
			want: `Token exchange failed with status 502: upstream down`,
		},
		{
			name: "a long plain-text error body is truncated",
			token: func(_ int, w http.ResponseWriter) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, strings.Repeat("x", 2005))
			},
			want: `Token exchange failed with status 500: ` + strings.Repeat("x", 2000) + `... <5 more chars>`,
		},
		{
			name:  "a non-JSON success body",
			token: func(_ int, w http.ResponseWriter) { _, _ = fmt.Fprint(w, `not json`) },
			want:  `Token endpoint returned non-JSON response (status 200)`,
		},
		{
			name:  "a response without an access token",
			token: func(_ int, w http.ResponseWriter) { _, _ = fmt.Fprint(w, `{"expires_in":3600,"refresh_token":"r"}`) },
			want:  `Token endpoint response missing access_token: {}`,
		},
		{
			name: "an unsupported token type",
			token: func(_ int, w http.ResponseWriter) {
				_, _ = fmt.Fprint(w, `{"access_token":"a","token_type":"mac","expires_in":3600}`)
			},
			want: `Token endpoint response: unsupported token_type "mac" (want Bearer)`,
		},
		{
			name:  "a response without a finite expires_in",
			token: func(_ int, w http.ResponseWriter) { _, _ = fmt.Fprint(w, `{"access_token":"a"}`) },
			want:  `Token endpoint response missing required fields: {}`,
		},
		{
			name: "a missing identity token file",
			mutate: func(t *testing.T, env ProviderEnv, _ *string) {
				env[AnthropicIdentityTokenFileEnv] = filepath.Join(t.TempDir(), "absent.jwt")
			},
			// identity-token.mjs:14-16 interpolates Node's fs error: "Error: ENOENT: <description>, open '<path>'".
			want: `Failed to read identity token file at {file}: Error: ENOENT: no such file or directory, open '{file}'`,
		},
		{
			name: "an empty identity token file",
			mutate: func(t *testing.T, env ProviderEnv, _ *string) {
				path := filepath.Join(t.TempDir(), "empty.jwt")
				if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
					t.Fatal(err)
				}
				env[AnthropicIdentityTokenFileEnv] = path
			},
			want: `Identity token file at {file} is empty`,
		},
		{
			name: "an identity token over 16 KiB",
			mutate: func(t *testing.T, env ProviderEnv, _ *string) {
				if err := os.WriteFile(env[AnthropicIdentityTokenFileEnv], []byte(strings.Repeat("a", 16*1024+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: `Identity token is 17 KiB, exceeds the 16 KiB assertion limit`,
		},
		{
			name:   "a cleartext non-loopback token endpoint",
			mutate: func(_ *testing.T, _ ProviderEnv, baseURL *string) { *baseURL = "http://gateway.example.test" },
			want:   `Refusing to send credential over non-https token endpoint "http://gateway.example.test"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := federationEnvFor(t)
			wire := &federationWire{token: tc.token}
			baseURL := wire.serve(t)
			if tc.mutate != nil {
				tc.mutate(t, env, &baseURL)
			}
			message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env})
			want := strings.ReplaceAll(tc.want, "{file}", env[AnthropicIdentityTokenFileEnv])
			if message.StopReason != StopReasonError || message.ErrorMessage != want {
				t.Fatalf("stop reason = %q, error = %q\nwant %q", message.StopReason, message.ErrorMessage, want)
			}
			if got := len(wire.byPath("/v1/messages")); got != 0 {
				t.Fatalf("message requests = %d after a failed exchange, want 0", got)
			}
		})
	}
}
