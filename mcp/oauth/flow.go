package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/src/oauth/flow.ts.
//
// Adapted from modelcontextprotocol/typescript-sdk v1.29.0 src/client/auth.ts.

// formParams is a URLSearchParams: insertion ordered, form-urlencoded.
type formParams struct{ keys, values []string }

func (p *formParams) Set(key, value string) {
	if i := slices.Index(p.keys, key); i >= 0 {
		p.values[i] = value
		return
	}
	p.keys, p.values = append(p.keys, key), append(p.values, value)
}

func (p *formParams) Get(key string) (string, bool) {
	if i := slices.Index(p.keys, key); i >= 0 {
		return p.values[i], true
	}
	return "", false
}

func formEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c == ' ':
			b.WriteByte('+')
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

func (p *formParams) Encode() string {
	parts := make([]string, len(p.keys))
	for i, key := range p.keys {
		parts[i] = formEscape(key) + "=" + formEscape(p.values[i])
	}
	return strings.Join(parts, "&")
}

// AddClientAuthentication replaces the default client authentication of a
// token request.
type AddClientAuthentication func(ctx context.Context, headers http.Header, params *TokenParams, endpoint *url.URL, metadata *AuthorizationServerMetadata) error

// TokenParams is the form body of a token request.
type TokenParams struct{ p formParams }

// Set sets a form parameter.
func (t *TokenParams) Set(key, value string) { t.p.Set(key, value) }

// Get reads a form parameter.
func (t *TokenParams) Get(key string) (string, bool) { return t.p.Get(key) }

// OAuthClientProvider supplies the state of one OAuth client: registration,
// tokens, and the PKCE verifier. The optional capabilities are separate
// interfaces: [StateProvider], [ClientInformationSaver],
// [ClientMetadataDocumentProvider], [ClientAuthenticator],
// [CredentialInvalidator], and [DiscoveryStateStore].
type OAuthClientProvider interface {
	RedirectURL() string
	ClientMetadata() OAuthClientMetadata
	ClientInformation(ctx context.Context) (*OAuthClientInformationMixed, error)
	Tokens(ctx context.Context) (*OAuthTokens, error)
	SaveTokens(ctx context.Context, tokens OAuthTokens) error
	RedirectToAuthorization(ctx context.Context, authorizationURL *url.URL) error
	SaveCodeVerifier(ctx context.Context, verifier string) error
	CodeVerifier(ctx context.Context) (string, error)
}

// StateProvider supplies the OAuth `state` parameter.
type StateProvider interface {
	State(ctx context.Context) (string, error)
}

// ClientInformationSaver persists a dynamically registered client.
type ClientInformationSaver interface {
	SaveClientInformation(ctx context.Context, information OAuthClientInformationMixed) error
}

// OAuthClientMetadataDocument is a Client ID Metadata Document: an https URL
// used as `client_id`, and a redirect URI it lists.
type OAuthClientMetadataDocument struct {
	URL         string
	RedirectURL string
}

