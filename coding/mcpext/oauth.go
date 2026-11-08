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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
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
// Credentials live in `<agent-dir>/mcp-auth.json`, keyed by server name and URL.

const (
	// clientMetadataBaseURL is where PiG's site serves its Client ID Metadata
	// Documents, in Pi's shape: `client.json` and `<callback ID>/client.json`.
	// Pi serves its own on https://pi.dev/oauth; PiG's hosted origin
	// (internal/coding/pigidentity) replaces it, as for every hosted endpoint (D64).
	clientMetadataBaseURL = "https://pi-in-go.dev/oauth"
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

// McpOAuthCredentials is what the extension asks of a credential store, and what a host that keeps the credentials elsewhere implements (see [NewMcpOAuthCredentialStoreOver]): Pi's `credentials` option takes any object with these members (index.ts:508, 622, cli.ts:241).
type McpOAuthCredentials interface {
	// ForServer returns the store of the server with the given name and URL.
	ForServer(name, serverURL string) (oauth.McpOAuthServerStore, error)
	// Tokens returns the stored tokens of the server, nil when there are none.
	Tokens(name, serverURL string) *oauth.OAuthTokens
	// Remove deletes the stored state of the server and returns whether credentials were stored for it.
	Remove(name, serverURL string) (bool, error)
}

var _ McpOAuthCredentials = (*McpOAuthCredentialStore)(nil)

// McpOAuthCredentialStore keeps per-server OAuth state (client registration,
// tokens, pending PKCE verifier) in `mcp-auth.json`.
type McpOAuthCredentialStore struct {
	// custom, when set, answers every operation: the store is a view of the host's own credentials.
	custom  McpOAuthCredentials
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

// NewMcpOAuthCredentialStoreOver returns a store whose operations are those of custom.
func NewMcpOAuthCredentialStoreOver(custom McpOAuthCredentials) *McpOAuthCredentialStore {
	return &McpOAuthCredentialStore{custom: custom}
}

// NewMcpOAuthCredentialStoreWithBackend returns a store over backend. Refresh
// locks live in lockDir; an empty lockDir serializes refreshes in this process
// only.
func NewMcpOAuthCredentialStoreWithBackend(backend AuthStorageBackend, lockDir string) *McpOAuthCredentialStore {
	return &McpOAuthCredentialStore{backend: backend, lockDir: lockDir}
}

// storedStates is the parsed `mcp-auth.json`, keyed by server name and URL in
// the file's order, as a JavaScript object keeps it.
type storedStates = *orderedjson.Object

func parseStates(content string) (storedStates, error) {
	if strings.TrimFunc(content, isJSWhitespace) == "" {
		return orderedjson.New(), nil
	}
	// JSON.parse reads a number beyond float64 as Infinity, which the rewrite stores as null; decoding into a Go value would reject the whole file.
	var syntax json.RawMessage
	if err := json.Unmarshal([]byte(content), &syntax); err != nil {
		return nil, err
	}
	if len(syntax) == 0 || syntax[0] != '{' {
		return orderedjson.New(), nil
	}
	return orderedjson.Parse(syntax)
}

func serverKey(serverURL string) (string, error) {
	u, err := nodeurl.ParseURL(serverURL)
	if err != nil {
		return "", fmt.Errorf("Invalid URL: %s", serverURL)
	}
	return u.Href(), nil
}

// storeKeys returns the keys of a server's state: by name and URL, so servers
// sharing a URL keep separate accounts, and the legacy key by URL alone,
// written by older versions.
func storeKeys(name, serverURL string) (key, legacyKey string, err error) {
	legacyKey, err = serverKey(serverURL)
	if err != nil {
		return "", "", err
	}
	return mcp.McpNamespace(name) + "|" + legacyKey, legacyKey, nil
}

// truthy is JavaScript truthiness of a stored JSON value.
func truthy(raw json.RawMessage, present bool) bool {
	if !present {
		return false
	}
	switch string(bytes.TrimSpace(raw)) {
	case "null", "false", `""`, "0", "-0":
		return false
	}
	var n float64
	return json.Unmarshal(raw, &n) != nil || n != 0
}

type serverStore struct {
	store     *McpOAuthCredentialStore
	key       string
	legacyKey string
}

// ForServer returns the store of the server with the given name and URL.
func (c *McpOAuthCredentialStore) ForServer(name, serverURL string) (oauth.McpOAuthServerStore, error) {
	if c.custom != nil {
		return c.custom.ForServer(name, serverURL)
	}
	key, legacyKey, err := storeKeys(name, serverURL)
	if err != nil {
		return nil, err
	}
	return &serverStore{store: c, key: key, legacyKey: legacyKey}, nil
}

// Load returns the server's state. The first server to load legacy state
// takes it over; others with the same URL sign in again.
func (s *serverStore) Load(context.Context) (*oauth.McpOAuthState, error) {
	var raw json.RawMessage
	var ok bool
	err := s.store.backend.WithLock(func(current string, _ bool) (*string, error) {
		states, err := parseStates(current)
		if err != nil {
			return nil, err
		}
		raw, ok = states.Get(s.key)
		legacy, hasLegacy := states.Get(s.legacyKey)
		if truthy(raw, ok) || !truthy(legacy, hasLegacy) {
			return nil, nil
		}
		states.Set(s.key, legacy)
		states.Delete(s.legacyKey)
		raw, ok = legacy, true
		data, err := marshalStates(states)
		if err != nil {
			return nil, err
		}
		next := data + "\n"
		return &next, nil
	})
	if err != nil {
		return nil, err
	}
	if !ok || string(raw) == "null" {
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
		// proper-lockfile reports a lock held by another process as `ELOCKED`.
		if errors.Is(err, pilock.ErrLocked) {
			return oauth.NewMcpRefreshLockBusyError()
		}
		return err
	}
	defer func() { _ = lock.Release() }()
	return fn(ctx)
}

// Tokens returns the stored tokens of the server with the given name and URL,
// for noticing sign-ins done by another process. It does not take over legacy
// state.
func (c *McpOAuthCredentialStore) Tokens(name, serverURL string) *oauth.OAuthTokens {
	if c.custom != nil {
		return c.custom.Tokens(name, serverURL)
	}
	key, legacyKey, err := storeKeys(name, serverURL)
	if err != nil {
		return nil
	}
	states, err := c.read()
	if err != nil {
		return nil
	}
	raw, ok := states.Get(key)
	if !ok || string(raw) == "null" {
		raw, ok = states.Get(legacyKey)
	}
	var state oauth.McpOAuthState
	if !ok || json.Unmarshal(raw, &state) != nil {
		return nil
	}
	return state.Tokens
}

// Remove deletes the stored state of the server with the given name and URL,
// or the legacy state it would take over. It returns whether credentials were
// stored for it.
func (c *McpOAuthCredentialStore) Remove(name, serverURL string) (bool, error) {
	if c.custom != nil {
		return c.custom.Remove(name, serverURL)
	}
	key, legacyKey, err := storeKeys(name, serverURL)
	if err != nil {
		return false, err
	}
	removed := false
	err = c.backend.WithLock(func(current string, _ bool) (*string, error) {
		states, err := parseStates(current)
		if err != nil {
			return nil, err
		}
		stored := ""
		if states.Has(key) {
			stored = key
		} else if states.Has(legacyKey) {
			stored = legacyKey
		}
		if stored == "" {
			return nil, nil
		}
		states.Delete(stored)
		data, err := marshalStates(states)
		if err != nil {
			return nil, err
		}
		removed = true
		next := data + "\n"
		return &next, nil
	})
	return removed && err == nil, err
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

// piClientMetadataDocuments are pi's Client ID Metadata Documents on PiG's origin: `client.json` and `<callback ID>/client.json`.
func piClientMetadataDocuments() *oauth.McpClientMetadataDocuments {
	return &oauth.McpClientMetadataDocuments{BaseURL: clientMetadataBaseURL, ListsRedirectURL: extension.IsClientMetadataDocumentCallback}
}

// piSettings are settings for pi-mcp: a rebranded build would show up as pi with pi's documents, so `auto` registers.
// pig divergence (D2): PiG's application name is not pi, so `auto` registers a client dynamically, as Pi does for any rebranded build; only
// `cimd` identifies as pi's document.
// upstream: packages/coding-agent/src/extensions/mcp/oauth.ts:piSettings
func piSettings(settings oauth.McpOAuthSettings, appName string) oauth.McpOAuthSettings {
	if cmp.Or(settings.ClientRegistration, "auto") == "auto" && appName != "pi" {
		settings.ClientRegistration = "dcr"
	}
	return settings
}

// McpAuthProviderOptions configure [NewMcpAuthProvider].
type McpAuthProviderOptions struct {
	ServerURL string
	Store     oauth.McpOAuthServerStore
	// Settings is only called when a refresh is needed, so a secret that fails to resolve fails the refresh instead of the whole connection setup.
	Settings func() (oauth.McpOAuthSettings, error)
	// OnChallenge receives the server's WWW-Authenticate challenge so sign-in can use its resource metadata URL and scope.
	OnChallenge func(oauth.OAuthChallenge)
	// AppName is the OAuth client name. Default: "pig".
	AppName string
}

// NewMcpAuthProvider is pi-mcp's [oauth.CreateMcpAuthProvider] with pi's client identity.
func NewMcpAuthProvider(options McpAuthProviderOptions) oauth.McpAuthProvider {
	appName := cmp.Or(options.AppName, "pig")
	return oauth.CreateMcpAuthProvider(oauth.McpAuthProviderOptions{
		ServerURL: options.ServerURL,
		Store:     options.Store,
		Settings: func() (oauth.McpOAuthSettings, error) {
			settings, err := options.Settings()
			return piSettings(settings, appName), err
		},
		ClientName:              appName,
		ClientMetadataDocuments: piClientMetadataDocuments(),
		Fetch:                   nodeFetch(),
		OnChallenge:             options.OnChallenge,
	})
}

// nodeFetch is the one client of the MCP transports and OAuth requests that Pi sends through the global fetch.
var nodeFetch func() mcp.McpFetch = sync.OnceValue(func() mcp.McpFetch { return ai.NewNodeFetch() })

// McpSignInPrompt is how a sign-in reaches the user.
type McpSignInPrompt interface {
	// ShowAuthorizationURL shows the authorization URL to the user and opens it
	// in a browser.
	ShowAuthorizationURL(u oauth.URL)
	// PromptForRedirectURL asks for the redirect URL from the browser address
	// bar, for when the browser cannot reach the loopback callback (for example
	// over SSH). The context ends once the callback arrives or the sign-in is
	// cancelled. It returns an empty string when the user cancels.
	PromptForRedirectURL(ctx context.Context) (string, error)
}

// renderCallbackPage is extensions/mcp/oauth.ts renderCallbackPage: the success page with the sign-in message for an ok callback, the error page with the
// page's message and details for any other.
func renderCallbackPage(page oauth.OAuthCallbackPage) string {
	if page.OK {
		return ai.OAuthSuccessHTML("Signed in to the MCP server. You may now close this page.")
	}
	return ai.OAuthErrorHTML(page.Message, page.Details)
}

// SignInOptions configure [SignInMcpServer].
type SignInOptions struct {
	ServerURL string
	Store     oauth.McpOAuthServerStore
	Settings  oauth.McpOAuthSettings
	Challenge *oauth.OAuthChallenge
	Prompt    McpSignInPrompt
	// AppName is the OAuth client name. Default: "pig".
	AppName string
}

// SignInMcpServer signs in to an MCP server. It uses the stored refresh token when possible; otherwise it runs the browser authorization code
// flow against a loopback callback or a pasted redirect URL. Tokens are saved to the store. Ending ctx stops the sign-in at any step with
// [oauth.McpSignInCancelledError]; requests to the authorization server are also time-limited.
func SignInMcpServer(ctx context.Context, options SignInOptions) error {
	appName := cmp.Or(options.AppName, "pig")
	settings := piSettings(options.Settings, appName)
	documents := piClientMetadataDocuments()
	loopback, err := oauth.ListenForLoopbackSignIn(ctx, oauth.ListenForLoopbackSignInOptions{
		ServerURL: options.ServerURL, Store: options.Store, Settings: settings, ClientMetadataDocuments: documents, RenderPage: renderCallbackPage,
	})
	if err != nil {
		return err
	}
	defer func() { _ = loopback.Callback.Close() }()
	signIn := oauth.McpSignInOptions{
		McpOAuthClientOptions: oauth.McpOAuthClientOptions{
			ServerURL: options.ServerURL, Store: options.Store, Settings: settings, ClientName: appName, ClientMetadataDocuments: documents, Fetch: nodeFetch(),
		},
		RedirectURL: loopback.RedirectURL,
		Challenge:   options.Challenge,
	}
	start, err := oauth.StartMcpSignIn(ctx, signIn)
	if err != nil || start.Kind == oauth.McpSignInKindAuthorized {
		return err
	}
	wait := oauth.WaitForLoopbackSignInOptions{Callback: loopback.Callback, Authorization: start.Redirect()}
	if options.Prompt != nil {
		wait.OnRegistered = func() {
			u, err := oauth.ParseURL(start.AuthorizationURL)
			if err == nil {
				options.Prompt.ShowAuthorizationURL(u)
			}
		}
		wait.PromptForRedirectURL = options.Prompt.PromptForRedirectURL
	}
	response, err := oauth.WaitForLoopbackSignIn(ctx, wait)
	if err != nil {
		return err
	}
	return oauth.CompleteMcpSignIn(ctx, oauth.McpCompleteSignInOptions{McpSignInOptions: signIn, Authorization: start.Redirect(), Response: response})
}
