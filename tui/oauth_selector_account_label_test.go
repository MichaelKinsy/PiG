package tui

import (
	"strings"
	"testing"
)

// oauth-selector.ts:27-52 (Pi 1.0.0): an OAuth sign-in whose provider is not subscription-backed is an "account" both in the auth-type label and in the "configured" status of the provider's other row; unset keeps "subscription".
func TestOAuthSelectorLabelsNonSubscriptionOAuthAsAccount(t *testing.T) {
	notSubscription, subscription := false, true
	// The released one-argument call is upstream's call with subscription unset.
	for authType, want := range map[string]string{"oauth": "subscription", "api_key": "API key"} {
		if got := FormatAuthSelectorProviderType(authType); got != want {
			t.Fatalf("FormatAuthSelectorProviderType(%q) = %q, want %q", authType, got, want)
		}
	}
	if got := FormatAuthSelectorProviderType("oauth", false); got != "account" {
		t.Fatalf("FormatAuthSelectorProviderType(oauth, false) = %q, want account", got)
	}
	for _, tc := range []struct {
		name         string
		subscription *bool
		want         string
	}{
		{"not a subscription", &notSubscription, "account"},
		{"subscription", &subscription, "subscription"},
		{"unset", nil, "subscription"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := NewOAuthSelector("login", []OAuthProvider{
				{ID: "radius", Name: "Radius", AuthType: "oauth", Subscription: tc.subscription},
				{ID: "radius", Name: "Radius", AuthType: "api_key", Subscription: tc.subscription, Stored: true, StoredType: "oauth"},
			})
			text := stripANSI(strings.Join(selector.Render(100), "\n"))
			if !strings.Contains(text, "Radius ["+tc.want+"]") {
				t.Fatalf("OAuth row label is not [%s]:\n%s", tc.want, text)
			}
			if !strings.Contains(text, tc.want+" configured") {
				t.Fatalf("API-key row status is not %q:\n%s", tc.want+" configured", text)
			}
		})
	}
}
