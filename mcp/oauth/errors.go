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

// NewOAuthError builds an OAuthError; the optional errorURI is Pi's third constructor argument (errors.ts OAuthError constructor).
func NewOAuthError(code, message string, errorURI ...string) *OAuthError {
	e := &OAuthError{Code: code, Message: message}
	if len(errorURI) > 0 {
		e.ErrorURI = errorURI[0]
	}
	return e
}

// Name is the error's class name, Pi's `name` property ("OAuthError").
func (*OAuthError) Name() string { return "OAuthError" }

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

// NewOAuthIssuerMismatchError is `new OAuthIssuerMismatchError(expected, received)`.
func NewOAuthIssuerMismatchError(expected string, received *string) *OAuthIssuerMismatchError {
	e := &OAuthIssuerMismatchError{Expected: expected, Received: received}
	return e
}

// Name is the `name` property, "OAuthIssuerMismatchError".
func (e *OAuthIssuerMismatchError) Name() string { return "OAuthIssuerMismatchError" }

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
type OAuthInsecureEndpointError struct {
	Endpoint string
}

// NewOAuthInsecureEndpointError is `new OAuthInsecureEndpointError(endpoint)`.
func NewOAuthInsecureEndpointError(endpoint string) *OAuthInsecureEndpointError {
	e := &OAuthInsecureEndpointError{Endpoint: endpoint}
	return e
}

// Name is the `name` property, "OAuthInsecureEndpointError".
func (e *OAuthInsecureEndpointError) Name() string { return "OAuthInsecureEndpointError" }

func (e *OAuthInsecureEndpointError) Error() string {
	return "Refusing to send OAuth credentials to non-HTTPS endpoint " + e.Endpoint
}

// OAuthRegistrationError reports a failed dynamic client registration.
type OAuthRegistrationError struct {
	Status int
	Body   string
}

// NewOAuthRegistrationError is `new OAuthRegistrationError(status, body)`.
func NewOAuthRegistrationError(status int, body string) *OAuthRegistrationError {
	e := &OAuthRegistrationError{Status: status, Body: body}
	return e
}

// Name is the `name` property, "OAuthRegistrationError".
func (e *OAuthRegistrationError) Name() string { return "OAuthRegistrationError" }

// Message is the `message` property, the text of Error.
func (e *OAuthRegistrationError) Message() string { return e.Error() }

// Cause is the `cause` property. The upstream constructor sets none, so it is always nil.
func (e *OAuthRegistrationError) Cause() error { return nil }

func (e *OAuthRegistrationError) Error() string {
	return fmt.Sprintf("OAuth dynamic client registration failed with status %d: %s", e.Status, e.Body)
}

// McpOAuthAuthorizationRequiredError reports that the user has to authorize.
type McpOAuthAuthorizationRequiredError struct {
}

// NewMcpOAuthAuthorizationRequiredError is `new McpOAuthAuthorizationRequiredError()`.
func NewMcpOAuthAuthorizationRequiredError() *McpOAuthAuthorizationRequiredError {
	e := &McpOAuthAuthorizationRequiredError{}
	return e
}

// Name is the `name` property, "McpOAuthAuthorizationRequiredError".
func (e *McpOAuthAuthorizationRequiredError) Name() string {
	return "McpOAuthAuthorizationRequiredError"
}

func (e *McpOAuthAuthorizationRequiredError) Error() string {
	return "MCP OAuth authorization requires user interaction"
}
