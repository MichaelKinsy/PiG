package oauth_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// flow.ts:197 asks for consent when options.scope.split(/\s+/) includes "offline_access". JavaScript's \s includes U+FEFF and excludes U+0085, the opposite of
// strings.Fields, so a BOM separates the scopes and a U+0085 does not.
func TestStartAuthorizationSplitsTheScopeAtJavaScriptWhitespace(t *testing.T) {
	for _, tc := range []struct {
		scope   string
		consent bool
	}{
		{"read offline_access", true},
		{"read\ufeffoffline_access", true},
		{"read\u00a0offline_access\u3000", true},
		{"offline_access\u0085", false},
		{"read\u0085offline_access", false},
		{"offline_access_extra", false},
	} {
		authorizationURL, _, err := oauth.StartAuthorization("https://auth.example.com", oauth.StartAuthorizationOptions{
			ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "client"},
			RedirectURL:       "http://localhost/callback", Scope: tc.scope,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := authorizationURL.Query().Get("prompt") == "consent"; got != tc.consent {
			t.Errorf("scope %q: prompt=consent is %v, want %v", tc.scope, got, tc.consent)
		}
	}
}
