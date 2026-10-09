package oauth_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// packages/mcp/src/oauth/errors.ts (Pi 1.0.4): each class's constructor sets the message, `name` and its own members. OAuthError is `super(message || code)`
// (errors.ts:6); the issuer mismatch message quotes both issuers with JSON.stringify, `none` for an absent `received` (errors.ts:18-24); the insecure
// endpoint and authorization-required classes have fixed messages (errors.ts:28-34, 49-54).
//
// mutation-checked: Name() of NewOAuthInsecureEndpointError returns "OAuthError"; NewOAuthError drops its code fallback.
func TestOAuthErrorConstructorsMatchErrorsTs(t *testing.T) {
	received := `https://evil.test/"x`
	for _, tc := range []struct {
		name string
		err  interface {
			Error() string
			Name() string
		}
		wantName    string
		wantMessage string
	}{
		{"OAuthError message", oauth.NewOAuthError("invalid_grant", "expired", "https://e.test"), "OAuthError", "expired"},
		{"OAuthError falls back to the code", oauth.NewOAuthError("server_error", ""), "OAuthError", "server_error"},
		{"issuer mismatch", oauth.NewOAuthIssuerMismatchError("https://a.test", &received), "OAuthIssuerMismatchError", `OAuth issuer mismatch: expected "https://a.test", received "https://evil.test/\"x"`},
		{"issuer mismatch without received", oauth.NewOAuthIssuerMismatchError("https://a.test", nil), "OAuthIssuerMismatchError", `OAuth issuer mismatch: expected "https://a.test", received none`},
		{"insecure endpoint", oauth.NewOAuthInsecureEndpointError("http://x.test/token"), "OAuthInsecureEndpointError", "Refusing to send OAuth credentials to non-HTTPS endpoint http://x.test/token"},
		{"authorization required", oauth.NewMcpOAuthAuthorizationRequiredError(), "McpOAuthAuthorizationRequiredError", "MCP OAuth authorization requires user interaction"},
	} {
		if tc.err.Name() != tc.wantName || tc.err.Error() != tc.wantMessage {
			t.Errorf("%s: name %q message %q, want %q %q", tc.name, tc.err.Name(), tc.err.Error(), tc.wantName, tc.wantMessage)
		}
	}
	e := oauth.NewOAuthError("invalid_grant", "expired", "https://e.test")
	if e.Code != "invalid_grant" || e.ErrorURI != "https://e.test" {
		t.Errorf("OAuthError members: %+v", e)
	}
	if bare := oauth.NewOAuthError("c", "m"); bare.ErrorURI != "" {
		t.Errorf("errorUri without the argument = %q, want absent", bare.ErrorURI)
	}
	issuer := oauth.NewOAuthIssuerMismatchError("https://a.test", &received)
	if issuer.Expected != "https://a.test" || issuer.Received == nil || *issuer.Received != received {
		t.Errorf("issuer mismatch members: %+v", issuer)
	}
}
