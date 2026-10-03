package mcpext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/configvalue"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/coding-agent/src/extensions/mcp/runtime.ts.
//
// The part of the MCP integration that talks to servers: connections,
// transports, and OAuth sign-in.

const (
	// upstream: packages/coding-agent/src/extensions/mcp/runtime.ts:DEFAULT_TIMEOUT_SECONDS
	defaultTimeoutSeconds = 60
	stderrTailChars       = 2_000
)

// connectRetryDelays are the delays between attempts to connect to an HTTP
// server that failed with a transient error.
// upstream: packages/coding-agent/src/extensions/mcp/runtime.ts:CONNECT_RETRY_DELAYS_MS
var connectRetryDelays = []time.Duration{250 * time.Millisecond, 1_000 * time.Millisecond}

// ServerState is the state of one [Connection]. `disconnected` means the
// connection dropped (for example the stdio server exited); the next call
// reconnects.
type ServerState string

// Connection states.
const (
	StateConnecting   ServerState = "connecting"
	StateConnected    ServerState = "connected"
	StateDisconnected ServerState = "disconnected"
	StateNeedsAuth    ServerState = "needs-auth"
	StateFailed       ServerState = "failed"
	StateClosed       ServerState = "closed"
)

// TransportFactory creates the transport of a server.
type TransportFactory func(entry McpServerEntry, cwd string, authProvider mcp.AuthProvider) (mcp.Transport, error)

// signInRequiredMessage names the command that signs in: `/login <provider>` for a server with `auth.provider`, else `/mcp`.
func signInRequiredMessage(entry McpServerEntry) string {
	command := "/mcp"
	if entry.Config.IsHTTP() && entry.Config.Auth != nil && entry.Config.Auth.Provider != "" {
		command = "/login " + entry.Config.Auth.Provider
	}
	return fmt.Sprintf(`MCP server "%s" requires sign-in. Run %s to sign in.`, entry.Name, command)
}

// isTransientError: network failures and overloaded or restarting servers,
// which are worth another attempt.
func isTransientError(err error) bool {
	if httpErr, ok := errors.AsType[*mcp.McpHttpError](err); ok {
		return httpErr.Status == 408 || httpErr.Status == 429 || (httpErr.Status >= 500 && httpErr.Status != 501)
	}
	var urlErr *url.Error
	var netErr net.Error
	return errors.As(err, &urlErr) || errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF)
}

// usesOAuth: HTTP servers authenticate with OAuth unless the config supplies
// an `Authorization` header or `auth`.
func usesOAuth(entry McpServerEntry) bool {
	if !entry.Config.IsHTTP() || entry.Config.Auth != nil {
		return false
	}
	for _, name := range entry.Config.Headers.Keys() {
		if strings.EqualFold(name, "authorization") {
			return false
		}
	}
	return true
}

