package ai

// Federation auth tests from the 0.99.1 -> 0.99.2 packages/ai audit (audit-992-ai findings F1, F2). Each case states the
// Pi 0.99.2 behavior that was measured against the installed @earendil-works/pi-ai@0.99.2 package.

import (
	"maps"
	"net"
	"testing"
)

// anthropic-messages.ts:614-615, 932-937 and 1054-1067: a direct stream with no apiKey and no auth header uses
// workload identity federation when the federation variables are set. Pi never reads ANTHROPIC_AUTH_TOKEN in the
// API implementation (only providers/anthropic.ts does, ahead of federation); PiClient passes authToken: null.
// Probe: ANTHROPIC_AUTH_TOKEN=authtok plus the three federation variables in the process env, streamSimple and
// stream without options -> POST /v1/oauth/token, then /v1/messages with "Authorization: Bearer fed-token".
func TestAudit992DirectAnthropicStreamIgnoresAmbientAuthTokenForFederation(t *testing.T) {
	env := federationEnvFor(t)
	for name, value := range env {
		t.Setenv(name, value)
	}
	t.Setenv(AnthropicAuthTokenEnv, "ambient-auth-token")
	wire := &federationWire{}
	baseURL := wire.serve(t)
	message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{})
	if message.StopReason != StopReasonStop {
		t.Fatalf("stop reason = %q, error = %q", message.StopReason, message.ErrorMessage)
	}
	if got := len(wire.byPath("/v1/oauth/token")); got != 1 {
		t.Errorf("token exchanges = %d, want 1 (Pi federates; ANTHROPIC_AUTH_TOKEN is only read by the auth resolver)", got)
	}
	for _, request := range wire.byPath("/v1/messages") {
		if got := request.header.Get("Authorization"); got != "Bearer federated-token" {
			t.Errorf("message Authorization = %q, want %q", got, "Bearer federated-token")
		}
	}
}

// oidc-federation.mjs:47-49 wraps the fetch rejection as `Failed to reach token endpoint ${url}: ${err}`, and
// String(TypeError("fetch failed")) is "TypeError: fetch failed".
// Probe: baseUrl http://127.0.0.1:1 -> errorMessage
// "Failed to reach token endpoint http://127.0.0.1:1/v1/oauth/token: TypeError: fetch failed".
func TestAudit992FederationUnreachableTokenEndpointMessage(t *testing.T) {
	env := federationEnvFor(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	_ = listener.Close()
	message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env})
	want := "Failed to reach token endpoint " + baseURL + "/v1/oauth/token: TypeError: fetch failed"
	if message.StopReason != StopReasonError || message.ErrorMessage != want {
		t.Fatalf("stop reason = %q, error = %q\nwant error %q", message.StopReason, message.ErrorMessage, want)
	}
}

// anthropic-messages.ts:331-341, 614-615, 318-329: PiClient owns no credential (apiKey: null, authToken: null), so a
// direct stream with no apiKey, no auth header and no federation fails assertRequestAuth even when the process env
// holds ANTHROPIC_AUTH_TOKEN. Only the provider's auth resolver (providers/anthropic.ts:56-62) reads that variable.
func TestAnthropicDirectStreamDoesNotReadAmbientAuthToken(t *testing.T) {
	for _, name := range []string{AnthropicFederationRuleIDEnv, AnthropicOrganizationIDEnv, AnthropicIdentityTokenFileEnv} {
		t.Setenv(name, "")
	}
	t.Setenv(AnthropicAuthTokenEnv, "ambient-auth-token")
	wire := &federationWire{}
	baseURL := wire.serve(t)
	message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{})
	if message.StopReason != StopReasonError || message.ErrorMessage != "No API key for provider: anthropic" {
		t.Fatalf("stop reason = %q, error = %q, want the missing-key error", message.StopReason, message.ErrorMessage)
	}
	if got := len(wire.byPath("/v1/messages")); got != 0 {
		t.Errorf("messages requests = %d, want 0", got)
	}
}

// providers/anthropic.ts:47-69 reads the three required variables in order and treats an unset or empty value as absent;
// the service account and workspace are optional. Scoped env wins over the process env (provider-env.ts:getProviderEnvValue).
func TestAnthropicFederationEnv(t *testing.T) {
	names := []string{AnthropicFederationRuleIDEnv, AnthropicOrganizationIDEnv, AnthropicIdentityTokenFileEnv, AnthropicServiceAccountIDEnv, AnthropicWorkspaceIDEnv}
	full := ProviderEnv{AnthropicFederationRuleIDEnv: "rule", AnthropicOrganizationIDEnv: "org", AnthropicIdentityTokenFileEnv: "/id.jwt"}
	t.Run("required variables only", func(t *testing.T) {
		for _, name := range names {
			t.Setenv(name, "")
		}
		got := AnthropicFederationEnv(full)
		if len(got) != 3 || got[AnthropicFederationRuleIDEnv] != "rule" || got[AnthropicOrganizationIDEnv] != "org" || got[AnthropicIdentityTokenFileEnv] != "/id.jwt" {
			t.Fatalf("env = %#v", got)
		}
	})
	t.Run("optional variables travel", func(t *testing.T) {
		for _, name := range names {
			t.Setenv(name, "")
		}
		env := ProviderEnv{AnthropicServiceAccountIDEnv: "svc", AnthropicWorkspaceIDEnv: "ws"}
		maps.Copy(env, full)
		got := AnthropicFederationEnv(env)
		if len(got) != 5 || got[AnthropicServiceAccountIDEnv] != "svc" || got[AnthropicWorkspaceIDEnv] != "ws" {
			t.Fatalf("env = %#v", got)
		}
	})
	for _, missing := range names[:3] {
		t.Run("missing "+missing, func(t *testing.T) {
			for _, name := range names {
				t.Setenv(name, "")
			}
			env := ProviderEnv{}
			for name, value := range full {
				if name != missing {
					env[name] = value
				}
			}
			if got := AnthropicFederationEnv(env); got != nil {
				t.Fatalf("env = %#v, want nil", got)
			}
		})
	}
	t.Run("process env and scoped override", func(t *testing.T) {
		for _, name := range names {
			t.Setenv(name, "")
		}
		t.Setenv(AnthropicFederationRuleIDEnv, "process-rule")
		t.Setenv(AnthropicOrganizationIDEnv, "process-org")
		t.Setenv(AnthropicIdentityTokenFileEnv, "/process.jwt")
		got := AnthropicFederationEnv(ProviderEnv{AnthropicOrganizationIDEnv: "scoped-org"})
		if got[AnthropicFederationRuleIDEnv] != "process-rule" || got[AnthropicOrganizationIDEnv] != "scoped-org" {
			t.Fatalf("env = %#v", got)
		}
	})
}
