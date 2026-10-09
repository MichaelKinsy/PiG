package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type loginAgentNameProbe struct {
	Provider  string  `json:"provider"`
	AgentName *string `json:"agentName"`
}

type loginAgentNameResult struct {
	Hint       *string `json:"hint"`
	Originator *string `json:"originator"`
}

// LoginOptions.agentName (auth/types.ts:206-213) is the name an app introduces itself with: it is OpenAI ChatGPT's agent_name_hint
// (openai-chatgpt.ts:253, `options?.agentName ?? "Pi"`) and the Codex originator (openai-codex.ts:276,363). The pinned pi-ai runs both
// logins up to the authorization URL; an empty name is kept, not replaced by the default.
// pig divergence (D26): the Codex default originator is PiG's, so a probe without a name compares only the ChatGPT hint with Pi.
func TestLoginAgentNameMatchesPi(t *testing.T) {
	name := func(value string) *string { return &value }
	var probes []loginAgentNameProbe
	for _, provider := range []string{"chatgpt", "codex"} {
		for _, agentName := range []*string{nil, name("Acme Agent"), name(""), name("a b&c")} {
			probes = append(probes, loginAgentNameProbe{Provider: provider, AgentName: agentName})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/login_agent_name.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []loginAgentNameResult
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, probe := range probes {
		providerID := "openai"
		if probe.Provider == "codex" {
			providerID = "openai-codex"
		}
		isolateAnthropicCallbackHost(t)
		method, ok := OAuthProviderAuth(providerID)
		if !ok || method == nil {
			t.Fatalf("%s has no OAuth method", providerID)
		}
		var authURL string
		interaction := AuthInteraction{
			Notify: func(event AuthEvent) {
				if auth, ok := event.(AuthURLEvent); ok && authURL == "" {
					authURL = auth.URL
				}
			},
			// The first select option is the browser login; an empty pasted code then ends the login right after the URL was announced.
			Prompt: func(_ context.Context, prompt AuthPrompt) (string, error) {
				if selection, ok := prompt.(AuthSelectPrompt); ok {
					return selection.Options[0].ID, nil
				}
				return "", nil
			},
		}
		// Models.Login hands LoginOptions to the provider's login: the path under test.
		_, loginErr := method.Login(t.Context(), interaction, LoginOptions{GetDeviceID: func() string { return "0b1f9c3e-5a47-4a28-9d0f-6a1d2c3b4e5f" }, AgentName: probe.AgentName})
		if authURL == "" {
			t.Fatalf("probe %d (%s): no authorization URL: %v", i, probe.Provider, loginErr)
		}
		query, err := url.Parse(authURL)
		if err != nil {
			t.Fatal(err)
		}
		values := query.Query()
		if probe.Provider == "chatgpt" {
			// An omitted agentName takes PiG's identity instead of Pi's "Pi" (D26); a given name, empty included, is sent unchanged.
			expected := want[i].Hint
			if probe.AgentName == nil {
				def := pigidentity.ChatGPTAgentName
				expected = &def
			}
			if got := values.Get("agent_name_hint"); expected == nil || got != *expected {
				t.Errorf("probe %d chatgpt agentName=%v: agent_name_hint = %q, Pi %v", i, deref(probe.AgentName), got, deref(expected))
			}
			continue
		}
		expected := want[i].Originator
		if probe.AgentName == nil {
			def := pigidentity.CodexOriginator
			expected = &def
		}
		if got := values.Get("originator"); expected == nil || got != *expected {
			t.Errorf("probe %d codex agentName=%v: originator = %q, Pi %v", i, deref(probe.AgentName), got, deref(expected))
		}
	}
}

func deref(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
