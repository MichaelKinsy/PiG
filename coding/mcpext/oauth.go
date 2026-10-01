package mcpext

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/pilock"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/coding-agent/src/extensions/mcp/oauth.ts.
//
// OAuth sign-in for remote MCP servers. Connections never start a browser flow
// on their own. They send the stored access token and, after a 401, try the
// stored refresh token. When that is not possible they fail with
// [oauth.McpOAuthAuthorizationRequiredError], and the user signs in through
// `/mcp`, which runs the authorization code flow (PKCE, dynamic client
// registration) against a loopback callback.
//
// Credentials live in `<agent-dir>/mcp-auth.json`, keyed by server URL.

const (
	callbackHost = "127.0.0.1"
	callbackPath = "/callback"
	// fallbackRedirectURL is the redirect URI for refreshes when none is
	// stored. Refreshing never redirects the user.
	fallbackRedirectURL = "http://" + callbackHost + callbackPath
	// refreshSkew: access tokens this close to expiry are refreshed before they are sent.
	// upstream: packages/coding-agent/src/extensions/mcp/oauth.ts:REFRESH_SKEW_MS
	refreshSkew = 30 * time.Second
	// refreshRequestTimeout bounds each request of a refresh, so it cannot hold
	// the refresh lock or delay shutdown for long.
	// upstream: packages/coding-agent/src/extensions/mcp/oauth.ts:REFRESH_REQUEST_TIMEOUT_MS
	refreshRequestTimeout = 15 * time.Second
	// refreshLockStale: a refresh lock that its holder stopped renewing (the
	// process was killed) is taken over after this.
	// upstream: packages/coding-agent/src/extensions/mcp/oauth.ts:REFRESH_LOCK_STALE_MS
	refreshLockStale = 20 * time.Second
	// refreshLockWait is how long to wait for another process's refresh: longer
	// than a stale lock lives.
	// upstream: packages/coding-agent/src/extensions/mcp/oauth.ts:REFRESH_LOCK_WAIT_MS
	refreshLockWait = 25 * time.Second
	// upstream: packages/coding-agent/src/extensions/mcp/oauth.ts:REFRESH_LOCK_RETRY_MS
	refreshLockRetry = 100 * time.Millisecond
)

// McpOAuthSettings are the OAuth settings of one server, resolved.
type McpOAuthSettings struct {
	ClientID string
	// ClientSecret is already resolved.
	ClientSecret string
	CallbackPort *int
	// CallbackURL is a loopback redirect URI; see [extension.McpOAuthConfig].
	CallbackURL string
	// Scope lists the scopes to request, separated by spaces.
	Scope string
	// ClientName is the `client_name` of dynamic client registration. Default: the app name.
	ClientName string
}

// callbackSettings is where the loopback callback server listens and the
// redirect URI it serves.
type callbackSettings struct {
	// host is the address to listen on.
	host string
	// redirectHost is the host name in the redirect URI.
	redirectHost string
	port         *int
	path         string
	// fixedRedirectURL is the exact redirect URI, when the port is fixed.
	fixedRedirectURL string
}

func newCallbackSettings(settings McpOAuthSettings) (callbackSettings, error) {
	raw := settings.CallbackURL
	if raw == "" {
		raw = "http://" + callbackHost + callbackPath
	}
	u, err := url.Parse(raw)
	if err != nil {
		return callbackSettings{}, err
	}
	address := strings.TrimSuffix(strings.TrimPrefix(u.Hostname(), "["), "]")
	var port *int
	fixed := ""
	if u.Port() != "" {
		p, _ := strconv.Atoi(u.Port())
		port = &p
		// A configured URI with a port is sent exactly as written, since servers compare it as a string.
		fixed = settings.CallbackURL
	} else if settings.CallbackPort != nil {
		port = settings.CallbackPort
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		fixed = u.Scheme + "://" + host + ":" + strconv.Itoa(*port) + pathOrSlash(u.EscapedPath())
	}
	host := address
	// `localhost` is served on 127.0.0.1; browsers fall back to it when ::1 refuses.
	if address == "localhost" {
		host = callbackHost
	}
	return callbackSettings{host: host, redirectHost: address, port: port, path: pathOrSlash(u.EscapedPath()), fixedRedirectURL: fixed}, nil
}

