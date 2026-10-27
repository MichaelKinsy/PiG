// pig-specific: the function options of createMcpExtension (extensions/mcp/index.ts McpExtensionOptions: loadConfig, createTransport, credentials, openUrl, updateConfig) for an extension that passes the compiled-in MCP extension to a DefaultResourceLoader it constructs. Pi's options are functions of the caller; they stay in the caller's process and the host calls them over the wire that exists, the session graph callback (subprocess.RequestSessionGraphCallback), with JSON in and JSON out. No request kind is added.

//go:build !pig_strip_mcp

package coding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	_ "github.com/MichaelKinsy/PiG/internal/tlsdistrust" // hardens http.DefaultClient, which the token fetch of an MCP auth provider uses (pig additive (D191))
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// mcpFactory is createMcpExtension: the data options logPath and startupWaitMs, and the function options (loadConfig, createTransport, credentials, openUrl, updateConfig), which the extension names by callback id.
func (f graphExtensionFactory) mcpFactory(scope subprocess.SessionGraphScope, agentDir string) (extension.InlineExtension, error) {
	var options struct {
		LogPath       string   `json:"logPath"`
		StartupWaitMs *float64 `json:"startupWaitMs"`
		graphMcpFunctions
	}
	if f.hasOptions() {
		if err := json.Unmarshal(f.Options, &options); err != nil {
			return nil, f.optionsError(err)
		}
	}
	built := mcpext.Options{LogPath: options.LogPath, AgentDir: agentDir, ConfigDirName: icodingagent.ConfigDirName()}
	if options.StartupWaitMs != nil {
		wait := int(*options.StartupWaitMs)
		built.StartupWaitMs = &wait
	}
	options.apply(scope, &built)
	return mcpext.CreateMcpExtension(built), nil
}

// graphMcpFunctions are the callback ids of the function options, as the extension named them in the option object.
type graphMcpFunctions struct {
	LoadConfig      string `json:"loadConfig"`
	CreateTransport string `json:"createTransport"`
	Credentials     string `json:"credentials"`
	OpenURL         string `json:"openUrl"`
	UpdateConfig    string `json:"updateConfig"`
}

// apply sets the options of the functions the extension passed.
func (f graphMcpFunctions) apply(scope subprocess.SessionGraphScope, options *mcpext.Options) {
	if f.LoadConfig != "" {
		options.LoadConfig = graphMcpLoadConfig(scope, f.LoadConfig)
	}
	if f.CreateTransport != "" {
		options.CreateTransport = graphMcpTransportFactory(scope, f.CreateTransport)
	}
	if f.Credentials != "" {
		options.Credentials = mcpext.NewMcpOAuthCredentialStoreOver(&graphMcpCredentials{scope: scope, id: f.Credentials})
	}
	if f.OpenURL != "" {
		options.OpenURL = func(url string) {
			//portlint:allow ctxbackground Pi's openUrl is a synchronous function of the sign-in flow and carries no signal
			_, _ = scope.Callback(context.Background(), f.OpenURL, mustMarshalArgs(url))
		}
	}
	if f.UpdateConfig != "" {
		options.UpdateConfig = func(entry mcpext.McpServerEntry, patch mcpext.McpServerConfigPatch) error {
			//portlint:allow ctxbackground Pi's updateConfig is a synchronous function of the manager's save and carries no signal
			_, err := scope.Callback(context.Background(), f.UpdateConfig, mustMarshalArgs(graphMcpEntryOf(entry), graphMcpPatch{Enabled: patch.Enabled, Exposure: patch.Exposure}))
			return err
		}
	}
}

func mustMarshalArgs(args ...any) json.RawMessage {
	if args == nil {
		args = []any{}
	}
	//portlint:allow mapkeyorder the arguments are wire structs and strings the callers above build; members have a fixed order
	data, err := json.Marshal(args)
	if err != nil {
		// Every caller passes strings and wire structs, which always encode.
		panic(err)
	}
	return data
}

