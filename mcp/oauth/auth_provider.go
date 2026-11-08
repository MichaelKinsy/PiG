package oauth

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/src/oauth/auth-provider.ts (Pi bb266bf35).
//
// The auth provider for connections to an OAuth-protected MCP server: it sends the stored access token and refreshes it, but never starts a
// browser flow. When the user has to sign in, it fails with [McpOAuthAuthorizationRequiredError] and the application runs [StartMcpSignIn].

// McpAuthProvider is an auth provider that can say when its refreshes have finished.
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
	// Settings is only called when a refresh is needed, so a secret that fails to resolve fails the refresh instead of the whole connection setup.
	Settings func() (McpOAuthSettings, error)
	// ClientName is the `client_name` for dynamic client registration unless the settings name another: the application.
	ClientName              string
	ClientMetadataDocuments *McpClientMetadataDocuments
	// Fetch is the default of the requests to the authorization server; see [McpOAuthClientOptions.Fetch].
	Fetch mcp.McpFetch
	// OnChallenge receives the server's `WWW-Authenticate` challenge so sign-in can use its resource metadata URL and scope.
	OnChallenge func(OAuthChallenge)
}

type refreshRun struct {
	done chan struct{}
	err  error
}

type authProvider struct {
	options McpAuthProviderOptions
	mu      sync.Mutex
	current *refreshRun
}

// CreateMcpAuthProvider returns the auth provider for MCP connections: it sends the stored access token and refreshes it when it is about to
// expire or after a 401. It fails with [McpOAuthAuthorizationRequiredError] when the user has to sign in, including when the server asks for more
// scope (`insufficient_scope`).
//
// Many servers rotate refresh tokens, so two refreshes with the same refresh token lose the grant. Requests in this process share one refresh,
// and other processes are kept out by the store's refresh lock, held from reading the tokens to saving new ones. Tokens that changed meanwhile
// (another process refreshed them, or the user signed in) are used without refreshing.
func CreateMcpAuthProvider(options McpAuthProviderOptions) McpAuthProvider {
	return &authProvider{options: options}
}

func (p *authProvider) running() *refreshRun {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

// refresh replaces staleToken, the access token that expired or was rejected.
func (p *authProvider) refresh(ctx context.Context, staleToken string, fetch mcp.McpFetch, challenge *OAuthChallenge) error {
	p.mu.Lock()
	run := p.current
	if run == nil {
		run = &refreshRun{done: make(chan struct{})}
		p.current = run
		// A refresh the server answered may have rotated the refresh token, so its answer must be saved: it does not end with the request.
		runCtx := context.WithoutCancel(ctx)
		go func() {
			err := p.options.Store.WithRefreshLock(runCtx, func(ctx context.Context) error {
				return p.refreshLocked(ctx, staleToken, fetch, challenge)
			})
			p.mu.Lock()
			p.current = nil
			p.mu.Unlock()
			run.err = err
			close(run.done)
		}()
	}
	p.mu.Unlock()
	select {
	case <-run.done:
		return run.err
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
		ServerURL: p.options.ServerURL, Store: p.options.Store, Settings: settings, ClientName: p.options.ClientName,
		ClientMetadataDocuments: p.options.ClientMetadataDocuments, Fetch: p.options.Fetch,
	}
	result, err := RefreshStoredTokens(ctx, client, *state, RefreshStoredTokensOptions{Fetch: fetch, Challenge: challenge})
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
	if run := p.running(); run != nil {
		select {
		case <-run.done:
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
	if run := p.running(); run != nil {
		select {
		case <-run.done:
		case <-ctx.Done():
		}
	}
}

// RefreshStoredTokensOptions are the optional inputs of [RefreshStoredTokens].
type RefreshStoredTokensOptions struct {
	Fetch     mcp.McpFetch
	Challenge *OAuthChallenge
}

// RefreshStoredTokens refreshes the tokens of state with the stored client (or the document it signed in with), or reports [OAuthRedirect] when
// that is not possible. It never registers a client or prepares an authorization request, so a pending sign-in stays usable. Call it while
// holding the store's refresh lock. Not cancellable: a refresh the server answered may have rotated the refresh token, so its answer must be
// saved; pass a context that does not end with the request.
func RefreshStoredTokens(ctx context.Context, client McpOAuthClientOptions, state McpOAuthState, options RefreshStoredTokensOptions) (OAuthFlowResult, error) {
	// Refreshing never redirects the user; the redirect URI only picks the client metadata.
	loopbackSettings, err := GetLoopbackSettings(client.Settings)
	if err != nil {
		return "", err
	}
	redirectURL := loopbackSettings.FixedRedirectURL
	if redirectURL == "" {
		if registered := RegisteredRedirectURLs(state.ClientInformation); len(registered) > 0 {
			redirectURL = registered[0]
		}
	}
	if redirectURL == "" {
		redirectURL = DefaultRedirectURL
	}
	provider, err := CreateOAuthClientProvider(client, redirectURL, nil)
	if err != nil {
		return "", err
	}
	fetch := options.Fetch
	if fetch == nil {
		fetch = client.Fetch
	}
	flow := OAuthFlowOptions{
		ServerURL: client.ServerURL, AuthorizationServerMetadataURL: client.Settings.AuthServerMetadataURL,
		Fetch: TimedFetch(fetch), RefreshOnly: true,
	}
	if options.Challenge != nil {
		flow.ResourceMetadataURL = options.Challenge.ResourceMetadataURL
	}
	return AuthorizeMcp(ctx, provider, flow)
}
