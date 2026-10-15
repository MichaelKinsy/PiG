package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/oauth-selector.ts

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi oauth-selector.ts:103-108 searches provider method names as well as names,
// IDs and auth types. "open" matches Anthropic through "Anthropic API key".
func TestLoginSearchIncludesProviderMethod(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir()})
	providers := m.oauthProviderList("login-api-key")
	selector := tui.NewOAuthSelectorComponent("login", providers, nil, nil)
	selector.HandleInput("open")
	var ids []string
	for range len(providers) {
		id := selector.SelectedID()
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
		selector.HandleInput("\x1b[B")
	}
	for _, id := range []string{"anthropic", "google", "amazon-bedrock", "google-vertex", "openrouter"} {
		if !slices.Contains(ids, id) {
			t.Errorf("missing %s from %v", id, ids)
		}
	}
}

func TestLoginOAuthListReportsEnvironmentKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "fake-env")
	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir()})
	providers := m.oauthProviderList("login-oauth")
	selector := tui.NewOAuthSelectorComponent("login", providers, nil, nil)
	selector.HandleInput("openrouter")
	if text := plainRender(selector); !strings.Contains(text, "API key configured") {
		t.Fatalf("OAuth status: %s", text)
	}
}

func TestLogoutIncludesStoredAPIKeyProviders(t *testing.T) {
	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(dir + "/auth.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"openai", "cloudflare-ai-gateway", "removed-extension"} {
		if err := auth.Set(id, ai.Credential{Type: ai.CredentialAPIKey, Key: "fake"}); err != nil {
			t.Fatal(err)
		}
	}
	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: dir})
	options := m.oauthProviderList("logout")
	for _, id := range []string{"openai", "cloudflare-ai-gateway", "removed-extension"} {
		index := slices.IndexFunc(options, func(p tui.OAuthProvider) bool { return p.ID == id })
		if index < 0 {
			t.Errorf("missing stored provider %s", id)
			continue
		}
		selector := tui.NewOAuthSelectorComponent("logout", options[index:index+1], nil, nil)
		if text := plainRender(selector); !strings.Contains(text, "✓ configured") {
			t.Errorf("logout status: %s", text)
		}
	}
}