// expandHome: `~` and `~/…` (also `~\…` on Windows) name the home directory,
// like in a shell.
func expandHome(value string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(value, `~\`)) {
		return filepath.Join(home, value[2:])
	}
	return value
}

// resolveHeadersOrThrow resolves every header value like an API key, failing
// with the header's name in the message.
func resolveHeaders(headers *extension.OrderedStrings, description string) (map[string]string, error) {
	if headers == nil || len(headers.Keys()) == 0 {
		return nil, nil
	}
	resolved := map[string]string{}
	for _, key := range headers.Keys() {
		value, _ := headers.Get(key)
		out, err := configvalue.ResolveOrError(value, fmt.Sprintf(`%s header "%s"`, description, key), nil)
		if err != nil {
			return nil, err
		}
		resolved[key] = out
	}
	return resolved, nil
}

// CreateDefaultTransport builds the stdio or streamable HTTP transport a
// server's config asks for.
func CreateDefaultTransport(entry McpServerEntry, cwd string, authProvider mcp.AuthProvider) (mcp.Transport, error) {
	config, name := entry.Config, entry.Name
	if config.IsHTTP() {
		headers, err := resolveHeaders(config.Headers, fmt.Sprintf(`MCP server "%s"`, name))
		if err != nil {
			return nil, err
		}
		return mcp.NewStreamableHTTPTransport(mcp.StreamableHTTPTransportOptions{URL: config.URL, Headers: headers, AuthProvider: authProvider})
	}
	env := map[string]string{}
	for _, key := range config.Env.Keys() {
		value, _ := config.Env.Get(key)
		resolved, err := configvalue.ResolveOrError(value, fmt.Sprintf(`MCP server "%s" env "%s"`, name, key), nil)
		if err != nil {
			return nil, err
		}
		env[key] = resolved
	}
	args := make([]string, len(config.Args))
	for i, arg := range config.Args {
		args[i] = expandHome(arg)
	}
	dir := expandHome(config.Cwd)
	if config.Cwd == "" {
		dir = "."
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return mcp.NewStdioTransport(mcp.StdioTransportOptions{
		Command: expandHome(config.Command), Args: args, Cwd: filepath.Clean(dir), Env: env, Stderr: "pipe",
	}), nil
}

// withoutTemplates: servers that do not implement `resources/templates/list`
// have no templates.
func withoutTemplates[T any](list func() (T, error), empty T) (T, error) {
	value, err := list()
	if err != nil {
		var mcpErr *mcp.McpError
		if errors.As(err, &mcpErr) && mcpErr.Code == mcp.JSONRPCMethodNotFound {
			return empty, nil
		}
	}
	return value, err
}

func listTemplates(ctx context.Context, client *mcp.Client, options mcp.RequestOptions) ([]mcp.ResourceTemplate, error) {
	return withoutTemplates(func() ([]mcp.ResourceTemplate, error) { return client.ListResourceTemplates(ctx, options) }, []mcp.ResourceTemplate{})
}

// fetchResources lists resources and templates at connect time, for the counts
// in `/mcp` and `pi mcp list`. A server whose lists fail still connects: the
// resource tools list and read its resources on demand.
func fetchResources(ctx context.Context, client *mcp.Client) ([]mcp.Resource, []mcp.ResourceTemplate) {
	var resources []mcp.Resource
	var templates []mcp.ResourceTemplate
	var wg sync.WaitGroup
	wg.Go(func() {
		if list, err := client.ListResources(ctx, mcp.RequestOptions{}); err == nil {
			resources = list
		}
	})
	wg.Go(func() {
		if list, err := listTemplates(ctx, client, mcp.RequestOptions{}); err == nil {
			templates = list
		}
	})
	wg.Wait()
	visibleResources := make([]mcp.Resource, 0, len(resources))
	for _, resource := range resources {
		if !IsMcpAppResource(resource.URI, resource.MimeType) {
			visibleResources = append(visibleResources, resource)
		}
	}
	visibleTemplates := make([]mcp.ResourceTemplate, 0, len(templates))
	for _, template := range templates {
		if !IsMcpAppResource(template.URITemplate, template.MimeType) {
			visibleTemplates = append(visibleTemplates, template)
		}
	}
	return visibleResources, visibleTemplates
}

// ConnectionOptions configure a [Connection].
type ConnectionOptions struct {
	Entry           McpServerEntry
	Cwd             string
	CreateTransport TransportFactory
	Credentials     *McpOAuthCredentialStore
	// ProviderToken is the current token of a pi provider, for servers with `auth.provider`. It returns "" when the
	// provider has none.
	ProviderToken func(ctx context.Context, provider string) string
	// OnTools is called when the server's tools or resources change.
	OnTools func(*Connection)
	// OnChange is called when the state, error, or tools change.
	OnChange func(*Connection)
	// Log receives the server's log messages (`notifications/message`).
	Log *McpServerLog
	// ClientName and ClientVersion identify this client to servers. Defaults:
	// "pig" and the PiG version (upstream sends its VERSION).
	ClientName    string
	ClientVersion string
}

type openFlight struct {
	done   chan struct{}
	client *mcp.Client
	err    error
}

// Connection is one configured server. It reconnects lazily when a call finds
// the connection gone. It is safe for concurrent use.
type Connection struct {
	// Entry is the server's configuration.
	Entry McpServerEntry

	cwd             string
	createTransport TransportFactory
	authProvider    mcp.AuthProvider
	// settled returns when no token refresh is running; nil for a provider token.
	settled       func(context.Context)
	onTools       func(*Connection)
	onChange      func(*Connection)
	log           *McpServerLog
	clientName    string
	clientVersion string

	mu           sync.Mutex
	state        ServerState
	errText      string
	tools        []mcp.Tool
	hasResources bool
	resources    []mcp.Resource
	templates    []mcp.ResourceTemplate
	instructions string
	challenge    *oauth.OAuthChallenge
	client       *mcp.Client
	opening      *openFlight
	closed       bool
	closedCh     chan struct{}
	// lifetime ends when the connection closes; it aborts a connect in flight.
	lifetime    context.Context
	endLifetime context.CancelFunc
	stderrTail  string
	background  sync.WaitGroup
}

// NewConnection returns a connection in the connecting state. It does not
// connect until the first call to [Connection.GetClient].
func NewConnection(options ConnectionOptions) (*Connection, error) {
	c := &Connection{
		Entry:           options.Entry,
		cwd:             options.Cwd,
		createTransport: options.CreateTransport,
		onTools:         options.OnTools,
		onChange:        options.OnChange,
		log:             options.Log,
		clientName:      options.ClientName,
		clientVersion:   options.ClientVersion,
		state:           StateConnecting,
		closedCh:        make(chan struct{}),
	}
	c.lifetime, c.endLifetime = context.WithCancel(context.Background())
	if c.clientName == "" {
		c.clientName = "pig"
	}
	if c.clientVersion == "" {
		c.clientVersion = pigversion.Version
	}
	if url := c.OAuthURL(); url != "" {
		store, err := options.Credentials.ForServer(c.Name(), url)
		if err != nil {
			return nil, err
		}
		provider := NewMcpAuthProvider(McpAuthProviderOptions{
			ServerURL: url, Store: store, AppName: c.clientName,
			Settings:    c.OAuthSettings,
			OnChallenge: func(challenge oauth.OAuthChallenge) { c.setChallenge(&challenge) },
		})
		c.authProvider, c.settled = provider, provider.Settled
	} else if entry := c.Entry.Config; entry.IsHTTP() && entry.Auth != nil {
		// Read on every request, so the provider's refreshes apply; MCP stores no copy.
		c.authProvider = providerTokenAuth{provider: entry.Auth.Provider, token: options.ProviderToken}
	}
	return c, nil
}

// Name is the server's name.
func (c *Connection) Name() string { return c.Entry.Name }

// State is the connection state.
func (c *Connection) State() ServerState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Error is the last error, or "".
func (c *Connection) Error() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.errText
}

// Tools returns the tools the server offered at the last connect or change.
func (c *Connection) Tools() []mcp.Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]mcp.Tool(nil), c.tools...)
}

// HasResources reports whether the server offers resources.
func (c *Connection) HasResources() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasResources
}

// Resources returns the resources listed at the last connect or change,
// without MCP App resources.
func (c *Connection) Resources() []mcp.Resource {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]mcp.Resource(nil), c.resources...)
}

// ResourceTemplates returns the templates listed at the last connect or change.
func (c *Connection) ResourceTemplates() []mcp.ResourceTemplate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]mcp.ResourceTemplate(nil), c.templates...)
}

// Instructions are the server instructions from `initialize`, describing its
// tools as a group.
func (c *Connection) Instructions() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.instructions
}

// Challenge is the last OAuth challenge from the server; sign-in uses its
// resource metadata URL and scope.
func (c *Connection) Challenge() *oauth.OAuthChallenge {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.challenge
}

func (c *Connection) setChallenge(challenge *oauth.OAuthChallenge) {
	c.mu.Lock()
	c.challenge = challenge
	c.mu.Unlock()
}

// ClearChallenge drops the challenge that asked for a sign-in, once it is answered.
func (c *Connection) ClearChallenge() { c.setChallenge(nil) }

// Timeout is the per-request timeout.
func (c *Connection) Timeout() time.Duration {
	seconds := float64(defaultTimeoutSeconds)
	if c.Entry.Config.Timeout != nil {
		seconds = *c.Entry.Config.Timeout
	}
	return time.Duration(seconds * float64(time.Second))
}

// providerTokenAuth sends the current token of a pi provider. The provider's own refresh keeps it current; a 401 means the user has to sign in to the provider again.
type providerTokenAuth struct {
	provider string
	token    func(ctx context.Context, provider string) string
}

func (a providerTokenAuth) Token(ctx context.Context) (string, error) {
	if a.token == nil {
		return "", nil
	}
	return a.token(ctx, a.provider), nil
}

// OAuthURL is the server URL when the server authenticates with OAuth, or "".
func (c *Connection) OAuthURL() string {
	if usesOAuth(c.Entry) {
		return c.Entry.Config.URL
	}
	return ""
}

// OAuthSettings resolves the server's OAuth settings. A client secret that
// fails to resolve is an error.
func (c *Connection) OAuthSettings() (McpOAuthSettings, error) {
	oauthConfig := c.Entry.Config.OAuth
	if !c.Entry.Config.IsHTTP() || oauthConfig == nil {
		return McpOAuthSettings{}, nil
	}
	settings := McpOAuthSettings{
		ClientID: oauthConfig.ClientID, CallbackPort: oauthConfig.CallbackPort, CallbackURL: oauthConfig.CallbackURL, Scope: oauthConfig.Scope, ClientName: oauthConfig.ClientName,
	}
	if oauthConfig.AuthServerMetadataURL != "" {
		// Config validation accepted the URL. `new URL()` strips leading and trailing C0 controls and spaces and
		// drops tabs and newlines before it parses, which url.Parse rejects.
		metadataURL, err := url.Parse(nodeurl.PrepareInput(oauthConfig.AuthServerMetadataURL))
		if err != nil {
			return McpOAuthSettings{}, err
		}
		settings.AuthServerMetadataURL = metadataURL
	}
	if oauthConfig.ClientSecret != "" {
		secret, err := configvalue.ResolveOrError(oauthConfig.ClientSecret, fmt.Sprintf(`MCP server "%s" oauth.clientSecret`, c.Entry.Name), nil)
		if err != nil {
			return McpOAuthSettings{}, err
		}
		settings.ClientSecret = secret
	}
	return settings, nil
}

// GetClient returns the connected client, connecting first when needed.
// Concurrent callers share one connection attempt.
func (c *Connection) GetClient(ctx context.Context) (*mcp.Client, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf(`MCP server "%s" is shut down`, c.Entry.Name)
	}
	if c.client != nil && c.client.ConnectionState() == mcp.ClientStateConnected {
		client := c.client
		c.mu.Unlock()
		return client, nil
	}
	flight := c.opening
	if flight == nil {
		flight = &openFlight{done: make(chan struct{})}
		c.opening = flight
		c.background.Go(func() {
			flight.client, flight.err = c.open(c.lifetime)
			c.mu.Lock()
			c.opening = nil
			c.mu.Unlock()
			close(flight.done)
		})
	}
	c.mu.Unlock()
	select {
	case <-flight.done:
		return flight.client, flight.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// CallTool calls a tool, reconnecting when needed. Tool calls are not retried
// after a transient HTTP error, since they may have run.
func (c *Connection) CallTool(ctx context.Context, name string, args any, options mcp.RequestOptions) (*mcp.CallToolResult, error) {
	return withClient(ctx, c, false, func(client *mcp.Client) (*mcp.CallToolResult, error) {
		return client.CallTool(ctx, name, args, options)
	})
}

// ReadResource reads a resource.
func (c *Connection) ReadResource(ctx context.Context, uri string, options mcp.RequestOptions) (*mcp.ReadResourceResult, error) {
	return withClient(ctx, c, true, func(client *mcp.Client) (*mcp.ReadResourceResult, error) {
		return client.ReadResource(ctx, uri, options)
	})
}

// ResourcesPage lists one page of resources.
func (c *Connection) ResourcesPage(ctx context.Context, cursor *string, options mcp.RequestOptions) (*mcp.ListResourcesResult, error) {
	return withClient(ctx, c, true, func(client *mcp.Client) (*mcp.ListResourcesResult, error) {
		return client.ListResourcesPage(ctx, cursor, options)
	})
}

// ResourceTemplatesPage lists one page of resource templates.
func (c *Connection) ResourceTemplatesPage(ctx context.Context, cursor *string, options mcp.RequestOptions) (*mcp.ListResourceTemplatesResult, error) {
	return withClient(ctx, c, true, func(client *mcp.Client) (*mcp.ListResourceTemplatesResult, error) {
		return withoutTemplates(func() (*mcp.ListResourceTemplatesResult, error) {
			return client.ListResourceTemplatesPage(ctx, cursor, options)
		}, &mcp.ListResourceTemplatesResult{ResourceTemplates: []mcp.ResourceTemplate{}})
	})
}

// AllResources lists every resource.
func (c *Connection) AllResources(ctx context.Context, options mcp.RequestOptions) ([]mcp.Resource, error) {
	return withClient(ctx, c, true, func(client *mcp.Client) ([]mcp.Resource, error) {
		return client.ListResources(ctx, options)
	})
}

// AllResourceTemplates lists every resource template.
func (c *Connection) AllResourceTemplates(ctx context.Context, options mcp.RequestOptions) ([]mcp.ResourceTemplate, error) {
	return withClient(ctx, c, true, func(client *mcp.Client) ([]mcp.ResourceTemplate, error) {
		return listTemplates(ctx, client, options)
	})
}

// withClient runs a request, reconnecting when needed. `readOnly` requests are
// retried once after a transient HTTP error; tool calls are not, since they may
// have run.
func withClient[T any](ctx context.Context, c *Connection, readOnly bool, run func(*mcp.Client) (T, error)) (T, error) {
	var zero T
	for attempt := 1; ; attempt++ {
		client, err := c.GetClient(ctx)
		if err != nil {
			return zero, err
		}
		value, err := run(client)
		if err == nil {
			return value, nil
		}
		var httpErr *mcp.McpHttpError
		if readOnly && attempt == 1 && errors.As(err, &httpErr) && isTransientError(err) {
			if !c.sleep(ctx, connectRetryDelays[0]) {
				return zero, context.Cause(ctx)
			}
			continue
		}
		var expired *mcp.McpSessionExpiredError
		if errors.As(err, &expired) && attempt == 1 {
			// The server no longer knows the session (restart, deploy), so it did not run the request.
			// Retry once on a new session. The old client is detached but not closed: closing would
			// fail its other in-flight calls, which instead get the same 404 and retry the same way.
			c.mu.Lock()
			if c.client == client {
				c.client = nil
			}
			c.mu.Unlock()
			continue
		}
		if !c.needsSignIn(err) {
			return zero, err
		}
		c.dropClient(client)
		c.markNeedsAuth()
		return zero, errors.New(signInRequiredMessage(c.Entry))
	}
}

// sleep waits d, or until ctx or the connection ends. It reports whether the
// full time passed.
func (c *Connection) sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// Reconnect connects again with fresh credentials, for example after signing in.
func (c *Connection) Reconnect(ctx context.Context) error {
	c.waitOpening()
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client != nil {
		c.dropClient(client)
	}
	_, err := c.GetClient(ctx)
	return err
}

// SignOut disconnects after the stored credentials were removed.
func (c *Connection) SignOut() {
	c.waitOpening()
	c.mu.Lock()
	client, closed := c.client, c.closed
	c.mu.Unlock()
	if client != nil {
		c.dropClient(client)
	}
	if !closed {
		c.markNeedsAuth()
	}
}

func (c *Connection) waitOpening() {
	c.mu.Lock()
	flight := c.opening
	c.mu.Unlock()
	if flight != nil {
		<-flight.done
	}
}

// needsSignIn: OAuth servers that still reject the request after a refresh
// need the user to sign in again.
func (c *Connection) needsSignIn(err error) bool {
	if _, ok := errors.AsType[*oauth.McpOAuthAuthorizationRequiredError](err); ok {
		return true
	}
	var authErr *mcp.McpAuthRequiredError
	return c.authProvider != nil && errors.As(err, &authErr)
}

func (c *Connection) markNeedsAuth() {
	c.mu.Lock()
	c.state = StateNeedsAuth
	c.errText = ""
	c.mu.Unlock()
	c.changed()
}

func (c *Connection) changed() {
	if c.onChange != nil {
		c.onChange(c)
	}
}

func (c *Connection) dropClient(client *mcp.Client) {
	c.mu.Lock()
	if c.client == client {
		c.client = nil
	}
	c.mu.Unlock()
	_ = client.Close()
}

func (c *Connection) open(ctx context.Context) (*mcp.Client, error) {
	c.mu.Lock()
	c.state = StateConnecting
	c.mu.Unlock()
	c.changed()
	var retries []time.Duration
	if c.Entry.Config.IsHTTP() {
		retries = connectRetryDelays
	}
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		c.stderrTail = ""
		c.mu.Unlock()
		client, err := c.connectOnce(ctx)
		if err == nil {
			return client, nil
		}
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed || attempt >= len(retries) || !isTransientError(err) {
			return nil, c.connectFailed(err)
		}
		select {
		case <-time.After(retries[attempt]):
		case <-c.closedCh:
		}
		c.mu.Lock()
		closed = c.closed
		c.mu.Unlock()
		if closed {
			return nil, c.connectFailed(err)
		}
	}
}

// pathToFileURL is Node's url.pathToFileURL(path).href (Node 24.19.0).
func pathToFileURL(path string) string {
	return pathToFileURLFor(path, runtime.GOOS == "windows")
}

// pathToFileURLFor resolves path as path.posix or path.win32 does, keeps a
// trailing separator, and percent-encodes what Node's encodePathChars and
// the WHATWG path state encode: C0 controls, space, `"#%<>?[\]^` + "`{|}~",
// DEL and every non-ASCII byte. A Windows UNC path names the URL's host.
func pathToFileURLFor(path string, windows bool) string {
	resolved := path
	unc := windows && strings.HasPrefix(path, `\\`)
	if !unc {
		var err error
		if windows {
			resolved, err = nodepath.Win32Resolve(nodepath.Process(), path)
		} else {
			resolved, err = nodepath.PosixResolve(nodepath.Process(), path)
		}
		if err != nil {
			resolved = path
		}
	}
	host := ""
	if windows && strings.HasPrefix(resolved, `\\`) {
		rest := strings.TrimPrefix(resolved, `\\`)
		if after, ok := strings.CutPrefix(resolved, `\\?\UNC\`); ok {
			rest = after
		}
		if server, share, ok := strings.Cut(rest, `\`); ok && server != "" {
			host, resolved = server, `\`+share
			if ascii, err := nodeurl.FileHost(server); err == nil {
				host = ascii
			}
		}
	}
	// path.resolve strips a trailing separator, so it is added back.
	endsInSeparator := strings.HasSuffix(resolved, "/") || windows && strings.HasSuffix(resolved, `\`)
	if last := path[max(len(path)-1, 0):]; (last == "/" || windows && last == `\`) && !endsInSeparator {
		resolved += "/"
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.WriteString("file://" + host)
	if windows && host == "" {
		b.WriteByte('/')
	}
	for i := range len(resolved) {
		c := resolved[i]
		switch {
		case windows && c == '\\':
			b.WriteByte('/')
		case c <= 0x20 || c >= 0x7f || strings.IndexByte("\"#%<>?[\\]^`{|}~", c) >= 0:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func (c *Connection) connectOnce(ctx context.Context) (*mcp.Client, error) {
	client := mcp.NewClient(mcp.ClientOptions{
		Implementation:   mcp.Implementation{Name: c.clientName, Version: c.clientVersion},
		RequestTimeoutMs: int(c.Timeout().Milliseconds()),
		Roots:            []mcp.Root{{URI: pathToFileURL(c.cwd), Name: filepath.Base(c.cwd)}},
	})
	if c.log != nil {
		client.OnNotification("notifications/message", func(params json.RawMessage) { c.log.Write(c.Entry.Name, params) })
	}
	var stdio *mcp.StdioTransport
	fail := func(err error) (*mcp.Client, error) {
		_ = client.Close()
		if stdio != nil {
			tail := strings.TrimSpace(stdio.Stderr())
			if runes := []rune(tail); len(runes) > stderrTailChars {
				tail = string(runes[len(runes)-stderrTailChars:])
			}
			c.mu.Lock()
			c.stderrTail = tail
			c.mu.Unlock()
		}
		return nil, err
	}
	var authProvider mcp.AuthProvider
	if c.authProvider != nil {
		authProvider = c.authProvider
	}
	transport, err := c.createTransport(c.Entry, c.cwd, authProvider)
	if err != nil {
		return fail(err)
	}
	stdio, _ = transport.(*mcp.StdioTransport)
	if _, err := client.Connect(ctx, transport); err != nil {
		return fail(err)
	}
	client.OnNotification("notifications/tools/list_changed", func(json.RawMessage) { c.startBackground(func() { c.refreshTools(client) }) })
	client.OnNotification("notifications/resources/list_changed", func(json.RawMessage) { c.startBackground(func() { c.refreshResources(client) }) })
	client.OnClose(func() { c.handleClientClose(client, stdio) })
	// Servers without the tools capability (prompts or resources only) do not answer tools/list.
	caps := client.ServerCapabilities()
	hasResources := caps != nil && caps.Resources != nil
	var tools []mcp.Tool
	var resources []mcp.Resource
	var templates []mcp.ResourceTemplate
	var toolsErr error
	var wg sync.WaitGroup
	if caps != nil && caps.Tools != nil {
		wg.Go(func() { tools, toolsErr = client.ListTools(ctx, mcp.RequestOptions{}) })
	}
	if hasResources {
		wg.Go(func() { resources, templates = fetchResources(ctx, client) })
	}
	wg.Wait()
	if toolsErr != nil {
		return fail(toolsErr)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return fail(errors.New("shut down while connecting"))
	}
	if client.ConnectionState() != mcp.ClientStateConnected {
		return fail(errors.New("connection closed during setup"))
	}
	c.mu.Lock()
	c.client = client
	c.tools = tools
	c.hasResources = hasResources
	c.resources, c.templates = resources, templates
	c.instructions = ""
	if instructions := client.Instructions(); instructions != nil {
		c.instructions = strings.TrimFunc(*instructions, isJSWhitespace)
	}
	c.state = StateConnected
	c.errText = ""
	c.mu.Unlock()
	if c.onTools != nil {
		c.onTools(c)
	}
	c.changed()
	return client, nil
}

// startBackground runs f on a goroutine the connection owns and drains in Close.
func (c *Connection) startBackground(f func()) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.background.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.background.Done()
		f()
	}()
}