func pathOrSlash(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// mergeScopes returns the scopes of both lists, each once.
func mergeScopes(scopes ...string) string {
	var merged []string
	for _, scope := range scopes {
		for s := range strings.FieldsSeq(scope) {
			if !slices.Contains(merged, s) {
				merged = append(merged, s)
			}
		}
	}
	return strings.Join(merged, " ")
}

// McpOAuthServerStore is the state store of one server plus its refresh lock.
type McpOAuthServerStore interface {
	oauth.McpOAuthStateStore
	// WithRefreshLock runs fn while no other process refreshes the server's tokens.
	WithRefreshLock(ctx context.Context, fn func(ctx context.Context) error) error
}

// McpOAuthCredentialStore keeps per-server OAuth state (client registration,
// tokens, pending PKCE verifier) in `mcp-auth.json`.
type McpOAuthCredentialStore struct {
	backend AuthStorageBackend
	// lockDir holds the refresh lock files. Without one, refreshes are only
	// serialized in this process.
	lockDir string
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
}

// NewMcpOAuthCredentialStore returns a store over the file at
// `<agentDir>/mcp-auth.json`, with refresh locks in agentDir.
func NewMcpOAuthCredentialStore(agentDir string) *McpOAuthCredentialStore {
	return &McpOAuthCredentialStore{
		backend: NewFileAuthStorageBackend(agentDir + string(os.PathSeparator) + "mcp-auth.json"),
		lockDir: agentDir,
	}
}

// NewMcpOAuthCredentialStoreWithBackend returns a store over backend. Refresh
// locks live in lockDir; an empty lockDir serializes refreshes in this process
// only.
func NewMcpOAuthCredentialStoreWithBackend(backend AuthStorageBackend, lockDir string) *McpOAuthCredentialStore {
	return &McpOAuthCredentialStore{backend: backend, lockDir: lockDir}
}

// storedStates is the parsed `mcp-auth.json`, keyed by server URL in the
// file's order, as a JavaScript object keeps it.
type storedStates = *orderedjson.Object

func parseStates(content string) (storedStates, error) {
	if strings.TrimSpace(content) == "" {
		return orderedjson.New(), nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, err
	}
	if _, ok := parsed.(map[string]any); !ok {
		return orderedjson.New(), nil
	}
	return orderedjson.Parse([]byte(content))
}

func serverKey(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("Invalid URL: %s", serverURL)
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

type serverStore struct {
	store *McpOAuthCredentialStore
	key   string
}

// ForServer returns the store of one server URL.
func (c *McpOAuthCredentialStore) ForServer(serverURL string) (McpOAuthServerStore, error) {
	key, err := serverKey(serverURL)
	if err != nil {
		return nil, err
	}
	return &serverStore{store: c, key: key}, nil
}

func (s *serverStore) Load(context.Context) (*oauth.McpOAuthState, error) {
	states, err := s.store.read()
	if err != nil {
		return nil, err
	}
	raw, ok := states.Get(s.key)
	if !ok {
		return nil, nil
	}
	var state oauth.McpOAuthState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *serverStore) Save(_ context.Context, state oauth.McpOAuthState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.store.write(func(states storedStates) { states.Set(s.key, raw) })
}

func (s *serverStore) WithRefreshLock(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.store.withRefreshLock(ctx, s.key, fn)
}

// withRefreshLock takes a lock file per server. When the process exits, its
// locks are removed; when it is killed, the lock goes stale because it is no
// longer renewed, and the next process takes it over.
func (c *McpOAuthCredentialStore) withRefreshLock(ctx context.Context, key string, fn func(ctx context.Context) error) error {
	if c.lockDir == "" {
		c.mu.Lock()
		if c.locks == nil {
			c.locks = map[string]*sync.Mutex{}
		}
		lock := c.locks[key]
		if lock == nil {
			lock = &sync.Mutex{}
			c.locks[key] = lock
		}
		c.mu.Unlock()
		lock.Lock()
		defer lock.Unlock()
		return fn(ctx)
	}
	if err := os.MkdirAll(c.lockDir, 0o700); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(key))
	lockPath := c.lockDir + string(os.PathSeparator) + "mcp-auth-refresh-" + hex.EncodeToString(digest[:])[:16]
	lock, err := pilock.AcquireWithOptions(ctx, lockPath, pilock.AcquireOptions{
		Stale: refreshLockStale, Update: refreshLockStale / 2, Retry: refreshLockRetry, Wait: refreshLockWait,
		// The default throws from a timer. A lost lock at worst lets two refreshes overlap.
		OnCompromised: func(error) {},
	})
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	return fn(ctx)
}

// Tokens returns the stored tokens of a server, for noticing sign-ins done by
// another process.
func (c *McpOAuthCredentialStore) Tokens(serverURL string) *oauth.OAuthTokens {
	key, err := serverKey(serverURL)
	if err != nil {
		return nil
	}
	states, err := c.read()
	if err != nil {
		return nil
	}
	var state oauth.McpOAuthState
	if raw, ok := states.Get(key); !ok || json.Unmarshal(raw, &state) != nil {
		return nil
	}
	return state.Tokens
}

// Remove deletes the stored state of a server. It returns whether credentials
// were stored for it.
func (c *McpOAuthCredentialStore) Remove(serverURL string) (bool, error) {
	key, err := serverKey(serverURL)
	if err != nil {
		return false, err
	}
	states, err := c.read()
	if err != nil {
		return false, err
	}
	if !states.Has(key) {
		return false, nil
	}
	err = c.write(func(states storedStates) { states.Delete(key) })
	return err == nil, err
}

func (c *McpOAuthCredentialStore) read() (storedStates, error) {
	var states storedStates
	err := c.backend.WithLock(func(current string, _ bool) (*string, error) {
		var err error
		states, err = parseStates(current)
		return nil, err
	})
	return states, err
}

func (c *McpOAuthCredentialStore) write(update func(storedStates)) error {
	return c.backend.WithLock(func(current string, _ bool) (*string, error) {
		states, err := parseStates(current)
		if err != nil {
			return nil, err
		}
		update(states)
		data, err := marshalStates(states)
		if err != nil {
			return nil, err
		}
		next := data + "\n"
		return &next, nil
	})
}

// marshalStates is JSON.stringify(states, null, 2): keys in insertion order,
// and strings escaped as JSON.stringify escapes them.
func marshalStates(states storedStates) (string, error) {
	raw, err := states.MarshalJSON()
	if err != nil {
		return "", err
	}
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, canonical, "", "  "); err != nil {
		return "", err
	}
	return out.String(), nil
}

