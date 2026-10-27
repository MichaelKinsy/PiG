package sdk

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The function options of Pi's createMcpExtension (extensions/mcp/index.ts McpExtensionOptions): loadConfig, createTransport, credentials, openUrl and updateConfig. They stay in this process. The reference [CreateMcpExtension] returns names them to the host as session graph callbacks when [NewDefaultResourceLoader] builds the loader, and the host runs them with JSON arguments over the connection (the wire of the loader's override options; no request kind is added).

// McpServerEntry is Pi's McpServerEntry (extensions/mcp/config.ts): one configured server.
type McpServerEntry struct {
	Name   string          `json:"name"`
	Config McpServerConfig `json:"config"`
	// Source is the config file that defined the entry, or the path of the extension that registered it.
	Source string `json:"source"`
	// Scope is "global" or "project" for an `mcp.json`, or "extension" for servers registered with RegisterMcpServer.
	Scope string `json:"scope,omitempty"`
	// Override is the project `mcp.json` with an override of this global server's `enabled`, `exposure` or `toolExposure`.
	Override string `json:"override,omitempty"`
}

// LoadedMcpConfig is Pi's LoadedMcpConfig: what a loadConfig function returns.
type LoadedMcpConfig struct {
	Servers []McpServerEntry `json:"servers"`
	// AutoEnableCodemode activates the codemode tool when `codemode` servers connect. Nil is Pi's undefined, true.
	AutoEnableCodemode *bool    `json:"autoEnableCodemode,omitempty"`
	Errors             []string `json:"errors"`
	// ProjectConfig is the project `mcp.json` when the project is trusted, where `/mcp` saves project overrides.
	ProjectConfig string `json:"projectConfig,omitempty"`
}

// McpServerConfigPatch is Pi's McpServerConfigPatch: the settings `/mcp` changes. A nil Enabled and an empty Exposure are Pi's undefined.
type McpServerConfigPatch struct {
	Enabled  *bool       `json:"enabled,omitempty"`
	Exposure McpExposure `json:"exposure,omitempty"`
}

// McpLoadConfigContext is the part of Pi's ExtensionContext that a loadConfig function reads: the session's working directory and whether the project is trusted (`ctx.cwd`, `ctx.isProjectTrusted()`). The rest of the context (its UI and session objects) lives in the host.
type McpLoadConfigContext struct {
	Cwd              string `json:"cwd"`
	IsProjectTrusted bool   `json:"isProjectTrusted"`
}

// McpTransport is Pi's McpTransport (packages/mcp/src/transports/transport.ts): the duplex message stream of one MCP connection. The host's client calls Start, Send and Close from the host; the SDK delivers them to the transport in this process, registers its listeners, and delivers what the transport emits back, in order. A transport that also has `SetProtocolVersion(version string)` (Pi's optional setProtocolVersion) is told the version the connection negotiated.
type McpTransport interface {
	// Start opens the transport.
	Start() error
	// Send writes one JSON-RPC message, as its encoded JSON. It may wait for the peer's HTTP reply, so calls run concurrently. ctx ends once the sender no longer waits for a reply.
	Send(ctx context.Context, message json.RawMessage) error
	// Close closes the transport.
	Close() error
	// OnMessage registers a listener for each JSON-RPC message the transport receives, as its encoded JSON, and returns the function that removes it.
	OnMessage(listener func(message json.RawMessage)) (dispose func())
	// OnError registers a listener for errors that do not close the transport.
	OnError(listener func(err error)) (dispose func())
	// OnClose registers a listener for the close, which the transport reports once.
	OnClose(listener func()) (dispose func())
}

// McpTransportEvents is Pi's TransportEvents: the listener bookkeeping a transport embeds. Emit calls the listeners in registration order on the caller's goroutine; EmitClose fires at most once.
type McpTransportEvents struct {
	mu          sync.Mutex
	next        int
	messages    map[int]func(json.RawMessage)
	errors      map[int]func(error)
	closes      map[int]func()
	closeCalled bool
}

func addListener[F any](mu *sync.Mutex, next *int, listeners *map[int]F, listener F) func() {
	mu.Lock()
	defer mu.Unlock()
	if *listeners == nil {
		*listeners = map[int]F{}
	}
	*next++
	id := *next
	(*listeners)[id] = listener
	return func() {
		mu.Lock()
		defer mu.Unlock()
		delete(*listeners, id)
	}
}

