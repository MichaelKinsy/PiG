package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Ports packages/mcp/src/oauth/types.ts.
//
// Adapted from modelcontextprotocol/typescript-sdk v1.29.0. Structural
// validation replaces Zod; the parse functions return the errors upstream's
// throw.

// OAuthProtectedResourceMetadata is RFC 9728 metadata. Extra keeps the members
// this package does not model.
type OAuthProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

type protectedResourceWire OAuthProtectedResourceMetadata

// MarshalJSON writes the modeled and the extra members.
func (m OAuthProtectedResourceMetadata) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(protectedResourceWire(m), m.Extra)
}

// UnmarshalJSON reads the modeled members and keeps the rest.
func (m *OAuthProtectedResourceMetadata) UnmarshalJSON(data []byte) error {
	var wire protectedResourceWire
	extra, err := unmarshalWithExtra(data, &wire)
	if err != nil {
		return err
	}
	*m = OAuthProtectedResourceMetadata(wire)
	m.Extra = extra
	return nil
}

// AuthorizationServerMetadata is RFC 8414 metadata. A nil slice means the
// member was absent.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	ClientIDMetadataDocumentSupported *bool    `json:"client_id_metadata_document_supported,omitempty"`
	// AuthorizationResponseIssParameterSupported reports whether
	// authorization responses carry an iss parameter (RFC 9207).
	AuthorizationResponseIssParameterSupported *bool `json:"authorization_response_iss_parameter_supported,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

type authorizationServerWire AuthorizationServerMetadata

// MarshalJSON writes the modeled and the extra members.
func (m AuthorizationServerMetadata) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(authorizationServerWire(m), m.Extra)
}

// UnmarshalJSON reads the modeled members and keeps the rest.
func (m *AuthorizationServerMetadata) UnmarshalJSON(data []byte) error {
	var wire authorizationServerWire
	extra, err := unmarshalWithExtra(data, &wire)
	if err != nil {
		return err
	}
	*m = AuthorizationServerMetadata(wire)
	m.Extra = extra
	return nil
}

// OAuthTokens is a token response.
type OAuthTokens struct {
	AccessToken  string   `json:"access_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    *float64 `json:"expires_in,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	IDToken      string   `json:"id_token,omitempty"`
}