func registeredRedirectURLs(client *oauth.OAuthClientInformationMixed) []string {
	if client == nil {
		return nil
	}
	return client.RedirectURIs
}

func createProvider(serverURL string, store oauth.McpOAuthStateStore, settings McpOAuthSettings, redirectURL, appName string, onRedirect func(*url.URL)) (*oauth.McpOAuthProvider, error) {
	return oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
		ServerURL:      serverURL,
		RedirectURL:    redirectURL,
		ClientMetadata: oauth.OAuthClientMetadata{ClientName: cmp.Or(settings.ClientName, appName)},
		ClientID:       settings.ClientID,
		ClientSecret:   settings.ClientSecret,
		Store:          store,
		OnRedirect: func(_ context.Context, u *url.URL) error {
			onRedirect(u)
			return nil
		},
	})
}

// McpAuthProvider is the auth provider of MCP connections.
type McpAuthProvider interface {
	mcp.AuthProvider
	mcp.UnauthorizedHandler
	// Settled returns when no refresh is running, so shutdown does not drop
	// rotated tokens before they are saved.
	Settled(ctx context.Context)
}

// McpAuthProviderOptions configure [NewMcpAuthProvider].
type McpAuthProviderOptions struct {
	ServerURL string
	Store     McpOAuthServerStore
	// Settings is only called when a refresh is needed, so a secret that fails
	// to resolve fails the refresh instead of the whole connection setup.
	Settings func() (McpOAuthSettings, error)
	// OnChallenge receives the server's WWW-Authenticate challenge so sign-in
	// can use its resource metadata URL and scope.
	OnChallenge func(oauth.OAuthChallenge)
	// AppName is the OAuth client name. Default: "pig".
	AppName string
}