func (c *Connection) connectFailed(err error) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if c.needsSignIn(err) && !closed {
		c.markNeedsAuth()
		return errors.New(signInRequiredMessage(c.Entry))
	}
	c.mu.Lock()
	if c.closed {
		c.state = StateClosed
	} else {
		c.state = StateFailed
	}
	c.errText = err.Error()
	if c.stderrTail != "" {
		c.errText += "\n" + c.stderrTail
	}
	message := fmt.Sprintf(`MCP server "%s" failed to connect: %s`, c.Entry.Name, c.errText)
	c.mu.Unlock()
	c.changed()
	return errors.New(message)
}

// handleClientClose: the transport dropped. The next call reconnects; until
// then the status shows why. A closed client is never reused.
func (c *Connection) handleClientClose(client *mcp.Client, stdio *mcp.StdioTransport) {
	c.mu.Lock()
	if c.client != client || c.closed {
		c.mu.Unlock()
		return
	}
	c.client = nil
	c.state = StateDisconnected
	c.errText = "Connection closed"
	if stdio != nil {
		tail := strings.TrimSpace(stdio.Stderr())
		if runes := []rune(tail); len(runes) > stderrTailChars {
			tail = string(runes[len(runes)-stderrTailChars:])
		}
		if tail != "" {
			c.errText = "Connection closed\n" + tail
		}
	}
	c.mu.Unlock()
	c.changed()
}

