package ai

// Ports .upstream/v0.99.2/packages/ai/test/anthropic-federation.test.ts and
// anthropic-federation-sdk.test.ts. Upstream hands the Anthropic SDK a `config` and lets the SDK exchange the
// identity token (node_modules/@anthropic-ai/sdk@0.129.0 lib/credentials/*). PiG has no SDK, so the tests observe
// the wire: the jwt-bearer exchange the SDK performs (oidc-federation.mjs:19-66) and the requests that follow.

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type federationWireRequest struct {
	path   string
	header http.Header
	body   string
}

// federationWire is a local Anthropic endpoint with the OAuth token route the SDK exchanges against.
type federationWire struct {
	mu       sync.Mutex
	requests []federationWireRequest
	// token answers the nth (zero-based) exchange; nil answers {access_token, expires_in: 3600}.
	token func(n int, w http.ResponseWriter)
	// messageStatus answers the nth message request; zero is 200.
	messageStatus func(n int) int
	exchanges     int
	messages      int
}

func (wire *federationWire) serve(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		wire.mu.Lock()
		wire.requests = append(wire.requests, federationWireRequest{path: r.URL.Path, header: r.Header.Clone(), body: string(raw)})
		var n int
		if r.URL.Path == "/v1/oauth/token" {
			n, wire.exchanges = wire.exchanges, wire.exchanges+1
		} else {
			n, wire.messages = wire.messages, wire.messages+1
		}
		wire.mu.Unlock()
		if r.URL.Path == "/v1/oauth/token" {
			if wire.token != nil {
				wire.token(n, w)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"federated-token","expires_in":3600}`)
			return
		}
		if wire.messageStatus != nil {
			if status := wire.messageStatus(n); status != 0 {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"bad token"}}`)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicEndTurnSSE)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func (wire *federationWire) byPath(path string) []federationWireRequest {
	wire.mu.Lock()
	defer wire.mu.Unlock()
	var matched []federationWireRequest
	for _, request := range wire.requests {
		if request.path == path {
			matched = append(matched, request)
		}
	}
	return matched
}

var federationContext = Context{SystemPrompt: "System prompt.", Messages: []Message{UserMessage{Content: UserText("Hello")}}}

// streamFederation streams one request through the provider and returns the terminal message. The consumer
// reserves its continuation before the provider runs, as the Agent does (see runAnthropicWire).
func streamFederation(t *testing.T, cfg AnthropicConfig, opts StreamOptions) *AssistantMessage {
	t.Helper()
	return streamFederationWithContext(t, cfg, federationContext, opts)
}

