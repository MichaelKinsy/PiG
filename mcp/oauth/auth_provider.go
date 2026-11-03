package oauth

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/src/oauth/auth-provider.ts.
//
// The auth provider for connections to an OAuth-protected MCP server: it sends the stored access token and refreshes
// it, but never starts a browser flow. When the user has to sign in, it fails with
// [McpOAuthAuthorizationRequiredError] and the application runs [StartMcpSignIn].

// McpAuthProvider is an [mcp.AuthProvider] that can say when its refreshes have finished.
type McpAuthProvider interface {
	mcp.AuthProvider
	mcp.UnauthorizedHandler
	// Settled returns when no refresh is running, so shutdown does not drop rotated tokens before they are saved.
	Settled(ctx context.Context)
}

// refreshSkew: access tokens this close to expiry are refreshed before they are sent.
const refreshSkew = 30 * time.Second

// McpAuthProviderOptions configure [CreateMcpAuthProvider].
type McpAuthProviderOptions struct {
	ServerURL string
	Store     McpOAuthServerStore
	// Settings is only called when a refresh is needed, so a secret that fails to resolve fails the refresh instead of
	// the whole connection setup.
	Settings func() (McpOAuthSettings, error)
	// OnChallenge receives the server's `WWW-Authenticate` challenge so sign-in can use its resource metadata URL and
	// scope.
	OnChallenge             func(OAuthChallenge)
	ClientName              string
	ClientMetadataDocuments *McpClientMetadataDocuments
	// Fetch is the application's request function; see [McpOAuthClientOptions.Fetch].
	Fetch mcp.McpFetch
}

type authProvider struct {
	options McpAuthProviderOptions
	mu      sync.Mutex
	current *refreshFlight
}

// CreateMcpAuthProvider is the auth provider for MCP connections: it sends the stored access token and refreshes it
// when it is about to expire or after a 401. It fails with [McpOAuthAuthorizationRequiredError] when the user has to
// sign in, including when the server asks for more scope (`insufficient_scope`).
//
// Many servers rotate refresh tokens, so two refreshes with the same refresh token lose the grant. Requests in this
// process share one refresh, and other processes are kept out by the store's refresh lock, held from reading the
// tokens to saving new ones. Tokens that changed meanwhile (another process refreshed them, or the user signed in) are
// used without refreshing.
func CreateMcpAuthProvider(options McpAuthProviderOptions) McpAuthProvider {
	return &authProvider{options: options}
}

