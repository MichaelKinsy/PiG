package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// providers/anthropic.ts:47-69 (0.99.2): `pi --list-models` with only the workload identity federation variables lists the
// anthropic catalog. Probe: isolated HOME, ANTHROPIC_FEDERATION_RULE_ID, ANTHROPIC_ORGANIZATION_ID and
// ANTHROPIC_IDENTITY_TOKEN_FILE only -> "anthropic claude-sonnet-4-5 ...".
func TestPrintModelList_AnthropicFederationEnv_ListsAnthropic(t *testing.T) {
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(ai.AnthropicFederationRuleIDEnv, "fdrl_test")
	t.Setenv(ai.AnthropicOrganizationIDEnv, "org-test")
	t.Setenv(ai.AnthropicIdentityTokenFileEnv, "/identity.jwt")
	dir := t.TempDir()
	reg := codingagent.NewModelRegistry(dir)

	out := captureStdout(func() { printModelList(reg, dir, "claude-sonnet-4-5") })

	if strings.Contains(out, "No models available") || !strings.Contains(out, "anthropic") || !strings.Contains(out, "claude-sonnet-4-5") {
		t.Fatalf("printModelList with federation-only env:\n%s", out)
	}
}

// providers/anthropic.ts:47-69 (0.99.2): with only the federation variables, startup selection (`pi -p hi`, no --model)
// finds an authenticated anthropic model instead of "No API key found for the selected model."
func TestSelectStartupModel_AnthropicFederationEnv_SelectsAnthropic(t *testing.T) {
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(ai.AnthropicFederationRuleIDEnv, "fdrl_test")
	t.Setenv(ai.AnthropicOrganizationIDEnv, "org-test")
	t.Setenv(ai.AnthropicIdentityTokenFileEnv, "/identity.jwt")
	dir := t.TempDir()
	selected, err := selectStartupModel(t.Context(), startupModelOptions{}, codingagent.Settings{}, testServices(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if selected.Model == nil || selected.Model.ProviderMeta.ProviderID != "anthropic" {
		t.Fatalf("selected model = %+v, want an anthropic model", selected.Model)
	}
}

// The complete startup path (model build, runtime auth resolution, provider request) for the federation-only
// environment. providers/anthropic.ts:47-69 puts federation after ANTHROPIC_AUTH_TOKEN, ANTHROPIC_OAUTH_TOKEN and
// ANTHROPIC_API_KEY, so a configured key or token wins and no token exchange happens.
func TestModelRuntimeStream_AnthropicFederationOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		env           map[string]string
		exchanges     int
		authorization string
		apiKey        string
	}{
		{name: "federation only", exchanges: 1, authorization: "Bearer federated-token"},
		{name: "auth token outranks federation", env: map[string]string{ai.AnthropicAuthTokenEnv: "auth-token"}, authorization: "Bearer auth-token"},
		{name: "api key outranks federation", env: map[string]string{ai.AnthropicAPIKeyEnv: "api-key"}, apiKey: "api-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filterAllProviderEnv(t)
			t.Setenv("HOME", t.TempDir())
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			identity := filepath.Join(t.TempDir(), "identity.jwt")
			if err := os.WriteFile(identity, []byte("header.payload.signature\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(ai.AnthropicFederationRuleIDEnv, "fdrl_test")
			t.Setenv(ai.AnthropicOrganizationIDEnv, "org-test")
			t.Setenv(ai.AnthropicIdentityTokenFileEnv, identity)
			var mu sync.Mutex
			var exchanges int
			var header http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if r.URL.Path == "/v1/oauth/token" {
					mu.Lock()
					exchanges++
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"access_token":"federated-token","expires_in":3600}`)
					return
				}
				mu.Lock()
				header = r.Header.Clone()
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"anthropic":{"baseUrl":"`+server.URL+`"}}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			services := testServices(t, dir)
			selected, err := selectStartupModel(t.Context(), startupModelOptions{}, codingagent.Settings{}, services)
			if err != nil {
				t.Fatal(err)
			}
			if selected.Model == nil || selected.Model.ProviderMeta.ProviderID != "anthropic" {
				t.Fatalf("selected model = %+v, want an anthropic model", selected.Model)
			}
			transcript := ai.Context{SystemPrompt: "System prompt.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}
			message := services.ModelRuntime().Complete(t.Context(), selected.Model, transcript, ai.StreamOptions{})
			mu.Lock()
			defer mu.Unlock()
			if message.StopReason == ai.StopReasonError {
				t.Fatalf("stop reason error: %s", message.ErrorMessage)
			}
			if exchanges != tc.exchanges {
				t.Errorf("token exchanges = %d, want %d", exchanges, tc.exchanges)
			}
			if got := header.Get("Authorization"); got != tc.authorization {
				t.Errorf("Authorization = %q, want %q", got, tc.authorization)
			}
			if got := header.Get("X-Api-Key"); got != tc.apiKey {
				t.Errorf("X-Api-Key = %q, want %q", got, tc.apiKey)
			}
		})
	}
}