func federationEnvFor(t *testing.T) ProviderEnv {
	t.Helper()
	for _, name := range []string{AnthropicAuthTokenEnv, AnthropicOAuthTokenEnv, AnthropicAPIKeyEnv, AnthropicFederationRuleIDEnv,
		AnthropicOrganizationIDEnv, AnthropicServiceAccountIDEnv, AnthropicIdentityTokenFileEnv, AnthropicWorkspaceIDEnv} {
		t.Setenv(name, "")
	}
	identity := filepath.Join(t.TempDir(), "identity.jwt")
	if err := os.WriteFile(identity, []byte("header.payload.signature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ProviderEnv{
		AnthropicFederationRuleIDEnv:  "fdrl_test",
		AnthropicOrganizationIDEnv:    "org-test",
		AnthropicServiceAccountIDEnv:  "svac_test",
		AnthropicIdentityTokenFileEnv: identity,
	}
}

func resolveAnthropicWithEnv(t *testing.T, env map[string]string) *AuthResult {
	t.Helper()
	auth, err := BuiltinProviderAuth("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	result, err := auth.APIKey.Resolve(t.Context(), APIKeyAuthInput{Ctx: AuthContext{
		Env:        func(name string) (string, bool) { value := env[name]; return value, value != "" },
		FileExists: func(string) bool { return false },
	}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func withoutEnv(env ProviderEnv, names ...string) ProviderEnv {
	out := ProviderEnv{}
	maps.Copy(out, env)
	for _, name := range names {
		delete(out, name)
	}
	return out
}

// anthropic-federation.test.ts:92-174 (#10177)
func TestAnthropicWorkloadIdentityFederation(t *testing.T) {
	t.Run("resolves the federation variables as provider env with no request auth", func(t *testing.T) {
		env := federationEnvFor(t)
		result := resolveAnthropicWithEnv(t, env)
		if result == nil || result.Auth.APIKey != "" || len(result.Auth.Headers) != 0 || result.Source != "workload identity federation" {
			t.Fatalf("result = %+v", result)
		}
		if len(result.Env) != len(env) {
			t.Fatalf("env = %v, want %v", result.Env, env)
		}
		for name, value := range env {
			if result.Env[name] != value {
				t.Fatalf("env[%s] = %q, want %q", name, result.Env[name], value)
			}
		}
	})

	t.Run("passes ANTHROPIC_WORKSPACE_ID through when set", func(t *testing.T) {
		env := federationEnvFor(t)
		env[AnthropicWorkspaceIDEnv] = "wrkspc_test"
		if result := resolveAnthropicWithEnv(t, env); result == nil || result.Env[AnthropicWorkspaceIDEnv] != "wrkspc_test" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("is not configured when a federation variable is missing", func(t *testing.T) {
		for _, missing := range []string{AnthropicFederationRuleIDEnv, AnthropicOrganizationIDEnv, AnthropicIdentityTokenFileEnv} {
			if result := resolveAnthropicWithEnv(t, withoutEnv(federationEnvFor(t), missing)); result != nil {
				t.Fatalf("without %s: result = %+v, want nil", missing, result)
			}
		}
	})

	t.Run("treats ANTHROPIC_SERVICE_ACCOUNT_ID as optional, like the SDK", func(t *testing.T) {
		partial := withoutEnv(federationEnvFor(t), AnthropicServiceAccountIDEnv)
		result := resolveAnthropicWithEnv(t, partial)
		if result == nil || result.Source != "workload identity federation" || len(result.Env) != len(partial) {
			t.Fatalf("result = %+v", result)
		}
		if _, present := result.Env[AnthropicServiceAccountIDEnv]; present {
			t.Fatalf("env = %v carries a service account", result.Env)
		}

		wire := &federationWire{}
		message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: wire.serve(t)}, StreamOptions{Env: partial})
		if message.StopReason != StopReasonStop {
			t.Fatalf("stop reason = %q (%s)", message.StopReason, message.ErrorMessage)
		}
		exchange := wire.byPath("/v1/oauth/token")
		if len(exchange) != 1 {
			t.Fatalf("exchanges = %d, want 1", len(exchange))
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(exchange[0].body), &body); err != nil {
			t.Fatal(err)
		}
		if _, present := body["service_account_id"]; present {
			t.Fatalf("exchange body = %v carries service_account_id", body)
		}
	})

	t.Run("keeps API key and auth token precedence over federation", func(t *testing.T) {
		env := federationEnvFor(t)
		env[AnthropicAPIKeyEnv] = "api-key"
		if result := resolveAnthropicWithEnv(t, env); result == nil || result.Auth.APIKey != "api-key" || result.Source != "ANTHROPIC_API_KEY" || len(result.Env) != 0 {
			t.Fatalf("api key: result = %+v", result)
		}
		env = federationEnvFor(t)
		env[AnthropicAuthTokenEnv] = "auth-token"
		result := resolveAnthropicWithEnv(t, env)
		if result == nil || result.Source != "ANTHROPIC_AUTH_TOKEN" || result.Auth.APIKey != "" || result.Auth.Headers["Authorization"] == nil || *result.Auth.Headers["Authorization"] != "Bearer auth-token" {
			t.Fatalf("auth token: result = %+v", result)
		}
	})

	t.Run("exchanges the identity token and sends the federated bearer token instead of a key", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{}
		message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: wire.serve(t)}, StreamOptions{Env: env})
		if message.StopReason != StopReasonStop {
			t.Fatalf("stop reason = %q (%s)", message.StopReason, message.ErrorMessage)
		}
		exchange := wire.byPath("/v1/oauth/token")
		if len(exchange) != 1 {
			t.Fatalf("exchanges = %d, want 1", len(exchange))
		}
		// oidc-federation.mjs:34-55
		wantBody := `{"grant_type":"urn:ietf:params:oauth:grant-type:jwt-bearer","assertion":"header.payload.signature","federation_rule_id":"fdrl_test","organization_id":"org-test","service_account_id":"svac_test"}`
		if exchange[0].body != wantBody {
			t.Fatalf("exchange body = %s\nwant %s", exchange[0].body, wantBody)
		}
		for name, want := range map[string]string{
			"Content-Type":   "application/json",
			"Anthropic-Beta": "oauth-2025-04-20,oidc-federation-2026-04-01",
			"User-Agent":     "Anthropic/JS 0.129.0",
		} {
			if got := exchange[0].header.Get(name); got != want {
				t.Errorf("exchange %s = %q, want %q", name, got, want)
			}
		}
		messages := wire.byPath("/v1/messages")
		if len(messages) != 1 {
			t.Fatalf("message requests = %d, want 1", len(messages))
		}
		header := messages[0].header
		if got := header.Get("Authorization"); got != "Bearer federated-token" {
			t.Errorf("Authorization = %q", got)
		}
		if _, present := header["X-Api-Key"]; present {
			t.Errorf("X-Api-Key = %v, want none", header["X-Api-Key"])
		}
		// client.mjs:436-447 appends the OAuth beta to the request's betas; the body never carries betas.
		if got := header.Get("Anthropic-Beta"); got != "oauth-2025-04-20" {
			t.Errorf("Anthropic-Beta = %q, want oauth-2025-04-20", got)
		}
		if strings.Contains(messages[0].body, `"betas"`) || strings.Contains(messages[0].body, "oauth-2025-04-20") {
			t.Errorf("request body carries the OAuth beta: %s", messages[0].body)
		}
		if strings.HasPrefix(header.Get("User-Agent"), "claude-cli/") {
			t.Errorf("User-Agent = %q, want no Claude Code identity", header.Get("User-Agent"))
		}
	})

	t.Run("sends the workspace in the exchange body when set", func(t *testing.T) {
		env := federationEnvFor(t)
		env[AnthropicWorkspaceIDEnv] = "wrkspc_test"
		wire := &federationWire{}
		streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: wire.serve(t)}, StreamOptions{Env: env})
		exchange := wire.byPath("/v1/oauth/token")
		if len(exchange) != 1 || !strings.HasSuffix(exchange[0].body, `"service_account_id":"svac_test","workspace_id":"wrkspc_test"}`) {
			t.Fatalf("exchange = %+v", exchange)
		}
		if messages := wire.byPath("/v1/messages"); len(messages) != 1 || messages[0].header.Get("Anthropic-Workspace-Id") != "" {
			t.Fatalf("a federation workspace is an exchange field, not a request header: %+v", messages)
		}
	})
	// @anthropic-ai/sdk 0.129.0 client.mjs prepareRequest joins the trimmed betas with "," (0.124.0 appended ", ").

	t.Run("appends the OAuth beta to the request's other betas", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{}
		cfg := AnthropicConfig{ProviderID: "anthropic", BaseURL: wire.serve(t), Compat: &AnthropicMessagesCompat{SupportsEagerToolInputStreaming: new(false)}}
		request := federationContext
		request.Tools = []ToolSchema{{Name: "lookup", Description: "d", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}
		streamFederationWithContext(t, cfg, request, StreamOptions{Env: env})
		messages := wire.byPath("/v1/messages")
		if len(messages) != 1 {
			t.Fatalf("message requests = %d", len(messages))
		}
		if got := messages[0].header.Get("Anthropic-Beta"); got != "fine-grained-tool-streaming-2025-05-14,oauth-2025-04-20" {
			t.Errorf("Anthropic-Beta = %q", got)
		}
	})

	t.Run("threads authContext federation variables through Models", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{}
		models := CreateModels(CreateModelsOptions{AuthContext: &AuthContext{
			Env:        func(name string) (string, bool) { value := env[name]; return value, value != "" },
			FileExists: func(string) bool { return false },
		}})
		models.SetProvider(builtinProvider("anthropic"))
		model := &Model{ID: "claude-test", ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages, BaseURL: wire.serve(t)}, Input: []string{"text"},
			Capabilities: ModelCapabilities{ContextWindow: 100000, MaxOutputTokens: 4096}}
		message := models.CompleteSimple(context.Background(), model, federationContext)
		if message.StopReason != StopReasonStop {
			t.Fatalf("stop reason = %q (%s)", message.StopReason, message.ErrorMessage)
		}
		messages := wire.byPath("/v1/messages")
		if len(wire.byPath("/v1/oauth/token")) != 1 || len(messages) != 1 || messages[0].header.Get("Authorization") != "Bearer federated-token" || messages[0].header.Get("X-Api-Key") != "" {
			t.Fatalf("requests = %+v", wire.requests)
		}
	})

	t.Run("lets an explicit API key win over federation env", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{}
		message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: wire.serve(t), APIKey: "explicit-key"}, StreamOptions{Env: env})
		if message.StopReason != StopReasonStop {
			t.Fatalf("stop reason = %q (%s)", message.StopReason, message.ErrorMessage)
		}
		if len(wire.byPath("/v1/oauth/token")) != 0 {
			t.Fatal("an explicit API key started a federation exchange")
		}
		if messages := wire.byPath("/v1/messages"); len(messages) != 1 || messages[0].header.Get("X-Api-Key") != "explicit-key" || messages[0].header.Get("Authorization") != "" {
			t.Fatalf("messages = %+v", messages)
		}
	})

	t.Run("lets request auth headers win over federation env", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{}
		message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: wire.serve(t)}, StreamOptions{Env: env, Headers: ProviderHeaders{"Authorization": new("Bearer gateway-token")}})
		if message.StopReason != StopReasonStop || len(wire.byPath("/v1/oauth/token")) != 0 {
			t.Fatalf("stop reason = %q (%s), exchanges = %d", message.StopReason, message.ErrorMessage, len(wire.byPath("/v1/oauth/token")))
		}
		if messages := wire.byPath("/v1/messages"); len(messages) != 1 || messages[0].header.Get("Authorization") != "Bearer gateway-token" {
			t.Fatalf("messages = %+v", messages)
		}
	})

	t.Run("does not federate other anthropic-messages providers", func(t *testing.T) {
		env := federationEnvFor(t)
		wire := &federationWire{}
		message := streamFederation(t, AnthropicConfig{ProviderID: "kimi-coding", BaseURL: wire.serve(t)}, StreamOptions{Env: env})
		if message.StopReason != StopReasonError || message.ErrorMessage != "No API key for provider: kimi-coding" {
			t.Fatalf("stop reason = %q (%s), want the missing-key error", message.StopReason, message.ErrorMessage)
		}
		if len(wire.requests) != 0 {
			t.Fatalf("requests = %+v, want none", wire.requests)
		}
	})
}