// ClientMetadataDocumentProvider names the Client ID Metadata Document to
// identify as instead of registering dynamically, or nil to register. It is
// called when no client information is stored; the document is not stored.
// metadata is nil when the authorization server has none; check
// `client_id_metadata_document_supported`.
type ClientMetadataDocumentProvider interface {
	ClientMetadataDocument(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
}

// ClientAuthenticator overrides client authentication on token requests.
type ClientAuthenticator interface {
	AddClientAuthentication() AddClientAuthentication
}

// CredentialInvalidator drops stored credentials. kind is one of "all",
// "client", "tokens", "verifier", or "discovery".
type CredentialInvalidator interface {
	InvalidateCredentials(ctx context.Context, kind string) error
}

// DiscoveryStateStore caches discovery.
type DiscoveryStateStore interface {
	SaveDiscoveryState(ctx context.Context, state OAuthDiscoveryState) error
	DiscoveryState(ctx context.Context) (*OAuthDiscoveryState, error)
}

// OAuthFlowOptions configure [AuthorizeMcp].
type OAuthFlowOptions struct {
	ServerURL         string
	AuthorizationCode string
	// Iss is the iss parameter of the authorization response that
	// delivered AuthorizationCode (RFC 9207); nil when it had none.
	Iss                 *string
	Scope               string
	ResourceMetadataURL *url.URL
	// AuthorizationServerMetadataURL is an authorization server metadata
	// document to use instead of discovery, for servers that advertise a
	// wrong authorization server or none. It is trusted as configured and
	// must use https, except on loopback.
	AuthorizationServerMetadataURL *url.URL
	Fetch                          mcp.McpFetch
	SkipIssuerValidation           bool
	// SkipRefresh goes straight to the authorization redirect instead of
	// refreshing stored tokens, for example when the server asks for scopes
	// the current grant lacks (a refresh keeps the old scope).
	SkipRefresh bool
}

// OAuthFlowResult is the outcome of [AuthorizeMcp].
type OAuthFlowResult string

// Flow results.
const (
	OAuthAuthorized OAuthFlowResult = "AUTHORIZED"
	OAuthRedirect   OAuthFlowResult = "REDIRECT"
)

// TokenRequestOptions are the inputs of a token request.
type TokenRequestOptions struct {
	Metadata                *AuthorizationServerMetadata
	ClientInformation       OAuthClientInformationMixed
	Resource                string
	AddClientAuthentication AddClientAuthentication
	Fetch                   mcp.McpFetch
}

func loopback(hostname string) bool {
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "[::1]" || hostname == "::1"
}

func secureEndpoint(value string) (*url.URL, error) {
	u, err := parseURL(value)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && !loopback(u.Hostname()) && !loopback(u.Host) {
		return nil, &OAuthInsecureEndpointError{Endpoint: u.String()}
	}
	return u, nil
}

func selectClientAuthMethod(information OAuthClientInformationMixed, supported []string) string {
	hinted := information.TokenEndpointAuthMethod
	if hinted != "" && slices.Contains([]string{"client_secret_basic", "client_secret_post", "none"}, hinted) &&
		(len(supported) == 0 || slices.Contains(supported, hinted)) {
		return hinted
	}
	if len(supported) == 0 {
		if information.ClientSecret != "" {
			return "client_secret_basic"
		}
		return "none"
	}
	if information.ClientSecret != "" && slices.Contains(supported, "client_secret_basic") {
		return "client_secret_basic"
	}
	if information.ClientSecret != "" && slices.Contains(supported, "client_secret_post") {
		return "client_secret_post"
	}
	if slices.Contains(supported, "none") {
		return "none"
	}
	if information.ClientSecret != "" {
		return "client_secret_post"
	}
	return "none"
}

func applyClientAuthentication(method string, information OAuthClientInformationMixed, headers http.Header, params *formParams) error {
	if method == "client_secret_basic" {
		if information.ClientSecret == "" {
			return errors.New("client_secret_basic requires a client secret")
		}
		headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(information.ClientID+":"+information.ClientSecret)))
		return nil
	}
	params.Set("client_id", information.ClientID)
	if method == "client_secret_post" && information.ClientSecret != "" {
		params.Set("client_secret", information.ClientSecret)
	}
	return nil
}