// listenersOf returns the listeners in registration order.
func listenersOf[F any](mu *sync.Mutex, listeners map[int]F) []F {
	mu.Lock()
	defer mu.Unlock()
	ids := make([]int, 0, len(listeners))
	for id := range listeners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]F, len(ids))
	for i, id := range ids {
		out[i] = listeners[id]
	}
	return out
}

// OnMessage implements [McpTransport].
func (t *McpTransportEvents) OnMessage(listener func(json.RawMessage)) func() {
	return addListener(&t.mu, &t.next, &t.messages, listener)
}

// OnError implements [McpTransport].
func (t *McpTransportEvents) OnError(listener func(error)) func() {
	return addListener(&t.mu, &t.next, &t.errors, listener)
}

// OnClose implements [McpTransport].
func (t *McpTransportEvents) OnClose(listener func()) func() {
	return addListener(&t.mu, &t.next, &t.closes, listener)
}

// EmitMessage delivers a received message to the listeners.
func (t *McpTransportEvents) EmitMessage(message json.RawMessage) {
	for _, listener := range listenersOf(&t.mu, t.messages) {
		listener(message)
	}
}

// EmitError reports an error to the listeners.
func (t *McpTransportEvents) EmitError(err error) {
	for _, listener := range listenersOf(&t.mu, t.errors) {
		listener(err)
	}
}

// EmitClose reports the close to the listeners, once.
func (t *McpTransportEvents) EmitClose() {
	t.mu.Lock()
	first := !t.closeCalled
	t.closeCalled = true
	t.mu.Unlock()
	if !first {
		return
	}
	for _, listener := range listenersOf(&t.mu, t.closes) {
		listener()
	}
}

// McpOrderedSender is implemented by a transport whose Send writes messages in the order the writes took their place, such as a stdio pipe: the client writes a request only after the one before it holds its place. SendOrdered calls placed once every later Send is written after message, and otherwise behaves as Send. A transport without it gets placed called just before its Send, as the in-tree HTTP transport does.
type McpOrderedSender interface {
	SendOrdered(ctx context.Context, message json.RawMessage, placed func()) error
}

// McpRejectedResponse is the data of the 401 or 403 response a transport's request got.
type McpRejectedResponse struct {
	Status int `json:"status"`
	// Headers are the response headers in order; the host reads the `WWW-Authenticate` challenge from them.
	Headers [][2]string `json:"headers"`
}

// McpUnauthorizedContext is Pi's UnauthorizedContext as data. Pi's `fetch` and the response's body stay in the host.
type McpUnauthorizedContext struct {
	Response  McpRejectedResponse
	ServerURL string
	// Token is the access token the rejected request carried, if any.
	Token string
}

// McpAuthProvider is Pi's AuthProvider of an HTTP server the host holds for a transport: it supplies bearer tokens and refreshes them after a rejection. The host owns it; its methods call the host.
type McpAuthProvider struct {
	context Context
	handle  string
}

// Token is `token()`: the current access token, nil for none.
func (p *McpAuthProvider) Token() (*string, error) {
	return hostDecoded[*string](p.context, graphCall, map[string]any{"handle": p.handle, "method": "token", "args": []any{}})
}

// OnUnauthorized is `onUnauthorized(context)`: the host refreshes the credentials after the response its request got; the transport retries the request once with what the provider holds then.
func (p *McpAuthProvider) OnUnauthorized(unauthorized McpUnauthorizedContext) error {
	wire := map[string]any{"status": unauthorized.Response.Status, "headers": unauthorized.Response.Headers, "serverUrl": unauthorized.ServerURL}
	if unauthorized.Token != "" {
		wire["token"] = unauthorized.Token
	}
	_, err := hostDecoded[graphDone](p.context, graphCall, map[string]any{"handle": p.handle, "method": "onUnauthorized", "args": []any{wire}})
	return err
}

// McpTransportFactory is Pi's McpTransportFactory: it creates the transport of a server. authProvider is nil for a server that uses no OAuth. The provider lives until the transport closes.
type McpTransportFactory func(entry McpServerEntry, cwd string, authProvider *McpAuthProvider) (McpTransport, error)