// streamFederationWithContext is streamFederation for a caller-supplied context.
func streamFederationWithContext(t *testing.T, cfg AnthropicConfig, request Context, opts StreamOptions) *AssistantMessage {
	t.Helper()
	if cfg.Model == "" {
		cfg.Model = "claude-test"
	}
	ctx := WithStreamContinuations(t.Context())
	var events []AssistantMessageEvent
	err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
		stream, err := NewAnthropicProvider(cfg).Stream(ctx, NormalizeContext(request), opts)
		observation.Yield()
		if err != nil {
			return err
		}
		for event := range stream.Events(observation.Context(ctx)) {
			events = append(events, event)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return anthropicTerminal(t, events)
}

// anthropic.ts:33-69 tests each ctx.env value for truthiness, so a variable an auth context reports as set to ""
// is absent: it neither selects a credential nor completes the federation config.
func TestAnthropicAuthTreatsEmptyContextValuesAsAbsent(t *testing.T) {
	auth, err := BuiltinProviderAuth("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(env map[string]string) *AuthResult {
		t.Helper()
		result, err := auth.APIKey.Resolve(t.Context(), APIKeyAuthInput{Ctx: AuthContext{
			// Every name is reported as set, as an auth context backed by a plain map of set variables does.
			Env:        func(name string) (string, bool) { return env[name], true },
			FileExists: func(string) bool { return false },
		}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	federation := map[string]string{AnthropicFederationRuleIDEnv: "fdrl_test", AnthropicOrganizationIDEnv: "org-test", AnthropicIdentityTokenFileEnv: "/tmp/identity.jwt"}
	result := resolve(federation)
	if result == nil || result.Source != "workload identity federation" || result.Auth.APIKey != "" || len(result.Auth.Headers) != 0 {
		t.Fatalf("empty credential variables selected a credential: %+v", result)
	}
	if len(result.Env) != len(federation) {
		t.Fatalf("env = %v, want only the non-empty federation variables %v", result.Env, federation)
	}
	for _, missing := range []string{AnthropicFederationRuleIDEnv, AnthropicOrganizationIDEnv, AnthropicIdentityTokenFileEnv} {
		partial := withoutEnv(federation, missing)
		if result := resolve(partial); result != nil {
			t.Fatalf("with %s empty: result = %+v, want nil", missing, result)
		}
	}
}