// graphMcpEntry is Pi's McpServerEntry (config.ts) on the wire.
type graphMcpEntry struct {
	Name     string                    `json:"name"`
	Config   extension.McpServerConfig `json:"config"`
	Source   string                    `json:"source"`
	Scope    string                    `json:"scope,omitempty"`
	Override string                    `json:"override,omitempty"`
}

func graphMcpEntryOf(entry mcpext.McpServerEntry) graphMcpEntry {
	return graphMcpEntry{Name: entry.Name, Config: entry.Config, Source: entry.Source, Scope: entry.Scope, Override: entry.Override}
}

// graphMcpPatch is Pi's McpServerConfigPatch on the wire.
type graphMcpPatch struct {
	Enabled  *bool                 `json:"enabled,omitempty"`
	Exposure extension.McpExposure `json:"exposure,omitempty"`
}

// graphMcpLoadContext is the data of Pi's ExtensionContext that loadConfig callers read: the session's working directory and whether the project is trusted.
type graphMcpLoadContext struct {
	Cwd              string `json:"cwd"`
	IsProjectTrusted bool   `json:"isProjectTrusted"`
}

// graphMcpLoadedConfig is Pi's LoadedMcpConfig on the wire.
type graphMcpLoadedConfig struct {
	Servers            []graphMcpEntry `json:"servers"`
	AutoEnableCodemode *bool           `json:"autoEnableCodemode"`
	Errors             []string        `json:"errors"`
	ProjectConfig      string          `json:"projectConfig"`
}

// graphMcpLoadConfig is the loadConfig option: the extension's function runs when a session starts, with the context's data, and a failure of it is the throw Pi's session_start handler propagates.
func graphMcpLoadConfig(scope subprocess.SessionGraphScope, id string) func(context.Context) mcpext.LoadedMcpConfig {
	return func(ctx context.Context) mcpext.LoadedMcpConfig {
		var data graphMcpLoadContext
		if c := extension.FromContext(ctx); c != nil {
			data.Cwd, _ = c.CWD()
			data.IsProjectTrusted, _ = c.IsProjectTrusted()
		}
		raw, err := scope.Callback(ctx, id, mustMarshalArgs(data))
		if err != nil {
			panic(err)
		}
		var loaded graphMcpLoadedConfig
		if err := json.Unmarshal(raw, &loaded); err != nil {
			panic(fmt.Errorf("loadConfig answered something other than a LoadedMcpConfig: %w", err))
		}
		out := mcpext.LoadedMcpConfig{AutoEnableCodemode: loaded.AutoEnableCodemode, Errors: loaded.Errors, ProjectConfig: loaded.ProjectConfig}
		for _, entry := range loaded.Servers {
			out.Servers = append(out.Servers, mcpext.McpServerEntry{Name: entry.Name, Config: entry.Config, Source: entry.Source, Scope: entry.Scope, Override: entry.Override})
		}
		return out
	}
}

// graphMcpAuthProvider is the AuthProvider of a transport as a graph object the extension calls (`token`, `onUnauthorized`), because the extension's transport asks the host for tokens: the one direction a callback cannot carry.
type graphMcpAuthProvider struct{ provider mcp.AuthProvider }

var _ subprocess.SessionGraphObject = (*graphMcpAuthProvider)(nil)

// Call implements subprocess.SessionGraphObject.
func (g *graphMcpAuthProvider) Call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "token":
		token, err := g.provider.Token(ctx)
		if err != nil {
			return nil, err
		}
		if token == "" {
			return nil, nil
		}
		return token, nil
	case "onUnauthorized":
		handler, ok := g.provider.(mcp.UnauthorizedHandler)
		if !ok {
			return nil, nil
		}
		var args []struct {
			Status    int         `json:"status"`
			Headers   [][2]string `json:"headers"`
			ServerURL string      `json:"serverUrl"`
			Token     string      `json:"token"`
		}
		if err := json.Unmarshal(raw, &args); err != nil || len(args) == 0 {
			return nil, errors.New("onUnauthorized takes the rejected response's data")
		}
		serverURL, err := url.Parse(args[0].ServerURL)
		if err != nil {
			return nil, err
		}
		response := &http.Response{StatusCode: args[0].Status, Header: http.Header{}, Body: http.NoBody}
		for _, header := range args[0].Headers {
			response.Header.Add(header[0], header[1])
		}
		return nil, handler.OnUnauthorized(ctx, mcp.UnauthorizedContext{Response: response, ServerURL: serverURL, Fetch: http.DefaultClient, Token: args[0].Token})
	}
	return nil, fmt.Errorf("an MCP auth provider has no method %s", method)
}