// McpOAuthTokens is Pi's OAuthTokens.
type McpOAuthTokens struct {
	AccessToken  string   `json:"access_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    *float64 `json:"expires_in,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	IDToken      string   `json:"id_token,omitempty"`
}

// McpOAuthState is Pi's McpOAuthState (client registration, tokens, pending PKCE verifier) as the JSON the host writes; a store keeps and returns it whole.
type McpOAuthState = json.RawMessage

// McpOAuthServerStore is Pi's McpOAuthStateStore plus the refresh lock of one server.
type McpOAuthServerStore interface {
	// Load returns the stored state, nil when there is none.
	Load() (McpOAuthState, error)
	// Save stores the state.
	Save(state McpOAuthState) error
	// WithRefreshLock runs fn while no other process refreshes the server's tokens.
	WithRefreshLock(fn func() error) error
}

// McpOAuthCredentialStore is what the MCP extension asks of its `credentials` option (Pi's McpOAuthCredentialStore: per-server OAuth state in `mcp-auth.json` by default).
type McpOAuthCredentialStore interface {
	// ForServer returns the store of the server with the given name and URL.
	ForServer(name, serverURL string) (McpOAuthServerStore, error)
	// Tokens returns the stored tokens of the server, nil when there are none.
	Tokens(name, serverURL string) (*McpOAuthTokens, error)
	// Remove deletes the stored state of the server and returns whether credentials were stored for it.
	Remove(name, serverURL string) (bool, error)
}

// mcpFunctionOptions are the function options of McpExtensionOptions.
type mcpFunctionOptions struct {
	loadConfig      func(McpLoadConfigContext) (LoadedMcpConfig, error)
	createTransport McpTransportFactory
	credentials     McpOAuthCredentialStore
	openURL         func(url string)
	updateConfig    func(entry McpServerEntry, patch McpServerConfigPatch) error
}

// register names each function to the host in wire, the option object of the reference.
func (f *mcpFunctionOptions) register(c Context, wire map[string]any) {
	if f == nil {
		return
	}
	if f.loadConfig != nil {
		wire["loadConfig"] = c.callbackID(func(args []json.RawMessage) (any, error) {
			var ctx McpLoadConfigContext
			if err := decodeGraphArg(args, 0, &ctx); err != nil {
				return nil, err
			}
			return f.loadConfig(ctx)
		})
	}
	if f.openURL != nil {
		wire["openUrl"] = c.callbackID(func(args []json.RawMessage) (any, error) {
			var url string
			if err := decodeGraphArg(args, 0, &url); err != nil {
				return nil, err
			}
			f.openURL(url)
			return nil, nil
		})
	}
	if f.updateConfig != nil {
		wire["updateConfig"] = c.callbackID(func(args []json.RawMessage) (any, error) {
			var entry McpServerEntry
			var patch McpServerConfigPatch
			if err := decodeGraphArg(args, 0, &entry); err != nil {
				return nil, err
			}
			if err := decodeGraphArg(args, 1, &patch); err != nil {
				return nil, err
			}
			return nil, f.updateConfig(entry, patch)
		})
	}
	if f.createTransport != nil {
		wire["createTransport"] = c.callbackID(func(args []json.RawMessage) (any, error) {
			var entry McpServerEntry
			var cwd, auth string
			if err := decodeGraphArg(args, 0, &entry); err != nil {
				return nil, err
			}
			if err := decodeGraphArg(args, 1, &cwd); err != nil {
				return nil, err
			}
			if err := decodeGraphArg(args, 2, &auth); err != nil {
				return nil, err
			}
			var provider *McpAuthProvider
			if auth != "" {
				provider = &McpAuthProvider{context: c, handle: auth}
			}
			transport, err := f.createTransport(entry, cwd, provider)
			if err != nil {
				return nil, err
			}
			if transport == nil {
				return nil, errors.New("createTransport returned no transport")
			}
			_, setsVersion := transport.(interface{ SetProtocolVersion(string) })
			return map[string]any{"transport": newMcpTransportShim(c, transport).id, "setsProtocolVersion": setsVersion}, nil
		})
	}
	if f.credentials != nil {
		wire["credentials"] = c.callbackID((&mcpCredentialsShim{store: f.credentials}).serve)
	}
}

