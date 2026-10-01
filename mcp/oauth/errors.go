package oauth

import "fmt"

// Ports packages/mcp/src/oauth/errors.ts.

// OAuthError is an OAuth error response.
type OAuthError struct {
	Code     string
	Message  string
	ErrorURI string
}

func (e *OAuthError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

// OAuthIssuerMismatchError reports authorization server metadata whose issuer
// differs from the URL it was discovered at.
type OAuthIssuerMismatchError struct{ Expected, Received string }

func (e *OAuthIssuerMismatchError) Error() string {
	return fmt.Sprintf("OAuth issuer mismatch: expected %q, received %q", e.Expected, e.Received)
}

// OAuthInsecureEndpointError reports a credential endpoint that is not HTTPS.
type OAuthInsecureEndpointError struct{ Endpoint string }

func (e *OAuthInsecureEndpointError) Error() string {
	return "Refusing to send OAuth credentials to non-HTTPS endpoint " + e.Endpoint
}

// OAuthRegistrationError reports a failed dynamic client registration.
type OAuthRegistrationError struct {
	Status int
	Body   string
}

func (e *OAuthRegistrationError) Error() string {
	return fmt.Sprintf("OAuth dynamic client registration failed with status %d: %s", e.Status, e.Body)
}

// McpOAuthAuthorizationRequiredError reports that the user has to authorize.
type McpOAuthAuthorizationRequiredError struct{}

func (e *McpOAuthAuthorizationRequiredError) Error() string {
	return "MCP OAuth authorization requires user interaction"
}