// graphMcpTransportFactory is the createTransport option: the extension's factory answers the id of the transport it made, and the proxy talks to that id.
func graphMcpTransportFactory(scope subprocess.SessionGraphScope, id string) mcpext.TransportFactory {
	return func(entry mcpext.McpServerEntry, cwd string, authProvider mcp.AuthProvider) (mcp.Transport, error) {
		auth := ""
		if authProvider != nil {
			handle, err := scope.Add(&graphMcpAuthProvider{provider: authProvider})
			if err != nil {
				return nil, err
			}
			auth = handle
		}
		//portlint:allow ctxbackground Pi's createTransport is a synchronous function of the connection's attempt and carries no signal
		raw, err := scope.Callback(context.Background(), id, mustMarshalArgs(graphMcpEntryOf(entry), cwd, auth))
		if err != nil {
			if auth != "" {
				scope.Remove(auth)
			}
			return nil, err
		}
		var made struct {
			Transport           string `json:"transport"`
			SetsProtocolVersion bool   `json:"setsProtocolVersion"`
		}
		if err := json.Unmarshal(raw, &made); err != nil || made.Transport == "" {
			if auth != "" {
				scope.Remove(auth)
			}
			return nil, errors.New("createTransport answered no transport")
		}
		transport := newGraphMcpTransport(scope, made.Transport, auth)
		if made.SetsProtocolVersion {
			// Only a transport that has Pi's setProtocolVersion is told the version.
			return &graphMcpVersionedTransport{graphMcpTransport: transport}, nil
		}
		return transport, nil
	}
}

// graphMcpTransport is an mcp.Transport whose methods run in the extension: one callback id takes `[op, ...]` for start, send, abort, close and receive.
//
// `receive` is a long poll: the extension answers it with the events its transport produced since the last poll, in order (message, error, close, placed), and the poll loop delivers them to the listeners on its own goroutine, as the in-tree transports do.
type graphMcpTransport struct {
	mcp.TransportEvents
	scope subprocess.SessionGraphScope
	id    string
	// auth is the handle of the transport's auth provider object, dropped with the transport.
	auth string

	seq     atomic.Uint64
	mu      sync.Mutex
	placed  map[uint64]func()
	pollEnd context.CancelFunc
	polled  chan struct{}
	closed  atomic.Bool
}

var (
	_ mcp.Transport             = (*graphMcpTransport)(nil)
	_ mcp.OrderedSender         = (*graphMcpTransport)(nil)
	_ mcp.ProtocolVersionSetter = (*graphMcpVersionedTransport)(nil)
)

func newGraphMcpTransport(scope subprocess.SessionGraphScope, id, auth string) *graphMcpTransport {
	return &graphMcpTransport{scope: scope, id: id, auth: auth, placed: map[uint64]func(){}}
}

func (t *graphMcpTransport) call(ctx context.Context, args ...any) (json.RawMessage, error) {
	return t.scope.Callback(ctx, t.id, mustMarshalArgs(args...))
}

// Start implements mcp.Transport. It starts the caller's transport, then the poll that delivers what it produces.
func (t *graphMcpTransport) Start() error {
	//portlint:allow ctxbackground mcp.Transport.Start carries no context; the transport owns its lifetime and ends it in Close
	if _, err := t.call(context.Background(), "start"); err != nil {
		return err
	}
	//portlint:allow ctxbackground the poll is the transport's own goroutine, ended by Close
	pollCtx, end := context.WithCancel(context.Background())
	t.mu.Lock()
	t.pollEnd, t.polled = end, make(chan struct{})
	polled := t.polled
	t.mu.Unlock()
	go t.poll(pollCtx, polled)
	return nil
}

// graphMcpEvent is one event of a poll's answer.
type graphMcpEvent struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
	Error   string          `json:"error"`
	Seq     uint64          `json:"seq"`
}