type refreshFlight struct {
	done chan struct{}
	err  error
}

type authProvider struct {
	options McpAuthProviderOptions
	mu      sync.Mutex
	current *refreshFlight
}

// NewMcpAuthProvider returns the auth provider for MCP connections: it sends
// the stored access token and refreshes it when it is about to expire or after
// a 401. It fails with [oauth.McpOAuthAuthorizationRequiredError] when the user
// has to sign in, including when the server asks for more scope
// (`insufficient_scope`).
//
// Many servers rotate refresh tokens, so two refreshes with the same refresh
// token lose the grant. Requests in this process share one refresh, and other
// processes are kept out by the store's refresh lock, held from reading the
// tokens to saving new ones. Tokens that changed meanwhile (another process
// refreshed them, or the user signed in) are used without refreshing.
func NewMcpAuthProvider(options McpAuthProviderOptions) McpAuthProvider {
	if options.AppName == "" {
		options.AppName = "pig"
	}
	return &authProvider{options: options}
}

func (p *authProvider) running() *refreshFlight {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

type timeoutFetch struct {
	inner   mcp.McpFetch
	timeout time.Duration
}

func (t timeoutFetch) Do(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(request.Context(), t.timeout)
	response, err := t.inner.Do(request.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &closeBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type closeBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *closeBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// refresh replaces staleToken, the access token that expired or was rejected.
func (p *authProvider) refresh(ctx context.Context, staleToken string, fetch mcp.McpFetch, challenge *oauth.OAuthChallenge) error {
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

func (p *authProvider) refreshLocked(ctx context.Context, staleToken string, fetch mcp.McpFetch, challenge *oauth.OAuthChallenge) error {
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
		return &oauth.McpOAuthAuthorizationRequiredError{}
	}
	settings, err := p.options.Settings()
	if err != nil {
		return err
	}
	callback, err := newCallbackSettings(settings)
	if err != nil {
		return err
	}
	redirectURL := callback.fixedRedirectURL
	if redirectURL == "" {
		if registered := registeredRedirectURLs(state.ClientInformation); len(registered) > 0 {
			redirectURL = registered[0]
		}
	}
	if redirectURL == "" {
		redirectURL = fallbackRedirectURL
	}
	provider, err := createProvider(p.options.ServerURL, p.options.Store, settings, redirectURL, p.options.AppName, func(*url.URL) {})
	if err != nil {
		return err
	}
	flow := oauth.OAuthFlowOptions{ServerURL: p.options.ServerURL, Fetch: timeoutFetch{inner: orDefaultFetch(fetch), timeout: refreshRequestTimeout}}
	if challenge != nil {
		flow.ResourceMetadataURL, flow.Scope = challenge.ResourceMetadataURL, challenge.Scope
	}
	// Refreshes the tokens, or reports that a new sign-in is needed.
	result, err := oauth.AuthorizeMcp(ctx, provider, flow)
	if err != nil {
		return err
	}
	if result == oauth.OAuthRedirect {
		return &oauth.McpOAuthAuthorizationRequiredError{}
	}
	return nil
}

func orDefaultFetch(fetch mcp.McpFetch) mcp.McpFetch {
	if fetch == nil {
		return http.DefaultClient
	}
	return fetch
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
	expired := state.TokensExpireAt != nil && *state.TokensExpireAt-refreshSkew.Milliseconds() <= time.Now().UnixMilli()
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
	challenge := oauth.ParseWWWAuthenticate(strings.Join(unauthorized.Response.Header.Values("Www-Authenticate"), ", "))
	if p.options.OnChallenge != nil {
		p.options.OnChallenge(challenge)
	}
	// A refresh keeps the granted scope, so more scope needs a new sign-in.
	if challenge.Error == "insufficient_scope" {
		return &oauth.McpOAuthAuthorizationRequiredError{}
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

// McpSignInPrompt is how a sign-in reaches the user.
type McpSignInPrompt interface {
	// ShowAuthorizationURL shows the authorization URL to the user and opens it
	// in a browser.
	ShowAuthorizationURL(u *url.URL)
	// PromptForRedirectURL asks for the redirect URL from the browser address
	// bar, for when the browser cannot reach the loopback callback (for example
	// over SSH). The context ends once the callback arrives. It returns an
	// empty string when the user cancels.
	PromptForRedirectURL(ctx context.Context) (string, error)
}

// McpSignInCancelledError reports a cancelled sign-in.
type McpSignInCancelledError struct{}

func (e *McpSignInCancelledError) Error() string { return "Sign-in cancelled" }

func codeFromRedirectURL(input, state string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(input))
	if err != nil || u.Scheme == "" {
		return "", errors.New("Expected the full redirect URL from the browser address bar")
	}
	query := u.Query()
	if failure := query.Get("error"); failure != "" {
		if query.Has("error_description") {
			return "", errors.New(query.Get("error_description"))
		}
		return "", errors.New(failure)
	}
	if query.Get("state") != state {
		return "", errors.New("The redirect URL belongs to a different sign-in")
	}
	code := query.Get("code")
	if code == "" {
		return "", errors.New("The redirect URL does not contain an authorization code")
	}
	return code, nil
}

type codeResult struct {
	code string
	err  error
}

// waitForAuthorizationCode waits for the browser callback or a pasted
// redirect URL, whichever comes first. The prompt runs on a goroutine that
// ends when the prompt returns; its result is dropped once the browser wins.
// The callers register wait before they show the authorization URL: a browser
// can reach the callback before this function runs.
func waitForAuthorizationCode(ctx context.Context, wait *oauth.CallbackWait, state string, prompt McpSignInPrompt) (string, error) {
	promptCtx, cancelPrompt := context.WithCancel(ctx)
	defer cancelPrompt()
	fromBrowser := make(chan codeResult, 1)
	go func() {
		received, err := wait.Wait(promptCtx)
		fromBrowser <- codeResult{received.Code, err}
	}()
	fromUser := make(chan codeResult, 1)
	go func() {
		input, err := prompt.PromptForRedirectURL(promptCtx)
		if err != nil {
			fromUser <- codeResult{err: err}
			return
		}
		if strings.TrimSpace(input) == "" {
			fromUser <- codeResult{err: &McpSignInCancelledError{}}
			return
		}
		code, err := codeFromRedirectURL(input, state)
		fromUser <- codeResult{code, err}
	}()
	select {
	case r := <-fromBrowser:
		return r.code, r.err
	case r := <-fromUser:
		return r.code, r.err
	case <-ctx.Done():
		return "", context.Cause(ctx)
	}
}

// listenForCallback listens on port, or on a free port when it is taken and
// not required.
func listenForCallback(settings callbackSettings, port *int, required bool) (*oauth.OAuthCallbackServer, error) {
	options := oauth.OAuthCallbackServerOptions{
		Host: settings.host, RedirectHost: settings.redirectHost, Path: settings.path,
		RenderPage: func(page oauth.OAuthCallbackPage) string {
			if page.OK {
				return ai.OAuthSuccessHTML("Signed in to the MCP server. You may now close this page.")
			}
			return ai.OAuthErrorHTML(page.Message, page.Details)
		},
	}
	if port != nil {
		options.Port = *port
	}
	server, err := oauth.ListenOAuthCallbackServer(options)
	if err == nil {
		return server, nil
	}
	if required || port == nil {
		return nil, err
	}
	options.Port = 0
	return oauth.ListenOAuthCallbackServer(options)
}

// SignInOptions configure [SignInMcpServer].
type SignInOptions struct {
	ServerURL string
	Store     oauth.McpOAuthStateStore
	Settings  McpOAuthSettings
	Challenge *oauth.OAuthChallenge
	Prompt    McpSignInPrompt
	// AppName is the OAuth client name. Default: "pig".
	AppName string
}

// SignInMcpServer signs in to an MCP server. It uses the stored refresh token
// when possible; otherwise it runs the browser authorization code flow.
// Tokens are saved to the store.
func SignInMcpServer(ctx context.Context, options SignInOptions) error {
	if options.AppName == "" {
		options.AppName = "pig"
	}
	stored, err := options.Store.Load(ctx)
	if err != nil {
		return err
	}
	callbackOptions, err := newCallbackSettings(options.Settings)
	if err != nil {
		return err
	}
	// Reuse the port of the registered redirect URI so the registered client stays valid.
	preferredPort := callbackOptions.port
	if preferredPort == nil && stored != nil {
		if registered := registeredRedirectURLs(stored.ClientInformation); len(registered) > 0 {
			if u, err := url.Parse(registered[0]); err == nil {
				if p, err := strconv.Atoi(u.Port()); err == nil && p != 0 {
					preferredPort = &p
				}
			}
		}
	}
	callback, err := listenForCallback(callbackOptions, preferredPort, callbackOptions.port != nil)
	if err != nil {
		return err
	}
	defer func() { _ = callback.Close() }()
	redirectURL := callbackOptions.fixedRedirectURL
	if redirectURL == "" {
		redirectURL = callback.RedirectURL
	}
	if stored != nil {
		next := stored.Clone()
		// Every sign-in gets a fresh `state` parameter.
		next.OAuthState = ""
		// A registered client cannot use another redirect URI, and its tokens belong to it.
		if options.Settings.ClientID == "" && !slices.Contains(registeredRedirectURLs(stored.ClientInformation), redirectURL) {
			next.ClientInformation, next.Tokens, next.TokensExpireAt = nil, nil, nil
		}
		if err := options.Store.Save(ctx, next); err != nil {
			return err
		}
	}

	var authorizationURL *url.URL
	provider, err := createProvider(options.ServerURL, options.Store, options.Settings, redirectURL, options.AppName, func(u *url.URL) { authorizationURL = u })
	if err != nil {
		return err
	}
	flow := oauth.OAuthFlowOptions{ServerURL: options.ServerURL}
	var challengeScope string
	if options.Challenge != nil {
		flow.ResourceMetadataURL = options.Challenge.ResourceMetadataURL
		challengeScope = options.Challenge.Scope
		// A refresh keeps the granted scope; a server asking for more needs the browser flow.
		flow.SkipRefresh = options.Challenge.Error == "insufficient_scope"
	}
	// A server asking for more scope gets it on top of the configured scope.
	flow.Scope = mergeScopes(options.Settings.Scope, challengeScope)
	result, err := oauth.AuthorizeMcp(ctx, provider, flow)
	if err != nil {
		return err
	}
	if result == oauth.OAuthAuthorized {
		return nil
	}
	if authorizationURL == nil {
		return errors.New("OAuth flow did not produce an authorization URL")
	}
	state, err := provider.State(ctx)
	if err != nil {
		return err
	}
	// The wait is registered before the URL is shown: a browser that follows the
	// redirect at once reaches the callback before this goroutine goes on.
	wait, err := callback.WaitForCallback(state)
	if err != nil {
		return err
	}
	options.Prompt.ShowAuthorizationURL(authorizationURL)
	code, err := waitForAuthorizationCode(ctx, wait, state, options.Prompt)
	if err != nil {
		return err
	}
	flow.AuthorizationCode = code
	_, err = oauth.AuthorizeMcp(ctx, provider, flow)
	return err
}