// OAuthClientMetadata is RFC 7591 client metadata.
type OAuthClientMetadata struct {
	RedirectURIs            []string        `json:"redirect_uris"`
	TokenEndpointAuthMethod string          `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string        `json:"grant_types,omitempty"`
	ResponseTypes           []string        `json:"response_types,omitempty"`
	ClientName              string          `json:"client_name,omitempty"`
	ClientURI               string          `json:"client_uri,omitempty"`
	LogoURI                 string          `json:"logo_uri,omitempty"`
	Scope                   string          `json:"scope,omitempty"`
	Contacts                []string        `json:"contacts,omitempty"`
	TosURI                  string          `json:"tos_uri,omitempty"`
	PolicyURI               string          `json:"policy_uri,omitempty"`
	JwksURI                 string          `json:"jwks_uri,omitempty"`
	Jwks                    json.RawMessage `json:"jwks,omitempty"`
	SoftwareID              string          `json:"software_id,omitempty"`
	SoftwareVersion         string          `json:"software_version,omitempty"`
	SoftwareStatement       string          `json:"software_statement,omitempty"`
}

// OAuthClientInformation is the client id and secret, plus the registered
// metadata when the client registered dynamically. It is upstream's
// OAuthClientInformationMixed: a configured client has only ClientID and
// ClientSecret, a registered client also carries its metadata.
type OAuthClientInformation struct {
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret,omitempty"`
	ClientIDIssuedAt      *float64 `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt *float64 `json:"client_secret_expires_at,omitempty"`
	OAuthClientMetadata
}

// OAuthClientInformationFull and OAuthClientInformationMixed are upstream
// names for the same union.
type (
	OAuthClientInformationFull  = OAuthClientInformation
	OAuthClientInformationMixed = OAuthClientInformation
)

// MarshalJSON omits redirect_uris for a client that registered none.
func (c OAuthClientInformation) MarshalJSON() ([]byte, error) {
	type plain OAuthClientInformation
	data, err := json.Marshal(plain(c))
	if err != nil {
		return nil, err
	}
	if c.RedirectURIs != nil {
		return data, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	delete(fields, "redirect_uris")
	return json.Marshal(fields)
}

// HasRedirectURIs reports whether the information came with metadata.
func (c OAuthClientInformation) HasRedirectURIs() bool { return c.RedirectURIs != nil }

// OAuthDiscoveryState is the cached discovery of one server.
type OAuthDiscoveryState struct {
	AuthorizationServerURL      string                          `json:"authorizationServerUrl"`
	AuthorizationServerMetadata *AuthorizationServerMetadata    `json:"authorizationServerMetadata,omitempty"`
	ResourceMetadata            *OAuthProtectedResourceMetadata `json:"resourceMetadata,omitempty"`
	ResourceMetadataURL         string                          `json:"resourceMetadataUrl,omitempty"`
}

// OAuthServerInfo is the result of discovery.
type OAuthServerInfo struct {
	AuthorizationServerURL      string
	AuthorizationServerMetadata *AuthorizationServerMetadata
	ResourceMetadata            *OAuthProtectedResourceMetadata
}

// OAuthChallenge is a parsed WWW-Authenticate challenge. Absent members are
// empty.
type OAuthChallenge struct {
	ResourceMetadataURL *url.URL
	Scope               string
	Error               string
	ErrorDescription    string
}

func requiredString(fields map[string]json.RawMessage, key, name string) (string, error) {
	var s string
	raw, ok := fields[key]
	if !ok || len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil || s == "" {
		return "", fmt.Errorf("Invalid %s", name)
	}
	return s, nil
}

// absent treats a missing member, `null`, and `""` as absent: servers send
// them for fields they have no value for, like `scope: ""`.
func absent(fields map[string]json.RawMessage, key string) bool {
	raw, ok := fields[key]
	if !ok || string(raw) == "null" {
		return true
	}
	var s string
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &s) == nil && s == ""
}

func optionalString(fields map[string]json.RawMessage, key, name string) (string, error) {
	if absent(fields, key) {
		return "", nil
	}
	return requiredString(fields, key, name)
}

func optionalStrings(fields map[string]json.RawMessage, key, name string) ([]string, error) {
	raw, ok := fields[key]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
		return nil, fmt.Errorf("Invalid %s", name)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		var s string
		if len(item) == 0 || item[0] != '"' || json.Unmarshal(item, &s) != nil {
			return nil, fmt.Errorf("Invalid %s", name)
		}
		out = append(out, s)
	}
	return out, nil
}

// safeURL requires an absolute URL that is not a script-bearing scheme.
func safeURL(fields map[string]json.RawMessage, key, name string) (string, error) {
	text, err := requiredString(fields, key, name)
	if err != nil {
		return "", err
	}
	// An unparsable URL fails as invalid metadata, not as the network
	// failure that discovery propagates.
	u, err := parseURL(text)
	if err != nil {
		return "", fmt.Errorf("Invalid %s", name)
	}
	switch u.Scheme {
	case "javascript", "data", "vbscript":
		return "", fmt.Errorf("Invalid %s", name)
	}
	return text, nil
}

func optionalURL(fields map[string]json.RawMessage, key, name string) (string, error) {
	if absent(fields, key) {
		return "", nil
	}
	return safeURL(fields, key, name)
}

func objectFields(value []byte, name string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if len(value) == 0 || value[0] != '{' || json.Unmarshal(value, &fields) != nil {
		return nil, fmt.Errorf("Invalid %s", name)
	}
	return fields, nil
}

// parseURL parses an absolute URL as `new URL(text)` does for the schemes
// MCP uses: it requires a scheme (and a host for http and https), lowercases
// the scheme and host, and gives an empty http(s) path the path "/".
func parseURL(text string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(text))
	if err != nil || u.Scheme == "" {
		return nil, errors.New("Invalid URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme == "http" || u.Scheme == "https" {
		if u.Host == "" {
			return nil, errors.New("Invalid URL")
		}
		u.Host = strings.ToLower(u.Host)
		if u.Path == "" {
			u.Path = "/"
		}
	}
	return u, nil
}

// ParseProtectedResourceMetadata validates RFC 9728 metadata.
func ParseProtectedResourceMetadata(value []byte) (*OAuthProtectedResourceMetadata, error) {
	fields, err := objectFields(value, "OAuth protected resource metadata")
	if err != nil {
		return nil, err
	}
	resource, err := safeURL(fields, "resource", "OAuth protected resource metadata resource")
	if err != nil {
		return nil, err
	}
	servers, err := optionalStrings(fields, "authorization_servers", "authorization_servers")
	if err != nil {
		return nil, err
	}
	for _, server := range servers {
		if _, err := safeURL(map[string]json.RawMessage{"u": mustJSON(server)}, "u", "authorization server URL"); err != nil {
			return nil, err
		}
	}
	scopes, err := optionalStrings(fields, "scopes_supported", "scopes_supported")
	if err != nil {
		return nil, err
	}
	var metadata OAuthProtectedResourceMetadata
	if err := json.Unmarshal(value, &metadata); err != nil {
		return nil, errors.New("Invalid OAuth protected resource metadata")
	}
	metadata.Resource, metadata.AuthorizationServers, metadata.ScopesSupported = resource, servers, scopes
	return &metadata, nil
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// ParseAuthorizationServerMetadata validates RFC 8414 metadata.
func ParseAuthorizationServerMetadata(value []byte) (*AuthorizationServerMetadata, error) {
	fields, err := objectFields(value, "authorization server metadata")
	if err != nil {
		return nil, err
	}
	responseTypes, err := optionalStrings(fields, "response_types_supported", "response_types_supported")
	if err != nil {
		return nil, err
	}
	if responseTypes == nil {
		return nil, errors.New("Invalid response_types_supported")
	}
	var m AuthorizationServerMetadata
	if err := json.Unmarshal(value, &m); err != nil {
		// A member of the wrong type: report the first that validation names.
		m = AuthorizationServerMetadata{}
	}
	if m.Issuer, err = safeURL(fields, "issuer", "authorization server issuer"); err != nil {
		return nil, err
	}
	if m.AuthorizationEndpoint, err = safeURL(fields, "authorization_endpoint", "authorization endpoint"); err != nil {
		return nil, err
	}
	if m.TokenEndpoint, err = safeURL(fields, "token_endpoint", "token endpoint"); err != nil {
		return nil, err
	}
	if m.RegistrationEndpoint, err = optionalURL(fields, "registration_endpoint", "registration endpoint"); err != nil {
		return nil, err
	}
	if m.ScopesSupported, err = optionalStrings(fields, "scopes_supported", "scopes_supported"); err != nil {
		return nil, err
	}
	m.ResponseTypesSupported = responseTypes
	if m.GrantTypesSupported, err = optionalStrings(fields, "grant_types_supported", "grant_types_supported"); err != nil {
		return nil, err
	}
	if m.TokenEndpointAuthMethodsSupported, err = optionalStrings(fields, "token_endpoint_auth_methods_supported", "token_endpoint_auth_methods_supported"); err != nil {
		return nil, err
	}
	if m.CodeChallengeMethodsSupported, err = optionalStrings(fields, "code_challenge_methods_supported", "code_challenge_methods_supported"); err != nil {
		return nil, err
	}
	m.ClientIDMetadataDocumentSupported = nil
	if raw, ok := fields["client_id_metadata_document_supported"]; ok {
		var b bool
		if (string(raw) == "true" || string(raw) == "false") && json.Unmarshal(raw, &b) == nil {
			m.ClientIDMetadataDocumentSupported = &b
		}
	}
	m.AuthorizationResponseIssParameterSupported = nil
	if raw, ok := fields["authorization_response_iss_parameter_supported"]; ok {
		var b bool
		if (string(raw) == "true" || string(raw) == "false") && json.Unmarshal(raw, &b) == nil {
			m.AuthorizationResponseIssParameterSupported = &b
		}
	}
	m.Extra = nil
	for name, raw := range fields {
		switch name {
		case "issuer", "authorization_endpoint", "token_endpoint", "registration_endpoint", "scopes_supported",
			"response_types_supported", "grant_types_supported", "token_endpoint_auth_methods_supported",
			"code_challenge_methods_supported", "client_id_metadata_document_supported",
			"authorization_response_iss_parameter_supported":
		default:
			if m.Extra == nil {
				m.Extra = map[string]json.RawMessage{}
			}
			m.Extra[name] = raw
		}
	}
	return &m, nil
}

// ParseOAuthTokens validates a token response.
func ParseOAuthTokens(value []byte) (*OAuthTokens, error) {
	fields, err := objectFields(value, "OAuth token response")
	if err != nil {
		return nil, err
	}
	var tokens OAuthTokens
	// `Number(null)` is 0, which would mark the token as expired at once.
	if !absent(fields, "expires_in") {
		expires, err := numberLike(fields["expires_in"])
		if err != nil {
			return nil, errors.New("Invalid expires_in")
		}
		tokens.ExpiresIn = &expires
	}
	if tokens.AccessToken, err = requiredString(fields, "access_token", "access_token"); err != nil {
		return nil, err
	}
	if tokens.TokenType, err = requiredString(fields, "token_type", "token_type"); err != nil {
		return nil, err
	}
	if tokens.Scope, err = optionalString(fields, "scope", "scope"); err != nil {
		return nil, err
	}
	if tokens.RefreshToken, err = optionalString(fields, "refresh_token", "refresh_token"); err != nil {
		return nil, err
	}
	if tokens.IDToken, err = optionalString(fields, "id_token", "id_token"); err != nil {
		return nil, err
	}
	return &tokens, nil
}

// numberLike is JavaScript's Number(value) for a JSON value, which accepts
// numeric strings.
func numberLike(raw json.RawMessage) (float64, error) {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &f); err != nil {
		return 0, err
	}
	return f, nil
}

// ParseClientInformation validates a dynamic registration response.
func ParseClientInformation(value []byte) (*OAuthClientInformationFull, error) {
	fields, err := objectFields(value, "OAuth client registration response")
	if err != nil {
		return nil, err
	}
	var info OAuthClientInformation
	if err := json.Unmarshal(value, &info); err != nil {
		info = OAuthClientInformation{}
	}
	if info.ClientID, err = requiredString(fields, "client_id", "client_id"); err != nil {
		return nil, err
	}
	if info.ClientSecret, err = optionalString(fields, "client_secret", "client_secret"); err != nil {
		return nil, err
	}
	info.ClientIDIssuedAt = optionalNumber(fields, "client_id_issued_at")
	info.ClientSecretExpiresAt = optionalNumber(fields, "client_secret_expires_at")
	uris, err := optionalStrings(fields, "redirect_uris", "redirect_uris")
	if err != nil {
		return nil, err
	}
	if uris == nil {
		uris = []string{}
	}
	info.RedirectURIs = uris
	return &info, nil
}

func optionalNumber(fields map[string]json.RawMessage, key string) *float64 {
	raw, ok := fields[key]
	if !ok || len(raw) == 0 || raw[0] == '"' {
		return nil
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return &f
}