func (t *graphMcpTransport) poll(ctx context.Context, done chan struct{}) {
	defer close(done)
	defer t.EmitClose()
	for {
		raw, err := t.call(ctx, "receive")
		if err != nil {
			if !t.closed.Load() && ctx.Err() == nil {
				t.EmitError(err)
			}
			return
		}
		var answer struct {
			Events []graphMcpEvent `json:"events"`
		}
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.EmitError(fmt.Errorf("the transport's receive answered something other than events: %w", err))
			return
		}
		for _, event := range answer.Events {
			switch event.Type {
			case "message":
				message, err := mcp.ParseJSONRPCMessage(event.Message)
				if err != nil {
					t.EmitError(err)
					continue
				}
				t.EmitMessage(message)
			case "error":
				t.EmitError(errors.New(event.Error))
			case "placed":
				t.mu.Lock()
				placed := t.placed[event.Seq]
				delete(t.placed, event.Seq)
				t.mu.Unlock()
				if placed != nil {
					placed()
				}
			case "close":
				return
			}
		}
	}
}

// graphMcpVersionedTransport is a graphMcpTransport whose extension-side transport has Pi's optional setProtocolVersion.
type graphMcpVersionedTransport struct{ *graphMcpTransport }

// SetProtocolVersion implements mcp.ProtocolVersionSetter: the extension's transport learns the negotiated version.
func (t *graphMcpVersionedTransport) SetProtocolVersion(version string) {
	//portlint:allow ctxbackground setProtocolVersion is a synchronous method of the transport and carries no signal
	_, _ = t.call(context.Background(), "setProtocolVersion", version)
}

// Send implements mcp.Transport.
func (t *graphMcpTransport) Send(ctx context.Context, message mcp.JSONRPCMessage) error {
	return t.send(ctx, message, nil)
}

// SendOrdered implements mcp.OrderedSender: placed is called when the extension's transport reports that the message holds its place, which the poll delivers.
func (t *graphMcpTransport) SendOrdered(ctx context.Context, message mcp.JSONRPCMessage, placed func()) error {
	return t.send(ctx, message, placed)
}

func (t *graphMcpTransport) send(ctx context.Context, message mcp.JSONRPCMessage, placed func()) error {
	seq := t.seq.Add(1)
	if placed != nil {
		t.mu.Lock()
		t.placed[seq] = placed
		t.mu.Unlock()
	}
	// The send's signal (Pi's McpTransportSendOptions.signal) reaches the extension as an abort of this send.
	stop := context.AfterFunc(ctx, func() {
		//portlint:allow ctxbackground the abort is the delivery of the send's own cancellation
		_, _ = t.call(context.Background(), "abort", seq)
	})
	_, err := t.call(ctx, "send", seq, message, placed != nil)
	stop()
	t.mu.Lock()
	left := t.placed[seq]
	delete(t.placed, seq)
	t.mu.Unlock()
	if left != nil {
		left()
	}
	return err
}

// Close implements mcp.Transport. It closes the caller's transport, ends the poll and drops the transport's auth provider.
func (t *graphMcpTransport) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	//portlint:allow ctxbackground mcp.Transport.Close carries no context
	_, err := t.call(context.Background(), "close")
	t.mu.Lock()
	end, polled := t.pollEnd, t.polled
	t.mu.Unlock()
	if end != nil {
		end()
		<-polled
	} else {
		t.EmitClose()
	}
	if t.auth != "" {
		t.scope.Remove(t.auth)
	}
	return err
}

// graphMcpCredentials is the credentials option: the extension's object serves forServer, tokens and remove, and a per-server store with load, save and a refresh lock, all behind one callback id taking `[op, ...]`.
type graphMcpCredentials struct {
	scope subprocess.SessionGraphScope
	id    string
}

var _ mcpext.McpOAuthCredentials = (*graphMcpCredentials)(nil)

func (c *graphMcpCredentials) call(ctx context.Context, args ...any) (json.RawMessage, error) {
	return c.scope.Callback(ctx, c.id, mustMarshalArgs(args...))
}

