package tui

import (
	"strings"
	"testing"
)

// Ports .upstream/v1.0.0/packages/coding-agent/test/oauth-selector.test.ts:74-146 with the same provider names, auth types, sources and expected indicators.
func TestOAuthSelectorUpstreamStatuses(t *testing.T) {
	cases := []struct {
		provider           OAuthProvider
		contains, excludes string
	}{
		{OAuthProvider{ID: "google", Name: "Google", AuthType: "api_key"}, "not configured", "✓ configured"},
		{OAuthProvider{ID: "anthropic", Name: "Anthropic", AuthType: "api_key", Status: testAuthCheck{"oauth", "OAuth"}}, "subscription configured", ""},
		{OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", Status: testAuthCheck{"api_key", "OPENAI_API_KEY"}}, "✓ env: OPENAI_API_KEY", "not configured"},
		{OAuthProvider{ID: "local-proxy", Name: "local-proxy", AuthType: "api_key", Status: testAuthCheck{"api_key", "models_json_key"}}, "✓ models_json_key", ""},
		{OAuthProvider{ID: "op-proxy", Name: "op-proxy", AuthType: "api_key", Status: testAuthCheck{"api_key", "models_json_command"}}, "✓ models_json_command", ""},
	}
	for _, tc := range cases {
		t.Run(tc.provider.ID, func(t *testing.T) {
			output := stripANSI(strings.Join(NewOAuthSelectorComponent("login", []OAuthProvider{tc.provider}, nil, nil).Render(120), "\n"))
			if !strings.Contains(output, tc.contains) || tc.excludes != "" && strings.Contains(output, tc.excludes) {
				t.Fatalf("render=%q", output)
			}
		})
	}
}

// Pi 1.0.0 oauth-selector.ts:27-33: formatAuthSelectorProviderType(authType, subscription?) labels an OAuth sign-in
// "account" only when subscription is false; an omitted subscription keeps "subscription", as the one-argument form
// callers used before 1.0.0 did. formatAuthSelectorProviderStatus (:38-41) uses it for a credential of the other type.
func TestFormatAuthSelectorProviderTypeSubscriptionArgument(t *testing.T) {
	for _, tc := range []struct {
		authType     string
		subscription []bool
		want         string
	}{
		{"oauth", nil, "subscription"},
		{"oauth", []bool{true}, "subscription"},
		{"oauth", []bool{false}, "account"},
		{"api_key", nil, "API key"},
		{"api_key", []bool{false}, "API key"},
	} {
		if got := FormatAuthSelectorProviderType(tc.authType, tc.subscription...); got != tc.want {
			t.Errorf("FormatAuthSelectorProviderType(%q, %v) = %q, want %q", tc.authType, tc.subscription, got, tc.want)
		}
	}
	if got := FormatAuthSelectorProviderType("oauth"); got != "subscription" {
		t.Errorf("one-argument form = %q, want subscription", got)
	}
	account := false
	status := stripANSI(FormatAuthSelectorProviderStatus(OAuthProvider{ID: "radius", Name: "Radius", AuthType: "api_key", Status: testAuthCheck{"oauth", ""}, Subscription: &account}))
	if status != " • account configured" {
		t.Errorf("status = %q, want %q", status, " • account configured")
	}
}
