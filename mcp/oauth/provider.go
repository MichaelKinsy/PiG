package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sync"
	"time"
)

// Ports packages/mcp/src/oauth/provider.ts.
//
// Adapted from modelcontextprotocol/typescript-sdk v1.29.0 src/client/auth.ts.

// McpOAuthState is everything stored for one MCP server URL.
type McpOAuthState struct {
	ServerURL         string                  `json:"serverUrl"`
	ClientInformation *OAuthClientInformation `json:"clientInformation,omitempty"`
	Tokens            *OAuthTokens            `json:"tokens,omitempty"`
	// TokensExpireAt is when the access token expires, in milliseconds since
	// the epoch, from expires_in at the time it was saved.
	TokensExpireAt *int64               `json:"tokensExpireAt,omitempty"`
	CodeVerifier   string               `json:"codeVerifier,omitempty"`
	OAuthState     string               `json:"oauthState,omitempty"`
	Discovery      *OAuthDiscoveryState `json:"discovery,omitempty"`
}

// Clone returns a deep copy.
func (s McpOAuthState) Clone() McpOAuthState {
	data, err := json.Marshal(s)
	if err != nil {
		return s
	}
	var out McpOAuthState
	if json.Unmarshal(data, &out) != nil {
		return s
	}
	return out
}

// McpOAuthStateStore stores the state of one provider. Applications inject
// durable storage.
type McpOAuthStateStore interface {
	// Load returns the stored state, or nil.
	Load(ctx context.Context) (*McpOAuthState, error)
	Save(ctx context.Context, state McpOAuthState) error
}

// MemoryOAuthStateStore keeps the state in memory.
type MemoryOAuthStateStore struct {
	mu    sync.Mutex
	value *McpOAuthState
}

// Load returns a copy of the stored state.
func (m *MemoryOAuthStateStore) Load(context.Context) (*McpOAuthState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.value == nil {
		return nil, nil
	}
	clone := m.value.Clone()
	return &clone, nil
}

// Save stores a copy of the state.
func (m *MemoryOAuthStateStore) Save(_ context.Context, state McpOAuthState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := state.Clone()
	m.value = &clone
	return nil
}

// McpOAuthProviderOptions configure a [McpOAuthProvider].
type McpOAuthProviderOptions struct {
	ServerURL   string
	RedirectURL string
	// ClientMetadata's RedirectURIs, GrantTypes, ResponseTypes, and
	// TokenEndpointAuthMethod default from the redirect URL and client secret.
	ClientMetadata OAuthClientMetadata
	ClientID       string
	ClientSecret   string
	Store          McpOAuthStateStore
	OnRedirect     func(ctx context.Context, url *url.URL) error
}

// McpOAuthProvider is the default stateful [OAuthClientProvider] for one exact
// MCP server URL. Writes are serialized. State stored for another server URL is
// ignored, so credentials never leak across servers. As in provider.ts, a failed
// write fails every later load and update with the same error and the store is
// not touched again; a failed load outside an update fails only that call.
type McpOAuthProvider struct {
	redirectURL      string
	clientMetadata   OAuthClientMetadata
	serverURL        string
	configuredClient *OAuthClientInformation
	store            McpOAuthStateStore
	onRedirect       func(ctx context.Context, url *url.URL) error
	mu               sync.Mutex
	// writeErr is the error that rejected the write chain: provider.ts keeps `this.writes` rejected, so every later load and update fails with it.
	writeErr error
}