// ForServer implements mcpext.McpOAuthCredentials.
func (c *graphMcpCredentials) ForServer(name, serverURL string) (mcpext.McpOAuthServerStore, error) {
	//portlint:allow ctxbackground forServer is a synchronous function of Pi's credential store and carries no signal
	raw, err := c.call(context.Background(), "forServer", name, serverURL)
	if err != nil {
		return nil, err
	}
	var made struct {
		Store string `json:"store"`
	}
	if err := json.Unmarshal(raw, &made); err != nil || made.Store == "" {
		return nil, errors.New("forServer answered no store")
	}
	store := &graphMcpServerStore{credentials: c, id: made.Store}
	runtime.AddCleanup(store, func(release graphMcpStoreRelease) {
		//portlint:allow golifetime the release of a collected store is one call that ends with the connection
		go func() {
			//portlint:allow ctxbackground the release of a collected store has no caller
			_, _ = release.credentials.call(context.Background(), "release", release.id)
		}()
	}, graphMcpStoreRelease{credentials: c, id: made.Store})
	return store, nil
}

// graphMcpStoreRelease is what the cleanup of a collected store needs; it holds no reference to the store.
type graphMcpStoreRelease struct {
	credentials *graphMcpCredentials
	id          string
}

// Tokens implements mcpext.McpOAuthCredentials.
func (c *graphMcpCredentials) Tokens(name, serverURL string) *oauth.OAuthTokens {
	//portlint:allow ctxbackground tokens is a synchronous function of Pi's credential store and carries no signal
	raw, err := c.call(context.Background(), "tokens", name, serverURL)
	if err != nil {
		return nil
	}
	var tokens *oauth.OAuthTokens
	if len(raw) == 0 || json.Unmarshal(raw, &tokens) != nil {
		return nil
	}
	return tokens
}

// Remove implements mcpext.McpOAuthCredentials.
func (c *graphMcpCredentials) Remove(name, serverURL string) (bool, error) {
	//portlint:allow ctxbackground remove is a synchronous function of Pi's credential store and carries no signal
	raw, err := c.call(context.Background(), "remove", name, serverURL)
	if err != nil {
		return false, err
	}
	var removed bool
	if err := json.Unmarshal(raw, &removed); err != nil {
		return false, errors.New("remove answered something other than a boolean")
	}
	return removed, nil
}

type graphMcpServerStore struct {
	credentials *graphMcpCredentials
	id          string
}

var _ mcpext.McpOAuthServerStore = (*graphMcpServerStore)(nil)

// Load implements oauth.McpOAuthStateStore.
func (s *graphMcpServerStore) Load(ctx context.Context) (*oauth.McpOAuthState, error) {
	raw, err := s.credentials.call(ctx, "load", s.id)
	if err != nil {
		return nil, err
	}
	var state *oauth.McpOAuthState
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("load answered something other than a state: %w", err)
	}
	return state, nil
}

// Save implements oauth.McpOAuthStateStore.
func (s *graphMcpServerStore) Save(ctx context.Context, state oauth.McpOAuthState) error {
	_, err := s.credentials.call(ctx, "save", s.id, state)
	return err
}

// WithRefreshLock implements mcpext.McpOAuthServerStore. The extension's withRefreshLock function holds its lock while fn runs here: `lock` answers when it entered, and `unlock` lets it return with fn's outcome.
func (s *graphMcpServerStore) WithRefreshLock(ctx context.Context, fn func(ctx context.Context) error) error {
	raw, err := s.credentials.call(ctx, "lock", s.id)
	if err != nil {
		return err
	}
	var held struct {
		Lock string `json:"lock"`
	}
	if err := json.Unmarshal(raw, &held); err != nil || held.Lock == "" {
		return errors.New("lock answered no lock")
	}
	failure := ""
	fnErr := fn(ctx)
	if fnErr != nil {
		failure = fnErr.Error()
	}
	//portlint:allow ctxmapping the lock is released whatever became of ctx: the extension's function waits for it
	_, err = s.credentials.call(context.Background(), "unlock", s.id, held.Lock, failure, fnErr != nil)
	if fnErr != nil {
		return fnErr
	}
	return err
}