func pkce() (verifier, challenge string, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(bytes)
	digest := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

// StartAuthorizationOptions are the inputs of [StartAuthorization].
type StartAuthorizationOptions struct {
	Metadata          *AuthorizationServerMetadata
	ClientInformation OAuthClientInformationMixed
	RedirectURL       string
	Scope             string
	State             string
	Resource          string
}

// StartAuthorization builds the authorization URL with a fresh PKCE
// challenge and returns it with the code verifier.
func StartAuthorization(authorizationServerURL string, options StartAuthorizationOptions) (authorizationURL *url.URL, codeVerifier string, err error) {
	metadata := options.Metadata
	if metadata != nil && !slices.Contains(metadata.ResponseTypesSupported, "code") {
		return nil, "", errors.New("Authorization server does not support authorization codes")
	}
	if metadata != nil && metadata.CodeChallengeMethodsSupported != nil && !slices.Contains(metadata.CodeChallengeMethodsSupported, "S256") {
		return nil, "", errors.New("Authorization server does not support PKCE S256")
	}
	endpoint := ""
	if metadata != nil {
		endpoint = metadata.AuthorizationEndpoint
	} else {
		base, err := parseURL(authorizationServerURL)
		if err != nil {
			return nil, "", err
		}
		endpoint = base.ResolveReference(&url.URL{Path: "/authorize"}).String()
	}
	u, err := parseURL(endpoint)
	if err != nil {
		return nil, "", err
	}
	verifier, challenge, err := pkce()
	if err != nil {
		return nil, "", err
	}
	params := parseQuery(u.RawQuery)
	params.Set("response_type", "code")
	params.Set("client_id", options.ClientInformation.ClientID)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("redirect_uri", options.RedirectURL)
	if options.State != "" {
		params.Set("state", options.State)
	}
	if options.Scope != "" {
		params.Set("scope", options.Scope)
	}
	if slices.Contains(strings.Fields(options.Scope), "offline_access") {
		params.Set("prompt", "consent")
	}
	if options.Resource != "" {
		params.Set("resource", options.Resource)
	}
	u.RawQuery = params.Encode()
	return u, verifier, nil
}

// parseQuery reads a query string into ordered parameters.
func parseQuery(raw string) *formParams {
	p := &formParams{}
	if raw == "" {
		return p
	}
	for pair := range strings.SplitSeq(raw, "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		k, err1 := url.QueryUnescape(key)
		v, err2 := url.QueryUnescape(value)
		if err1 != nil || err2 != nil {
			k, v = key, value
		}
		p.keys, p.values = append(p.keys, k), append(p.values, v)
	}
	return p
}

func tokenRequest(ctx context.Context, authorizationServerURL string, options TokenRequestOptions, params *formParams) (*OAuthTokens, error) {
	endpoint := ""
	if options.Metadata != nil {
		endpoint = options.Metadata.TokenEndpoint
	} else {
		base, err := parseURL(authorizationServerURL)
		if err != nil {
			return nil, err
		}
		endpoint = base.ResolveReference(&url.URL{Path: "/token"}).String()
	}
	tokenURL, err := secureEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Accept", "application/json")
	headers.Set("Content-Type", "application/x-www-form-urlencoded")
	if options.Resource != "" {
		params.Set("resource", options.Resource)
	}
	if options.AddClientAuthentication != nil {
		wrapper := &TokenParams{p: *params}
		if err := options.AddClientAuthentication(ctx, headers, wrapper, tokenURL, options.Metadata); err != nil {
			return nil, err
		}
		*params = wrapper.p
	} else {
		var supported []string
		if options.Metadata != nil {
			supported = options.Metadata.TokenEndpointAuthMethodsSupported
		}
		method := selectClientAuthMethod(options.ClientInformation, supported)
		if err := applyClientAuthentication(method, options.ClientInformation, headers, params); err != nil {
			return nil, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header = headers
	//nolint:bodyclose // readBody closes the body
	response, err := fetchOrDefault(options.Fetch).Do(request)
	if err != nil {
		return nil, err
	}
	data, err := readBody(response)
	if err != nil {
		return nil, err
	}
	text := string(data)
	// Servers may report OAuth errors with any status, so check the body before the status.
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) == nil && fields != nil {
		var code string
		if raw, ok := fields["error"]; ok && len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &code) == nil {
			description, uri := code, ""
			if raw, ok := fields["error_description"]; ok && len(raw) > 0 && raw[0] == '"' {
				_ = json.Unmarshal(raw, &description)
			}
			if raw, ok := fields["error_uri"]; ok && len(raw) > 0 && raw[0] == '"' {
				_ = json.Unmarshal(raw, &uri)
			}
			return nil, &OAuthError{Code: code, Message: description, ErrorURI: uri}
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &OAuthError{Code: "server_error", Message: fmt.Sprintf("HTTP %d: %s", response.StatusCode, text)}
	}
	if !json.Valid(data) {
		data = []byte("null")
	}
	return ParseOAuthTokens(data)
}

// RegisterClientOptions are the inputs of [RegisterClient].
type RegisterClientOptions struct {
	Metadata       *AuthorizationServerMetadata
	ClientMetadata OAuthClientMetadata
	Scope          string
	Fetch          mcp.McpFetch
}

// RegisterClient registers a client dynamically (RFC 7591).
func RegisterClient(ctx context.Context, authorizationServerURL string, options RegisterClientOptions) (*OAuthClientInformationFull, error) {
	endpoint := ""
	if options.Metadata != nil {
		endpoint = options.Metadata.RegistrationEndpoint
		if endpoint == "" {
			return nil, errors.New("Authorization server does not support dynamic client registration")
		}
	} else {
		base, err := parseURL(authorizationServerURL)
		if err != nil {
			return nil, err
		}
		endpoint = base.ResolveReference(&url.URL{Path: "/register"}).String()
	}
	body, err := registrationBody(options.ClientMetadata, options.Scope)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	//nolint:bodyclose // readBody closes the body
	response, err := fetchOrDefault(options.Fetch).Do(request)
	if err != nil {
		return nil, err
	}
	data, err := readBody(response)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &OAuthRegistrationError{Status: response.StatusCode, Body: string(data)}
	}
	if !json.Valid(data) {
		return nil, errors.New("Unexpected token in JSON")
	}
	return ParseClientInformation(data)
}

// registrationBody is `{...clientMetadata, ...(scope ? {scope} : {})}`.
func registrationBody(metadata OAuthClientMetadata, scope string) ([]byte, error) {
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	if scope == "" {
		return data, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	fields["scope"] = mustJSON(scope)
	return json.Marshal(fields)
}

// ExchangeAuthorizationCodeOptions are the inputs of [ExchangeAuthorizationCode].
type ExchangeAuthorizationCodeOptions struct {
	TokenRequestOptions
	Code         string
	CodeVerifier string
	RedirectURL  string
}

// ExchangeAuthorizationCode trades an authorization code for tokens.
func ExchangeAuthorizationCode(ctx context.Context, authorizationServerURL string, options ExchangeAuthorizationCodeOptions) (*OAuthTokens, error) {
	params := &formParams{}
	params.Set("grant_type", "authorization_code")
	params.Set("code", options.Code)
	params.Set("code_verifier", options.CodeVerifier)
	params.Set("redirect_uri", options.RedirectURL)
	return tokenRequest(ctx, authorizationServerURL, options.TokenRequestOptions, params)
}

// RefreshAuthorizationOptions are the inputs of [RefreshAuthorization].
type RefreshAuthorizationOptions struct {
	TokenRequestOptions
	RefreshToken string
}

// RefreshAuthorization refreshes tokens. The result keeps the old refresh
// token when the server does not rotate it.
func RefreshAuthorization(ctx context.Context, authorizationServerURL string, options RefreshAuthorizationOptions) (*OAuthTokens, error) {
	params := &formParams{}
	params.Set("grant_type", "refresh_token")
	params.Set("refresh_token", options.RefreshToken)
	tokens, err := tokenRequest(ctx, authorizationServerURL, options.TokenRequestOptions, params)
	if err != nil {
		return nil, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = options.RefreshToken
	}
	return tokens, nil
}

// withScope records the scope of a grant whose token response omits it.
func withScope(tokens OAuthTokens, scope string) OAuthTokens {
	if tokens.Scope == "" && scope != "" {
		tokens.Scope = scope
	}
	return tokens
}

// StepUpScope returns the scopes of a step-up authorization: the challenged
// scopes plus the ones granted so far, since a challenge may list only the
// missing scopes and a token with just those would lose access the old one
// had (SEP-2350). Without challenged scopes it returns "", which lets the
// flow pick its default.
func StepUpScope(granted, challenged string) string {
	if challenged == "" {
		return ""
	}
	var scopes []string
	for _, scope := range append(scopeFields(granted), scopeFields(challenged)...) {
		if !slices.Contains(scopes, scope) {
			scopes = append(scopes, scope)
		}
	}
	return strings.Join(scopes, " ")
}

// scopeFields splits a scope list at JavaScript `\s` whitespace, which differs
// from unicode.IsSpace in U+FEFF (whitespace) and U+0085 (not whitespace).
func scopeFields(scope string) []string {
	return strings.FieldsFunc(scope, func(r rune) bool { return r == '\uFEFF' || (r != '\u0085' && unicode.IsSpace(r)) })
}

func runFlow(ctx context.Context, provider OAuthClientProvider, options OAuthFlowOptions) (OAuthFlowResult, error) {
	var metadataURL *url.URL
	if options.AuthorizationServerMetadataURL != nil {
		var err error
		if metadataURL, err = secureEndpoint(options.AuthorizationServerMetadataURL.String()); err != nil {
			return "", err
		}
	}
	var cached *OAuthDiscoveryState
	discoveryStore, hasDiscoveryStore := provider.(DiscoveryStateStore)
	// With a configured metadata URL, discovery is not cached, so changing the URL applies at once.
	if hasDiscoveryStore && metadataURL == nil {
		var err error
		if cached, err = discoveryStore.DiscoveryState(ctx); err != nil {
			return "", err
		}
	}
	var discovered OAuthServerInfo
	if cached != nil && cached.AuthorizationServerURL != "" {
		discovered = OAuthServerInfo{
			AuthorizationServerURL:      cached.AuthorizationServerURL,
			AuthorizationServerMetadata: cached.AuthorizationServerMetadata,
			ResourceMetadata:            cached.ResourceMetadata,
		}
		if discovered.AuthorizationServerMetadata == nil {
			metadata, err := DiscoverAuthorizationServerMetadata(ctx, cached.AuthorizationServerURL, DiscoveryOptions{
				Fetch: options.Fetch, SkipIssuerValidation: options.SkipIssuerValidation,
			})
			if err != nil {
				return "", err
			}
			discovered.AuthorizationServerMetadata = metadata
		}
	} else {
		resourceMetadataURL := ""
		if options.ResourceMetadataURL != nil {
			resourceMetadataURL = options.ResourceMetadataURL.String()
		}
		info, err := DiscoverOAuthServerInfo(ctx, options.ServerURL, DiscoveryOptions{
			ResourceMetadataURL: resourceMetadataURL, AuthorizationServerMetadataURL: metadataURL,
			Fetch: options.Fetch, SkipIssuerValidation: options.SkipIssuerValidation,
		})
		if err != nil {
			return "", err
		}
		discovered = *info
	}
	if hasDiscoveryStore && metadataURL == nil {
		state := OAuthDiscoveryState{
			AuthorizationServerURL: discovered.AuthorizationServerURL, AuthorizationServerMetadata: discovered.AuthorizationServerMetadata,
			ResourceMetadata: discovered.ResourceMetadata,
		}
		if options.ResourceMetadataURL != nil {
			state.ResourceMetadataURL = options.ResourceMetadataURL.String()
		}
		if err := discoveryStore.SaveDiscoveryState(ctx, state); err != nil {
			return "", err
		}
	}
	metadata := discovered.AuthorizationServerMetadata
	resource, err := SelectResource(options.ServerURL, discovered.ResourceMetadata)
	if err != nil {
		return "", err
	}
	// An empty scope (for example from `scopes_supported: []`) falls through to the next source.
	scope := options.Scope
	if scope == "" && discovered.ResourceMetadata != nil {
		scope = strings.Join(discovered.ResourceMetadata.ScopesSupported, " ")
	}
	if scope == "" {
		scope = provider.ClientMetadata().Scope
	}
	client, err := provider.ClientInformation(ctx)
	if err != nil {
		return "", err
	}
	var clientDocument *OAuthClientMetadataDocument
	if p, ok := provider.(ClientMetadataDocumentProvider); ok && client == nil {
		if clientDocument, err = p.ClientMetadataDocument(metadata); err != nil {
			return "", err
		}
	}
	if clientDocument != nil {
		u, err := parseURL(clientDocument.URL)
		if err != nil {
			return "", err
		}
		if u.Scheme != "https" || u.Path == "/" {
			return "", errors.New("Invalid OAuth client metadata URL")
		}
		client = &OAuthClientInformationMixed{ClientID: clientDocument.URL}
	}
	if client == nil {
		if options.AuthorizationCode != "" {
			return "", errors.New("OAuth client information is missing during code exchange")
		}
		saver, canSave := provider.(ClientInformationSaver)
		if !canSave {
			return "", errors.New("OAuth client information cannot be persisted")
		}
		registered, err := RegisterClient(ctx, discovered.AuthorizationServerURL, RegisterClientOptions{
			Metadata: metadata, ClientMetadata: provider.ClientMetadata(), Scope: scope, Fetch: options.Fetch,
		})
		if err != nil {
			return "", err
		}
		client = registered
		if err := saver.SaveClientInformation(ctx, *client); err != nil {
			return "", err
		}
	}
	// The document's redirect URI may differ from the provider's, for example by a server-specific path.
	redirectURL := provider.RedirectURL()
	if clientDocument != nil {
		redirectURL = clientDocument.RedirectURL
	}
	tokenOptions := TokenRequestOptions{Metadata: metadata, ClientInformation: *client, Resource: resource, Fetch: options.Fetch}
	if authenticator, ok := provider.(ClientAuthenticator); ok {
		tokenOptions.AddClientAuthentication = authenticator.AddClientAuthentication()
	}
	if options.AuthorizationCode != "" {
		// RFC 9207: never send a code from another authorization server to this one.
		iss := options.Iss
		if metadata != nil && (iss != nil || (metadata.AuthorizationResponseIssParameterSupported != nil && *metadata.AuthorizationResponseIssParameterSupported)) {
			if iss == nil || *iss != metadata.Issuer {
				return "", &OAuthIssuerMismatchError{Expected: metadata.Issuer, Received: iss}
			}
		}
		verifier, err := provider.CodeVerifier(ctx)
		if err != nil {
			return "", err
		}
		tokens, err := ExchangeAuthorizationCode(ctx, discovered.AuthorizationServerURL, ExchangeAuthorizationCodeOptions{
			TokenRequestOptions: tokenOptions, Code: options.AuthorizationCode, CodeVerifier: verifier, RedirectURL: redirectURL,
		})
		if err != nil {
			return "", err
		}
		// A response without `scope` grants the requested scope (RFC 6749 §5.1). Recorded so a step-up can keep
		// it. Callers pass the options of the authorization request, so `scope` is what was requested.
		if err := provider.SaveTokens(ctx, withScope(*tokens, scope)); err != nil {
			return "", err
		}
		return OAuthAuthorized, nil
	}
	var existing *OAuthTokens
	if !options.SkipRefresh {
		if existing, err = provider.Tokens(ctx); err != nil {
			return "", err
		}
	}
	if existing != nil && existing.RefreshToken != "" {
		tokens, err := RefreshAuthorization(ctx, discovered.AuthorizationServerURL, RefreshAuthorizationOptions{
			TokenRequestOptions: tokenOptions, RefreshToken: existing.RefreshToken,
		})
		if err == nil {
			// A refresh without `scope` keeps the scope of the grant (RFC 6749 §6).
			if err := provider.SaveTokens(ctx, withScope(*tokens, existing.Scope)); err != nil {
				return "", err
			}
			return OAuthAuthorized, nil
		}
		if _, ok := errors.AsType[*OAuthInsecureEndpointError](err); ok {
			return "", err
		}
		var oauthErr *OAuthError
		if errors.As(err, &oauthErr) && oauthErr.Code != "server_error" {
			return "", err
		}
	}
	state := ""
	if p, ok := provider.(StateProvider); ok {
		if state, err = p.State(ctx); err != nil {
			return "", err
		}
	}
	authorizationURL, verifier, err := StartAuthorization(discovered.AuthorizationServerURL, StartAuthorizationOptions{
		Metadata: metadata, ClientInformation: *client, RedirectURL: redirectURL, Scope: scope, State: state, Resource: resource,
	})
	if err != nil {
		return "", err
	}
	if err := provider.SaveCodeVerifier(ctx, verifier); err != nil {
		return "", err
	}
	if err := provider.RedirectToAuthorization(ctx, authorizationURL); err != nil {
		return "", err
	}
	return OAuthRedirect, nil
}

// AuthorizeMcp runs one step of the authorization code flow: it exchanges a
// code, refreshes stored tokens, or redirects the user. A rejected client
// invalidates all credentials and a rejected grant invalidates the tokens,
// each followed by one retry.
func AuthorizeMcp(ctx context.Context, provider OAuthClientProvider, options OAuthFlowOptions) (OAuthFlowResult, error) {
	result, err := runFlow(ctx, provider, options)
	if err == nil {
		return result, nil
	}
	if oauthErr, ok := errors.AsType[*OAuthError](err); ok {
		kind := ""
		switch oauthErr.Code {
		case "invalid_client", "unauthorized_client":
			kind = "all"
		case "invalid_grant":
			kind = "tokens"
		}
		if kind != "" {
			if invalidator, ok := provider.(CredentialInvalidator); ok {
				if err := invalidator.InvalidateCredentials(ctx, kind); err != nil {
					return "", err
				}
			}
			return runFlow(ctx, provider, options)
		}
	}
	return "", err
}

type refreshFlight struct {
	done chan struct{}
	err  error
}

type oauthAdapter struct {
	provider OAuthClientProvider
	mu       sync.Mutex
	inFlight *refreshFlight
}

// AdaptOAuthProvider returns the auth provider for a Streamable HTTP
// transport. After a 401 it refreshes the tokens, or fails with
// [McpOAuthAuthorizationRequiredError] when the user has to authorize
// (again). Concurrent 401s share one refresh, and a request whose token was
// already replaced is just retried: with rotating refresh tokens, a second
// refresh with the old refresh token would fail and discard the new grant.
func AdaptOAuthProvider(provider OAuthClientProvider) mcp.AuthProvider {
	return &oauthAdapter{provider: provider}
}

func (a *oauthAdapter) Token(ctx context.Context) (string, error) {
	tokens, err := a.provider.Tokens(ctx)
	if err != nil || tokens == nil {
		return "", err
	}
	return tokens.AccessToken, nil
}

func (a *oauthAdapter) OnUnauthorized(ctx context.Context, unauthorized mcp.UnauthorizedContext) error {
	challenge := ParseWWWAuthenticate(strings.Join(unauthorized.Response.Header.Values("Www-Authenticate"), ", "))
	insufficient := challenge.Error == "insufficient_scope"
	a.mu.Lock()
	running := a.inFlight != nil
	a.mu.Unlock()
	if !insufficient && !running && unauthorized.Token != "" {
		tokens, err := a.provider.Tokens(ctx)
		if err != nil {
			return err
		}
		if tokens != nil && tokens.AccessToken != "" && tokens.AccessToken != unauthorized.Token {
			return nil
		}
	}
	a.mu.Lock()
	flight := a.inFlight
	if flight == nil {
		flight = &refreshFlight{done: make(chan struct{})}
		a.inFlight = flight
		flowCtx := context.WithoutCancel(ctx)
		go func() {
			scope := challenge.Scope
			var result OAuthFlowResult
			var err error
			if insufficient {
				var granted *OAuthTokens
				if granted, err = a.provider.Tokens(flowCtx); err == nil {
					grantedScope := ""
					if granted != nil {
						grantedScope = granted.Scope
					}
					scope = StepUpScope(grantedScope, challenge.Scope)
				}
			}
			if err == nil {
				result, err = AuthorizeMcp(flowCtx, a.provider, OAuthFlowOptions{
					ServerURL:           unauthorized.ServerURL.String(),
					ResourceMetadataURL: challenge.ResourceMetadataURL,
					Scope:               scope,
					Fetch:               unauthorized.Fetch,
					SkipRefresh:         insufficient,
				})
			}
			if err == nil && result == OAuthRedirect {
				err = &McpOAuthAuthorizationRequiredError{}
			}
			a.mu.Lock()
			a.inFlight = nil
			a.mu.Unlock()
			flight.err = err
			close(flight.done)
		}()
	}
	a.mu.Unlock()
	select {
	case <-flight.done:
		return flight.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