func (c *Connection) refreshTools(client *mcp.Client) {
	tools, err := client.ListTools(context.Background(), mcp.RequestOptions{})
	c.mu.Lock()
	if err != nil {
		c.errText = "Failed to refresh tools: " + err.Error()
		c.mu.Unlock()
		c.changed()
		return
	}
	if c.client != client || c.closed {
		c.mu.Unlock()
		return
	}
	c.tools = tools
	c.mu.Unlock()
	if c.onTools != nil {
		c.onTools(c)
	}
	c.changed()
}

func (c *Connection) refreshResources(client *mcp.Client) {
	resources, templates := fetchResources(context.Background(), client)
	c.mu.Lock()
	if c.client != client || c.closed {
		c.mu.Unlock()
		return
	}
	c.resources, c.templates = resources, templates
	c.mu.Unlock()
	if c.onTools != nil {
		c.onTools(c)
	}
	c.changed()
}

// Close shuts the connection down and waits for its background work.
func (c *Connection) Close() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.closedCh)
	}
	c.state = StateClosed
	client := c.client
	c.client = nil
	c.mu.Unlock()
	c.endLifetime()
	c.changed()
	if client != nil {
		_ = client.Close()
	}
	// A refresh the server already answered may have rotated the refresh token; exiting before the
	// new tokens are saved would lose the grant.
	if c.settled != nil {
		c.settled(context.Background())
	}
	c.background.Wait()
}