func decodeGraphArg(args []json.RawMessage, i int, into any) error {
	if i >= len(args) || len(args[i]) == 0 || string(args[i]) == "null" {
		return nil
	}
	return json.Unmarshal(args[i], into)
}

// mcpEvent is one thing a transport produced, in the order it happened: a message, an error, the close, or a send that took its place.
type mcpEvent struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message,omitempty"`
	Error   string          `json:"error,omitempty"`
	Seq     uint64          `json:"seq,omitempty"`
}

// mcpTransportShim serves one transport of the caller to the host: the host's ops arrive as one session graph callback, and what the transport produces is queued for the host's long poll (`receive`).
type mcpTransportShim struct {
	id        string
	transport McpTransport
	registry  *graphCallbackRegistry

	mu     sync.Mutex
	events []mcpEvent
	wake   chan struct{}
	closed bool
	aborts map[uint64]context.CancelFunc
}

func newMcpTransportShim(c Context, transport McpTransport) *mcpTransportShim {
	s := &mcpTransportShim{transport: transport, registry: &c.ext.graphCallbacks, wake: make(chan struct{}, 1), aborts: map[uint64]context.CancelFunc{}}
	s.id = c.ext.graphCallbacks.add(s.serve)
	transport.OnMessage(func(message json.RawMessage) { s.push(mcpEvent{Type: "message", Message: message}) })
	transport.OnError(func(err error) { s.push(mcpEvent{Type: "error", Error: err.Error()}) })
	transport.OnClose(s.pushClose)
	return s
}

func (s *mcpTransportShim) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *mcpTransportShim) push(event mcpEvent) {
	s.mu.Lock()
	if !s.closed {
		s.events = append(s.events, event)
	}
	s.mu.Unlock()
	s.signal()
}

// pushClose queues the close once; nothing is queued after it.
func (s *mcpTransportShim) pushClose() {
	s.mu.Lock()
	if !s.closed {
		s.events = append(s.events, mcpEvent{Type: "close"})
		s.closed = true
	}
	s.mu.Unlock()
	s.signal()
}

func (s *mcpTransportShim) serve(args []json.RawMessage) (any, error) {
	var op string
	if err := decodeGraphArg(args, 0, &op); err != nil {
		return nil, err
	}
	switch op {
	case "start":
		return nil, s.transport.Start()
	case "setProtocolVersion":
		var version string
		if err := decodeGraphArg(args, 1, &version); err != nil {
			return nil, err
		}
		if setter, ok := s.transport.(interface{ SetProtocolVersion(string) }); ok {
			setter.SetProtocolVersion(version)
		}
		return nil, nil
	case "receive":
		return s.receive(), nil
	case "send":
		return nil, s.send(args)
	case "abort":
		var seq uint64
		if err := decodeGraphArg(args, 1, &seq); err != nil {
			return nil, err
		}
		s.mu.Lock()
		cancel := s.aborts[seq]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil, nil
	case "close":
		err := s.transport.Close()
		s.pushClose()
		// The host closed the transport and waits for its poll to see the close; the id is not used again.
		defer s.registry.remove(s.id)
		return nil, err
	}
	return nil, fmt.Errorf("an MCP transport has no operation %s", op)
}

// receive answers the host's poll: the events since the last poll, or the next ones when there are none.
func (s *mcpTransportShim) receive() map[string]any {
	for {
		s.mu.Lock()
		if len(s.events) > 0 {
			events := s.events
			s.events = nil
			s.mu.Unlock()
			return map[string]any{"events": events}
		}
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return map[string]any{"events": []mcpEvent{{Type: "close"}}}
		}
		<-s.wake
	}
}

func (s *mcpTransportShim) send(args []json.RawMessage) error {
	if len(args) < 4 {
		return errors.New("send takes a sequence number, a message and the ordered flag")
	}
	var seq uint64
	var ordered bool
	if err := decodeGraphArg(args, 1, &seq); err != nil {
		return err
	}
	message := args[2]
	if err := decodeGraphArg(args, 3, &ordered); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.aborts[seq] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.aborts, seq)
		s.mu.Unlock()
		cancel()
	}()
	var once sync.Once
	placed := func() {
		once.Do(func() {
			if ordered {
				s.push(mcpEvent{Type: "placed", Seq: seq})
			}
		})
	}
	var err error
	if sender, ok := s.transport.(McpOrderedSender); ok && ordered {
		err = sender.SendOrdered(ctx, message, placed)
	} else {
		placed()
		err = s.transport.Send(ctx, message)
	}
	// A transport that never calls placed must not hold up the sends after this one.
	placed()
	return err
}