// NewMcpOAuthProvider returns a provider. It fails when the server URL does not parse.
func NewMcpOAuthProvider(options McpOAuthProviderOptions) (*McpOAuthProvider, error) {
	server, err := parseURL(options.ServerURL)
	if err != nil {
		return nil, err
	}
	p := &McpOAuthProvider{
		redirectURL: options.RedirectURL,
		serverURL:   server.String(),
		store:       options.Store,
		onRedirect:  options.OnRedirect,
	}
	metadata := options.ClientMetadata
	if metadata.RedirectURIs == nil {
		metadata.RedirectURIs = []string{p.redirectURL}
	}
	if metadata.GrantTypes == nil {
		metadata.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if metadata.ResponseTypes == nil {
		metadata.ResponseTypes = []string{"code"}
	}
	if metadata.TokenEndpointAuthMethod == "" {
		metadata.TokenEndpointAuthMethod = "none"
		if options.ClientSecret != "" {
			metadata.TokenEndpointAuthMethod = "client_secret_post"
		}
	}
	p.clientMetadata = metadata
	if options.ClientID != "" {
		p.configuredClient = &OAuthClientInformation{ClientID: options.ClientID, ClientSecret: options.ClientSecret}
	}
	if p.store == nil {
		p.store = &MemoryOAuthStateStore{}
	}
	return p, nil
}

// RedirectURL is the redirect URI.
func (p *McpOAuthProvider) RedirectURL() string { return p.redirectURL }

// ClientMetadata is the metadata registered for dynamic registration.
func (p *McpOAuthProvider) ClientMetadata() OAuthClientMetadata { return p.clientMetadata }

// State returns the OAuth state parameter, creating one on first use.
func (p *McpOAuthProvider) State(ctx context.Context) (string, error) {
	state, err := p.load(ctx)
	if err != nil {
		return "", err
	}
	if state.OAuthState != "" {
		return state.OAuthState, nil
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	value := hex.EncodeToString(bytes)
	if err := p.update(ctx, func(s *McpOAuthState) { s.OAuthState = value }); err != nil {
		return "", err
	}
	return value, nil
}

// ClientInformation returns the configured client, else the registered one.
func (p *McpOAuthProvider) ClientInformation(ctx context.Context) (*OAuthClientInformationMixed, error) {
	if p.configuredClient != nil {
		clone := *p.configuredClient
		return &clone, nil
	}
	state, err := p.load(ctx)
	if err != nil {
		return nil, err
	}
	return state.ClientInformation, nil
}

// SaveClientInformation stores a registered client, unless a client is configured.
func (p *McpOAuthProvider) SaveClientInformation(ctx context.Context, information OAuthClientInformationMixed) error {
	if p.configuredClient != nil {
		return nil
	}
	return p.update(ctx, func(s *McpOAuthState) { s.ClientInformation = &information })
}

// Tokens returns the stored tokens.
func (p *McpOAuthProvider) Tokens(ctx context.Context) (*OAuthTokens, error) {
	state, err := p.load(ctx)
	if err != nil {
		return nil, err
	}
	return state.Tokens, nil
}

// SaveTokens stores tokens and their expiry.
func (p *McpOAuthProvider) SaveTokens(ctx context.Context, tokens OAuthTokens) error {
	var expiresAt *int64
	if tokens.ExpiresIn != nil {
		value := time.Now().UnixMilli() + int64(*tokens.ExpiresIn*1000)
		expiresAt = &value
	}
	return p.update(ctx, func(s *McpOAuthState) {
		s.Tokens = &tokens
		s.TokensExpireAt = expiresAt
	})
}

// RedirectToAuthorization hands the authorization URL to the application.
func (p *McpOAuthProvider) RedirectToAuthorization(ctx context.Context, authorizationURL *url.URL) error {
	return p.onRedirect(ctx, authorizationURL)
}

// SaveCodeVerifier stores the PKCE verifier.
func (p *McpOAuthProvider) SaveCodeVerifier(ctx context.Context, verifier string) error {
	return p.update(ctx, func(s *McpOAuthState) { s.CodeVerifier = verifier })
}

// CodeVerifier returns the stored PKCE verifier.
func (p *McpOAuthProvider) CodeVerifier(ctx context.Context) (string, error) {
	state, err := p.load(ctx)
	if err != nil {
		return "", err
	}
	if state.CodeVerifier == "" {
		return "", errNoVerifier
	}
	return state.CodeVerifier, nil
}

type stringError string

func (e stringError) Error() string { return string(e) }

const errNoVerifier = stringError("No OAuth PKCE code verifier is stored")

// InvalidateCredentials drops stored credentials: "all", "client", "tokens",
// "verifier", or "discovery".
func (p *McpOAuthProvider) InvalidateCredentials(ctx context.Context, kind string) error {
	return p.update(ctx, func(s *McpOAuthState) {
		if kind == "all" || kind == "client" {
			s.ClientInformation = nil
		}
		if kind == "all" || kind == "tokens" {
			s.Tokens, s.TokensExpireAt = nil, nil
		}
		if kind == "all" || kind == "verifier" {
			s.CodeVerifier = ""
		}
		if kind == "all" || kind == "discovery" {
			s.Discovery = nil
		}
		if kind == "all" {
			s.OAuthState = ""
		}
	})
}

// SaveDiscoveryState caches discovery.
func (p *McpOAuthProvider) SaveDiscoveryState(ctx context.Context, discovery OAuthDiscoveryState) error {
	return p.update(ctx, func(s *McpOAuthState) { s.Discovery = &discovery })
}

// DiscoveryState returns the cached discovery.
func (p *McpOAuthProvider) DiscoveryState(ctx context.Context) (*OAuthDiscoveryState, error) {
	state, err := p.load(ctx)
	if err != nil {
		return nil, err
	}
	return state.Discovery, nil
}

func (p *McpOAuthProvider) load(ctx context.Context) (McpOAuthState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.writeErr != nil {
		return McpOAuthState{}, p.writeErr
	}
	return p.loadLocked(ctx)
}

func (p *McpOAuthProvider) loadLocked(ctx context.Context) (McpOAuthState, error) {
	state, err := p.store.Load(ctx)
	if err != nil {
		return McpOAuthState{}, err
	}
	return p.own(state), nil
}

// update is provider.ts update: the load and the save of one change run in the write chain, and an error from either rejects the chain for good.
func (p *McpOAuthProvider) update(ctx context.Context, change func(*McpOAuthState)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.writeErr != nil {
		return p.writeErr
	}
	state, err := p.loadLocked(ctx)
	if err == nil {
		change(&state)
		err = p.store.Save(ctx, state)
	}
	if err != nil && ctx.Err() == nil {
		// The caller's cancellation has no upstream counterpart (store calls take no signal), so it is not a rejected write.
		p.writeErr = err
	}
	return err
}

// own ignores stored state for another server URL.
func (p *McpOAuthProvider) own(state *McpOAuthState) McpOAuthState {
	if state != nil && state.ServerURL == p.serverURL {
		return *state
	}
	return McpOAuthState{ServerURL: p.serverURL}
}
