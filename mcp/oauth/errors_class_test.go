package oauth_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/src/oauth/errors.ts OAuthRegistrationError: message text, `name`, `cause` (never set) and `stack`.
func TestOAuthRegistrationErrorCarriesStatusBodyAndName(t *testing.T) {
	e := oauth.NewOAuthRegistrationError(400, "bad client")
	if e.Status != 400 || e.Body != "bad client" {
		t.Fatalf("status/body = %d %q", e.Status, e.Body)
	}
	want := "OAuth dynamic client registration failed with status 400: bad client"
	if e.Error() != want || e.Message() != want || e.Name() != "OAuthRegistrationError" || e.Cause() != nil {
		t.Fatalf("error=%q message=%q name=%q cause=%v", e.Error(), e.Message(), e.Name(), e.Cause())
	}
}

// packages/mcp/src/oauth/errors.ts:1-36,50-55: the constructors and `name` of the other error classes. `message` is the text Error reports
// (OAuthError's empty message is its code, as upstream's `message || code`).
func TestOAuthErrorClassesCarryTheirNameAndMessage(t *testing.T) {
	received := "https://other"
	cases := []struct {
		err           interface{ Error() string }
		name, message string
	}{
		{oauth.NewOAuthError("invalid_grant", "", "uri"), "OAuthError", "invalid_grant"},
		{oauth.NewOAuthError("invalid_grant", "expired", ""), "OAuthError", "expired"},
		{oauth.NewOAuthIssuerMismatchError("https://a", &received), "OAuthIssuerMismatchError", `OAuth issuer mismatch: expected "https://a", received "https://other"`},
		{oauth.NewOAuthInsecureEndpointError("http://x"), "OAuthInsecureEndpointError", "Refusing to send OAuth credentials to non-HTTPS endpoint http://x"},
		{oauth.NewMcpOAuthAuthorizationRequiredError(), "McpOAuthAuthorizationRequiredError", "MCP OAuth authorization requires user interaction"},
	}
	for _, c := range cases {
		named := c.err.(interface{ Name() string })
		if named.Name() != c.name || c.err.Error() != c.message {
			t.Errorf("%T: name=%q message=%q, want %q %q", c.err, named.Name(), c.err.Error(), c.name, c.message)
		}
	}
	if e := oauth.NewOAuthError("c", "m", "u"); e.Code != "c" || e.ErrorURI != "u" {
		t.Errorf("OAuthError = %+v", e)
	}
}
