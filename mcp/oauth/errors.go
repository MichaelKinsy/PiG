package oauth

import (
	"fmt"
	"strings"
)

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
// differs from the URL it was discovered at, or an authorization response
// whose `iss` parameter does not name the flow's authorization server
// (RFC 9207).
type OAuthIssuerMismatchError struct {
	Expected string
	// Received is nil when an authorization response lacks the `iss`
	// parameter its server promised.
	Received *string
}

func (e *OAuthIssuerMismatchError) Error() string {
	received := "none"
	if e.Received != nil {
		received = jsonString(*e.Received)
	}
	return fmt.Sprintf("OAuth issuer mismatch: expected %s, received %s", jsonString(e.Expected), received)
}

// jsonString is `JSON.stringify` of a string: it escapes `"`, `\` and the C0
// controls, with the short forms for \b \t \n \f \r, and keeps every other
// character, including U+2028 and U+2029, which encoding/json escapes.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
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