func (p *authProvider) running() *refreshFlight {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

// refresh replaces staleToken, the access token that expired or was rejected.
func (p *authProvider) refresh(ctx context.Context, staleToken string, fetch mcp.McpFetch, challenge *OAuthChallenge) error {
	p.mu.Lock()
	flight := p.current
	if flight == nil {
		flight = &refreshFlight{done: make(chan struct{})}
		p.current = flight
		flowCtx := context.WithoutCancel(ctx)
		go func() {
			err := p.options.Store.WithRefreshLock(flowCtx, func(ctx context.Context) error {
				return p.refreshLocked(ctx, staleToken, fetch, challenge)
			})
			p.mu.Lock()
			p.current = nil
			p.mu.Unlock()
			flight.err = err
			close(flight.done)
		}()
	}
	p.mu.Unlock()
	select {
	case <-flight.done:
		return flight.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (p *authProvider) refreshLocked(ctx context.Context, staleToken string, fetch mcp.McpFetch, challenge *OAuthChallenge) error {
	state, err := p.options.Store.Load(ctx)
	if err != nil {
		return err
	}
	current := ""
	if state != nil && state.Tokens != nil {
		current = state.Tokens.AccessToken
	}
	if current != staleToken {
		return nil
	}
	if state == nil || state.Tokens == nil || state.Tokens.RefreshToken == "" {
		return NewMcpOAuthAuthorizationRequiredError()
	}
	settings, err := p.options.Settings()
	if err != nil {
		return err
	}
	client := McpOAuthClientOptions{
		ServerURL: p.options.ServerURL, Store: p.options.Store, Settings: settings,
		ClientName: p.options.ClientName, ClientMetadataDocuments: p.options.ClientMetadataDocuments, Fetch: p.options.Fetch,
	}
	result, err := refreshStoredTokens(ctx, client, state, refreshOptions{fetch: fetch, challenge: challenge})
	if err != nil {
		return err
	}
	if result == OAuthRedirect {
		return NewMcpOAuthAuthorizationRequiredError()
	}
	return nil
}

// Token implements [mcp.AuthProvider].
func (p *authProvider) Token(ctx context.Context) (string, error) {
	if flight := p.running(); flight != nil {
		select {
		case <-flight.done:
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	}
	state, err := p.options.Store.Load(ctx)
	if err != nil {
		return "", err
	}
	if state == nil || state.Tokens == nil {
		return "", nil
	}
	token := state.Tokens.AccessToken
	expired := state.TokensExpireAt != nil && *state.TokensExpireAt-float64(refreshSkew.Milliseconds()) <= float64(time.Now().UnixMilli())
	if !expired || state.Tokens.RefreshToken == "" {
		return token, nil
	}
	// Failures fall through: the request goes out with the old token and a 401 decides what happens.
	_ = p.refresh(ctx, token, nil, nil)
	state, err = p.options.Store.Load(ctx)
	if err != nil || state == nil || state.Tokens == nil {
		return "", err
	}
	return state.Tokens.AccessToken, nil
}

// OnUnauthorized implements [mcp.UnauthorizedHandler].
func (p *authProvider) OnUnauthorized(ctx context.Context, unauthorized mcp.UnauthorizedContext) error {
	challenge := ParseWWWAuthenticate(jsstring.FromLatin1(strings.Join(unauthorized.Response.Header.Values("Www-Authenticate"), ", ")))
	if p.options.OnChallenge != nil {
		p.options.OnChallenge(challenge)
	}
	// A refresh keeps the granted scope, so more scope needs a new sign-in.
	if challenge.Error == "insufficient_scope" {
		return NewMcpOAuthAuthorizationRequiredError()
	}
	return p.refresh(ctx, unauthorized.Token, unauthorized.Fetch, &challenge)
}

// Settled implements [McpAuthProvider].
func (p *authProvider) Settled(ctx context.Context) {
	if flight := p.running(); flight != nil {
		select {
		case <-flight.done:
		case <-ctx.Done():
		}
	}
}

// refreshOptions are the optional inputs of [refreshStoredTokens].
type refreshOptions struct {
	fetch     mcp.McpFetch
	challenge *OAuthChallenge
}

// refreshStoredTokens refreshes the tokens of state with the stored client (or the document it signed in with), or
// reports [OAuthRedirect] when that is not possible. It never registers a client or prepares an authorization request,
// so a pending sign-in stays usable. Call it while holding the store's refresh lock. It is not cancellable: a refresh
// the server answered may have rotated the refresh token, so its answer must be saved.
func refreshStoredTokens(ctx context.Context, client McpOAuthClientOptions, state *McpOAuthState, options refreshOptions) (OAuthFlowResult, error) {
	// Refreshing never redirects the user; the redirect URI only picks the client metadata.
	loopback, err := loopbackSettings(client.Settings)
	if err != nil {
		return "", err
	}
	redirectURL := loopback.fixedRedirectURL
	if redirectURL == "" {
		if registered := registeredRedirectURLs(state.ClientInformation); len(registered) > 0 {
			redirectURL = registered[0]
		}
	}
	if redirectURL == "" {
		redirectURL = defaultRedirectURL
	}
	provider, err := createOAuthClientProvider(client, redirectURL, nil)
	if err != nil {
		return "", err
	}
	fetch := options.fetch
	if fetch == nil {
		fetch = client.Fetch
	}
	flow := OAuthFlowOptions{
		ServerURL: client.ServerURL, AuthorizationServerMetadataURL: client.Settings.AuthServerMetadataURL,
		Fetch: timedFetch(fetch), RefreshOnly: true,
	}
	if options.challenge != nil {
		flow.ResourceMetadataURL = options.challenge.ResourceMetadataURL
	}
	return AuthorizeMcp(ctx, provider, flow)
}
