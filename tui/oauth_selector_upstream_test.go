package tui

import (
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/oauth-selector.test.ts:74-151 with the same provider names, auth types, sources and expected indicators.
func TestOAuthSelectorUpstreamStatuses(t *testing.T) {
	cases := []struct {
		provider           OAuthProvider
		contains, excludes string
	}{
		{OAuthProvider{ID: "google", Name: "Google", AuthType: "api_key"}, "unconfigured", "✓ configured"},
		{OAuthProvider{ID: "anthropic", Name: "Anthropic", AuthType: "api_key", Stored: true, StoredType: "oauth", AuthStatusSource: "OAuth"}, "subscription configured", ""},
		{OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", AuthStatusSource: "environment", AuthStatusLabel: "OPENAI_API_KEY"}, "✓ env: OPENAI_API_KEY", "unconfigured"},
		{OAuthProvider{ID: "local-proxy", Name: "local-proxy", AuthType: "api_key", AuthStatusSource: "models_json_key"}, "✓ key in models.json", ""},
		{OAuthProvider{ID: "op-proxy", Name: "op-proxy", AuthType: "api_key", AuthStatusSource: "models_json_command"}, "✓ command in models.json", ""},
	}
	for _, tc := range cases {
		t.Run(tc.provider.ID, func(t *testing.T) {
			output := stripANSI(strings.Join(NewOAuthSelector("login", []OAuthProvider{tc.provider}).Render(120), "\n"))
			if !strings.Contains(output, tc.contains) || tc.excludes != "" && strings.Contains(output, tc.excludes) {
				t.Fatalf("render=%q", output)
			}
		})
	}
}