// mcpCredentialsShim serves the caller's credential store: the host's ops arrive as one callback.
type mcpCredentialsShim struct {
	store McpOAuthCredentialStore

	mu     sync.Mutex
	next   atomic.Uint64
	stores map[string]McpOAuthServerStore
	locks  map[string]*mcpRefreshLock
}

// mcpRefreshLock is a refresh lock the caller's store holds on behalf of the host: its function returns when the host unlocks.
type mcpRefreshLock struct {
	unlock chan error
	done   chan error
}

func (c *mcpCredentialsShim) serve(args []json.RawMessage) (any, error) {
	var op string
	if err := decodeGraphArg(args, 0, &op); err != nil {
		return nil, err
	}
	str := func(i int) (string, error) {
		var value string
		err := decodeGraphArg(args, i, &value)
		return value, err
	}
	switch op {
	case "forServer", "tokens", "remove":
		name, err := str(1)
		if err != nil {
			return nil, err
		}
		serverURL, err := str(2)
		if err != nil {
			return nil, err
		}
		switch op {
		case "tokens":
			return c.store.Tokens(name, serverURL)
		case "remove":
			return c.store.Remove(name, serverURL)
		}
		store, err := c.store.ForServer(name, serverURL)
		if err != nil {
			return nil, err
		}
		if store == nil {
			return nil, errors.New("forServer returned no store")
		}
		id := "s" + strconv.FormatUint(c.next.Add(1), 10)
		c.mu.Lock()
		if c.stores == nil {
			c.stores = map[string]McpOAuthServerStore{}
		}
		c.stores[id] = store
		c.mu.Unlock()
		return map[string]any{"store": id}, nil
	}
	id, err := str(1)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	store := c.stores[id]
	if op == "release" {
		delete(c.stores, id)
	}
	c.mu.Unlock()
	if op == "release" {
		return nil, nil
	}
	if store == nil {
		return nil, fmt.Errorf("unknown credential store %s", id)
	}
	switch op {
	case "load":
		return store.Load()
	case "save":
		if len(args) < 3 {
			return nil, errors.New("save takes a state")
		}
		return nil, store.Save(args[2])
	case "lock":
		return c.lock(id, store)
	case "unlock":
		lockID, err := str(2)
		if err != nil {
			return nil, err
		}
		var failure string
		var failed bool
		if err := decodeGraphArg(args, 3, &failure); err != nil {
			return nil, err
		}
		if err := decodeGraphArg(args, 4, &failed); err != nil {
			return nil, err
		}
		c.mu.Lock()
		held := c.locks[lockID]
		delete(c.locks, lockID)
		c.mu.Unlock()
		if held == nil {
			return nil, fmt.Errorf("unknown refresh lock %s", lockID)
		}
		if failed {
			held.unlock <- errors.New(failure)
		} else {
			held.unlock <- nil
		}
		return nil, <-held.done
	}
	return nil, fmt.Errorf("a credential store has no operation %s", op)
}

// lock runs the store's withRefreshLock with a function that waits for the host's unlock, and answers when the function entered.
func (c *mcpCredentialsShim) lock(storeID string, store McpOAuthServerStore) (any, error) {
	held := &mcpRefreshLock{unlock: make(chan error, 1), done: make(chan error, 1)}
	lockID := storeID + "." + strconv.FormatUint(c.next.Add(1), 10)
	entered := make(chan struct{})
	go func() {
		held.done <- store.WithRefreshLock(func() error {
			close(entered)
			return <-held.unlock
		})
	}()
	select {
	case <-entered:
	case err := <-held.done:
		// The store's withRefreshLock returned without running the function: it could not lock.
		if err == nil {
			err = errors.New("withRefreshLock returned without running its function")
		}
		return nil, err
	}
	c.mu.Lock()
	if c.locks == nil {
		c.locks = map[string]*mcpRefreshLock{}
	}
	c.locks[lockID] = held
	c.mu.Unlock()
	return map[string]any{"lock": lockID}, nil
}
