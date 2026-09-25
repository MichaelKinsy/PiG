package codingagent

import "testing"

// TestAnthropicEnvAuthResolution mirrors upstream providers/anthropic.ts
// anthropicApiKeyAuth().resolve for ambient env. ANTHROPIC_AUTH_TOKEN wins and
// resolves no API key, because the provider sends it as an Authorization
// bearer header (anthropic-auth-token.test.ts); ANTHROPIC_OAUTH_TOKEN stays an
// API key ahead of ANTHROPIC_API_KEY (env-api-keys.test.ts).
func TestAnthropicEnvAuthResolution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    map[string]string
		apiKey string
	}{
		{"all three", map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token", "ANTHROPIC_OAUTH_TOKEN": "oauth-token", "ANTHROPIC_API_KEY": "api-key"}, ""},
		{"auth token only", map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"}, ""},
		{"oauth token", map[string]string{"ANTHROPIC_OAUTH_TOKEN": "oauth-token", "ANTHROPIC_API_KEY": "api-key"}, "oauth-token"},
		{"api key", map[string]string{"ANTHROPIC_API_KEY": "api-key"}, "api-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAllAuthEnv(t)
			t.Setenv("HOME", t.TempDir())
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			if got := resolveAPIKeyFromEnv("anthropic"); got != tc.apiKey {
				t.Errorf("resolveAPIKeyFromEnv = %q, want %q", got, tc.apiKey)
			}
			if !hasEnvAuth("anthropic") {
				t.Error("hasEnvAuth = false, want true")
			}
			dir := t.TempDir()
			registry := NewModelRegistry(dir)
			if !registry.HasConfiguredAuth("anthropic") {
				t.Error("HasConfiguredAuth = false, want true")
			}
			if !AuthenticatedProviders(dir)["anthropic"] {
				t.Error("anthropic missing from AuthenticatedProviders")
			}
			entry, ok := registry.Resolve("anthropic", "claude-custom")
			if !ok || entry.APIKey != tc.apiKey {
				t.Errorf("Resolve = %+v, %v; want APIKey %q", entry, ok, tc.apiKey)
			}
		})
	}
}

func TestAnthropicEnvAuthAbsent(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("HOME", t.TempDir())
	if hasEnvAuth("anthropic") {
		t.Error("hasEnvAuth = true with no Anthropic env")
	}
	if AuthenticatedProviders(t.TempDir())["anthropic"] {
		t.Error("anthropic authenticated with no Anthropic env")
	}
}
