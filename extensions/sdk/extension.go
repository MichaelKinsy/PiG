package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Schema is a JSON Schema object for tool parameter definitions.
type Schema = map[string]any

// ToolPrepareArgumentsFunc transforms compatibility inputs before schema validation and execution.
type ToolPrepareArgumentsFunc func(params map[string]any) (map[string]any, error)

// ToolFunc is the handler for a tool execution.
// params is the decoded JSON params from the LLM.
// Return any serializable result, or an error.
type ToolFunc func(ctx Context, params map[string]any) (any, error)

// CommandFunc is the handler for a slash command.
type CommandFunc func(ctx Context, args string) error

// EventFunc is the handler for a lifecycle event.
// Return a result value to pass data back to the host (e.g.,
// BeforeAgentStartEventResult with SystemPrompt override).
// Most handlers return (nil, nil) to acknowledge without data.
type EventFunc func(ctx Context, data map[string]any) (any, error)

type ProjectTrustDecision string

const (
	ProjectTrustYes       ProjectTrustDecision = "yes"
	ProjectTrustNo        ProjectTrustDecision = "no"
	ProjectTrustUndecided ProjectTrustDecision = "undecided"
)

type ProjectTrustResult struct {
	Trusted  ProjectTrustDecision `json:"trusted"`
	Remember bool                 `json:"remember,omitempty"`
}

type ProjectTrustFunc func(ctx Context, data map[string]any) (ProjectTrustResult, error)

// ShortcutFunc is the handler for a keyboard shortcut.
type ShortcutFunc func(ctx Context) error

// FlagType is the supported CLI flag type.
type FlagType string

const (
	FlagBoolean FlagType = "boolean"
	FlagString  FlagType = "string"
)

// FlagOptions describes an extension CLI flag.
type FlagOptions struct {
	Description string
	Type        FlagType
	Default     any
}

// ProviderConfig registers or overrides a model provider.
type ProviderConfig map[string]any

// MessageRenderOptions is the renderer request options.
type MessageRenderOptions struct {
	Expanded bool `json:"expanded"`
	// OutputPad is the horizontal padding configured by the outputPad setting.
	OutputPad int `json:"outputPad"`
}

// RendererFunc renders a custom message into terminal lines for the host.
type RendererFunc func(ctx Context, message map[string]any, options MessageRenderOptions, width int) ([]string, error)

// EntryRenderOptions is the entry-renderer request options.
type EntryRenderOptions struct {
	Expanded bool `json:"expanded"`
}

// EntryRendererFunc renders a custom session entry into terminal lines for the host.
type EntryRendererFunc func(ctx Context, entry map[string]any, options EntryRenderOptions, width int) ([]string, error)

// Factory constructs an extension instance for generated standalone or packed
// runners. Factory-style packages should expose a function such as:
//
//	func Extension() *sdk.Extension
//
// Generated runners call that factory, then run the returned extension over a
// host-provided subprocess socket.
type Factory func() *Extension

// Extension is the builder for a subprocess extension. Create with [New],
// register tools/commands/events, then call [Extension.Run].
type Extension struct {
	name     string
	tools    []toolDef
	commands []cmdDef
	handlers []handlerDef

	eventMu     sync.Mutex
	eventNextID int

	toolFuncs          map[string]ToolFunc
	toolPrepareFuncs   map[string]ToolPrepareArgumentsFunc
	commandFuncs       map[string]CommandFunc
	eventFuncs         map[int]EventFunc
	shortcutFuncs      map[string]ShortcutFunc
	rendererFuncs      map[string]RendererFunc
	entryRendererFuncs map[string]EntryRendererFunc
	flagDefaults       map[string]any
	shortcuts          []shortcutDef
	flags              []flagDef
	providers          []providerDef
	renderers          []rendererDef
	entryRenderers     []rendererDef

	// terminalInputMu guards terminalInputFuncs, which the host consults
	// synchronously while it holds the user's keystroke.
	terminalInputMu     sync.RWMutex
	terminalInputFuncs  []terminalInputSub
	terminalInputNextID uint64

	// widthChangeMu guards widthChangeFuncs, notified after e.width is updated
	// so a handler that reads Context.Width sees the new value.
	widthChangeMu     sync.RWMutex
	widthChangeFuncs  []widthChangeSub
	widthChangeNextID uint64

	// oauthProviders holds the OAuth closures for providers registered with an
	// "oauth" capability, keyed by provider name for oauth_* dispatch.
	oauthProviders map[string]*OAuthProvider

	// conn is set during Run().
	conn *conn

	// session is the local session mirror, kept in sync by incremental
	// appends from the host. GetBranch() and GetEntries() read from it
	// instead of fetching the entire log over IPC.
	session sessionMirror

	// session info from host (set after ready message).
	mu            sync.RWMutex
	sessionName   string
	cwd           string
	mode          string
	width         int
	height        int
	model         string
	modelProvider string
	sessionFile   string

	requestsMu sync.Mutex
	requests   map[string]context.CancelFunc
	requestWG  sync.WaitGroup
	runCtx     context.Context
	runCancel  context.CancelFunc

	modelStreamSeq atomic.Uint64
	modelStreamsMu sync.RWMutex
	modelStreams   map[string]*ModelEventStream

	overlaySeq atomic.Uint64
	overlaysMu sync.RWMutex
	overlays   map[string]*remoteOverlay

	// notifyMu guards notifyHandled and notifyChanged, which let a host-call
	// goroutine wait for notifications that preceded the call result.
	notifyMu      sync.Mutex
	notifyHandled uint64
	notifyChanged chan struct{}
}

const (
	remoteComponentStopTimeout  = time.Second
	extensionHandlerStopTimeout = 2 * time.Second
	remoteRenderInterval        = 16 * time.Millisecond
)

type remoteOverlay struct {
	mu         sync.Mutex
	component  RemoteComponent
	lastLines  []string
	seq        uint64
	closed     bool
	lastRender time.Time
	active     atomic.Bool
	invalidate chan struct{}
	input      chan string
	stopCh     chan struct{}
	stopped    chan struct{}
	stopOnce   sync.Once
}

func newRemoteOverlay(component RemoteComponent) *remoteOverlay {
	return &remoteOverlay{component: component}
}

func (o *remoteOverlay) start(conn *conn, key string, width func() int) (err error) {
	o.invalidate = make(chan struct{}, 1)
	o.input = make(chan string, 64)
	o.stopCh = make(chan struct{})
	o.active.Store(true)
	if invalidator, ok := o.component.(RemoteComponentInvalidator); ok {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("attach focused invalidation: %v", recovered)
				}
			}()
			invalidator.SetInvalidate(o.requestRender)
		}()
		if err != nil {
			o.active.Store(false)
			return err
		}
	}
	o.stopped = make(chan struct{})
	go func() {
		defer close(o.stopped)
		for o.active.Load() {
			select {
			case data := <-o.input:
				if !o.active.Load() {
					return
				}
				o.handleInput(conn, key, data, width())
			case <-o.invalidate:
				if !o.active.Load() {
					return
				}
				if !o.waitRenderSlot() {
					return
				}
				if err := o.render(conn, key, width()); err != nil {
					o.closeWithError(conn, key, err)
				}
			case <-o.stopCh:
				return
			}
		}
	}()
	return nil
}

func (o *remoteOverlay) waitRenderSlot() bool {
	o.mu.Lock()
	delay := time.Until(o.lastRender.Add(remoteRenderInterval))
	o.mu.Unlock()
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-o.stopCh:
		return false
	}
}

func (o *remoteOverlay) requestRender() {
	if !o.active.Load() {
		return
	}
	select {
	case o.invalidate <- struct{}{}:
	default:
	}
}

func (o *remoteOverlay) enqueueInput(conn *conn, key, data string) {
	if !o.active.Load() {
		return
	}
	select {
	case o.input <- data:
	default:
		o.closeWithError(conn, key, errors.New("focused input queue is full"))
	}
}

func (o *remoteOverlay) stop() bool {
	o.active.Store(false)
	if invalidator, ok := o.component.(RemoteComponentInvalidator); ok {
		func() {
			defer func() { _ = recover() }()
			invalidator.SetInvalidate(nil)
		}()
	}
	if o.stopped == nil {
		return true
	}
	o.stopOnce.Do(func() { close(o.stopCh) })
	select {
	case <-o.stopped:
		return true
	case <-time.After(remoteComponentStopTimeout):
		return false
	}
}

func (o *remoteOverlay) closeWithError(conn *conn, key string, err error) {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	o.closed = true
	o.active.Store(false)
	o.mu.Unlock()
	_ = conn.notify("ui.custom.close", map[string]any{"key": key, "error": err.Error()})
}

func (o *remoteOverlay) handleInput(conn *conn, key, data string, width int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil && !o.closed {
			o.closed = true
			_ = conn.notify("ui.custom.close", map[string]any{"key": key, "error": fmt.Sprintf("focused input panicked: %v", recovered)})
		}
	}()
	if o.closed {
		return
	}
	result, err := o.component.HandleInput(data)
	if err != nil {
		o.closed = true
		_ = conn.notify("ui.custom.close", map[string]any{"key": key, "error": err.Error()})
		return
	}
	if result.Done {
		o.closed = true
		o.active.Store(false)
		if err := conn.notify("ui.custom.close", map[string]any{"key": key, "result": result.Value}); err != nil {
			_ = conn.notify("ui.custom.close", map[string]any{"key": key, "error": "encode focused result: " + err.Error()})
		}
		return
	}
	_ = o.renderLocked(conn, key, width)
}

func (o *remoteOverlay) render(conn *conn, key string, width int) (err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("focused render panicked: %v", recovered)
		}
	}()
	return o.renderLocked(conn, key, width)
}

func (o *remoteOverlay) renderLocked(conn *conn, key string, width int) error {
	if o.closed {
		return nil
	}
	lines := o.component.Render(width)
	o.lastRender = time.Now()
	if slices.Equal(lines, o.lastLines) {
		return nil
	}
	o.lastLines = append(o.lastLines[:0], lines...)
	o.seq++
	return conn.notify("ui.custom.render", map[string]any{"key": key, "lines": lines, "width": width, "seq": o.seq})
}

// New creates a new extension with the given name.
// The name must match the identity of the selected Package, Piglet, or exact path.
func New(name string) *Extension {
	return &Extension{
		name:               name,
		toolFuncs:          make(map[string]ToolFunc),
		toolPrepareFuncs:   make(map[string]ToolPrepareArgumentsFunc),
		commandFuncs:       make(map[string]CommandFunc),
		eventFuncs:         make(map[int]EventFunc),
		shortcutFuncs:      make(map[string]ShortcutFunc),
		rendererFuncs:      make(map[string]RendererFunc),
		entryRendererFuncs: make(map[string]EntryRendererFunc),
		flagDefaults:       make(map[string]any),
		requests:           make(map[string]context.CancelFunc),
		modelStreams:       make(map[string]*ModelEventStream),
		overlays:           make(map[string]*remoteOverlay),
	}
}

// Name returns the extension's registered name.
func (e *Extension) Name() string { return e.name }

// Tool registers a tool that the LLM can invoke.
func (e *Extension) Tool(name, description string, schema Schema, handler ToolFunc) {
	e.tools = append(e.tools, toolDef{
		Name:        name,
		Description: description,
		Parameters:  schema,
	})
	e.toolFuncs[name] = handler
}

// ToolWithPrepareArguments registers a tool with a local pre-validation argument transform.
func (e *Extension) ToolWithPrepareArguments(name, description string, schema Schema, prepare ToolPrepareArgumentsFunc, handler ToolFunc) {
	e.Tool(name, description, schema, handler)
	e.toolPrepareFuncs[name] = prepare
}

// ToolWithGuidelines registers a tool with system prompt guidelines.
// Guidelines are bullets injected into the system prompt's Guidelines section
// when this tool is active. Each guideline must name the tool it refers to -
// write "Use my_tool when..." not "Use this tool when...".
// Mirrors upstream pi's promptGuidelines on ToolDefinition.
func (e *Extension) ToolWithGuidelines(name, description string, schema Schema, guidelines []string, handler ToolFunc) {
	e.tools = append(e.tools, toolDef{
		Name:             name,
		Description:      description,
		Parameters:       schema,
		PromptGuidelines: guidelines,
	})
	e.toolFuncs[name] = handler
}

// ToolWithSource registers a tool with an explicit source identifier.
// Source overrides the default extension-name attribution Piglet tool
// scoping reads, allowing extensions that wrap external tool sources (e.g.
// MCP servers) to provide per-tool provenance. GetAllTools reports upstream's
// SourceInfo for the tool, not this source.
// pig additive (D23): ToolWithSource adds per-tool source attribution.
// Example: ext.ToolWithSource("list_models", desc, schema, "mcp:mctl-platform", guidelines, handler)
func (e *Extension) ToolWithSource(name, description string, schema Schema, source string, guidelines []string, handler ToolFunc) {
	e.tools = append(e.tools, toolDef{
		Name:             name,
		Description:      description,
		Parameters:       schema,
		PromptGuidelines: guidelines,
		Source:           source,
	})
	e.toolFuncs[name] = handler
}

// ConstrainedSampling is a provider-side constrained sampling request for a tool.
// Type is "json_schema" or "grammar". For json_schema, Strict is "prefer" or
// "require". For grammar, Variants maps a grammar format ("openai_lark",
// "openai_regex") to its definition. Mirrors upstream ConstrainedSamplingConfig.
type ConstrainedSampling struct {
	Type     string            `json:"type"`
	Strict   string            `json:"strict,omitempty"`
	Variants map[string]string `json:"variants,omitempty"`
}

// ToolWithConstrainedSampling registers a tool that requests provider-side
// constrained sampling. The host forwards the request to the provider, which (for
// OpenAI-compatible providers) turns a grammar request into a custom grammar
// tool. Mirrors upstream ToolDefinition.constrainedSampling.
func (e *Extension) ToolWithConstrainedSampling(name, description string, schema Schema, sampling ConstrainedSampling, handler ToolFunc) {
	e.tools = append(e.tools, toolDef{
		Name:                name,
		Description:         description,
		Parameters:          schema,
		ConstrainedSampling: sampling,
	})
	e.toolFuncs[name] = handler
}

// Command registers a slash command (e.g. /hello).
func (e *Extension) Command(name, description string, handler CommandFunc) {
	e.commands = append(e.commands, cmdDef{
		Name:        name,
		Description: description,
	})
	e.commandFuncs[name] = handler
}

// Shortcut registers a keyboard shortcut handler (e.g. "ctrl+shift+s").
func (e *Extension) Shortcut(key, description string, handler ShortcutFunc) {
	e.shortcuts = append(e.shortcuts, shortcutDef{
		Key:         key,
		Description: description,
	})
	e.shortcutFuncs[key] = handler
}

// Flag registers a CLI flag declaration for the extension.
func (e *Extension) Flag(name string, options FlagOptions) {
	def := flagDef{
		Name:        name,
		Description: options.Description,
		Type:        string(options.Type),
	}
	if options.Default != nil {
		data, _ := json.Marshal(options.Default)
		def.Default = data
	}
	e.flags = append(e.flags, def)
	e.flagDefaults[name] = options.Default
}

// RegisterProvider registers or overrides a model provider. When config carries
// an *OAuthProvider under the "oauth" key, its closures are stored for oauth_*
// dispatch and the wire config gets a serializable capability descriptor in
// their place.
func (e *Extension) RegisterProvider(name string, config ProviderConfig) {
	if raw, ok := config["oauth"]; ok {
		if provider, ok := raw.(*OAuthProvider); ok && provider != nil {
			e.registerOAuthProvider(name, provider)
			cfg := make(ProviderConfig, len(config))
			maps.Copy(cfg, config)
			cfg["oauth"] = oauthConfigFor(name, provider)
			config = cfg
		}
	}
	data, _ := json.Marshal(config)
	e.providers = append(e.providers, providerDef{Name: name, Config: data})
}

// UnregisterProvider removes a previously queued provider registration.
func (e *Extension) UnregisterProvider(name string) {
	filtered := e.providers[:0]
	for _, provider := range e.providers {
		if provider.Name != name {
			filtered = append(filtered, provider)
		}
	}
	e.providers = filtered
}

// MessageRenderer registers a custom message renderer.
func (e *Extension) MessageRenderer(customType string, handler RendererFunc) {
	e.renderers = append(e.renderers, rendererDef{CustomType: customType})
	e.rendererFuncs[customType] = handler
}

// EntryRenderer registers a custom session-entry renderer.
func (e *Extension) EntryRenderer(customType string, handler EntryRendererFunc) {
	e.entryRenderers = append(e.entryRenderers, rendererDef{CustomType: customType})
	e.entryRendererFuncs[customType] = handler
}

// OnSessionStart registers a handler for the session_start event.
func (e *Extension) OnSessionStart(handler EventFunc) func() {
	return e.OnEvent("session_start", handler)
}

// OnSessionShutdown registers a handler for the session_shutdown event.
func (e *Extension) OnSessionShutdown(handler EventFunc) func() {
	return e.OnEvent("session_shutdown", handler)
}

// OnToolResult registers a handler for the tool_result event.
func (e *Extension) OnToolResult(handler EventFunc) func() {
	return e.OnEvent("tool_result", handler)
}

// OnProjectTrust registers the pre-runtime project_trust handler. Completion,
// cancellation, and errors are awaited by the host.
func (e *Extension) OnProjectTrust(handler ProjectTrustFunc) func() {
	return e.OnEvent("project_trust", func(ctx Context, data map[string]any) (any, error) {
		return handler(ctx, data)
	})
}

// OnEvent registers a handler and returns an idempotent unsubscribe function.
func (e *Extension) OnEvent(eventName string, handler EventFunc) func() {
	e.eventMu.Lock()
	e.eventNextID++
	handlerID := e.eventNextID
	e.handlers = append(e.handlers, handlerDef{Event: eventName, HandlerID: handlerID})
	e.eventFuncs[handlerID] = handler
	conn := e.conn
	e.eventMu.Unlock()

	if conn != nil {
		// Upstream's pi.on throws when the runtime refuses it; this API has no
		// error result, so report the refusal where the host shows extension
		// output instead of dropping it.
		if result, err := conn.call("event.subscribe", map[string]any{"event": eventName, "handlerId": handlerID}); err != nil || (result != nil && result.Error != nil) {
			reportHostCallFailure("event.subscribe", err, result)
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			e.eventMu.Lock()
			conn := e.conn
			if conn == nil {
				for i, declaration := range e.handlers {
					if declaration.HandlerID == handlerID {
						e.handlers = append(e.handlers[:i:i], e.handlers[i+1:]...)
						break
					}
				}
				delete(e.eventFuncs, handlerID)
			}
			e.eventMu.Unlock()
			if conn != nil {
				if result, err := conn.call("event.unsubscribe", map[string]any{"event": eventName, "handlerId": handlerID}); err != nil || (result != nil && result.Error != nil) {
					reportHostCallFailure("event.unsubscribe", err, result)
				}
			}
		})
	}
}

// Run connects to the host, registers, and processes messages until shutdown.
// This blocks until the host sends a shutdown message or the connection closes.
// The socket path comes from the PIG_EXT_SOCKET environment variable.
func (e *Extension) Run() error {
	sockPath := os.Getenv("PIG_EXT_SOCKET")
	if sockPath == "" {
		return fmt.Errorf("PIG_EXT_SOCKET not set: extension must be launched by pig")
	}
	return e.RunWithSocket(sockPath)
}

// RunWithSocket connects to the host at the given socket path. Use this for
// testing extensions without the PIG_EXT_SOCKET environment variable.
func (e *Extension) RunWithSocket(sockPath string) error {
	// The extension author or Pig host selects this local Unix socket; no network URL is resolved.
	//nolint:gosec // G704 models arbitrary network input, not a required local IPC endpoint.
	nc, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to host: %w", err)
	}
	return e.RunWithConn(nc)
}

// RunWithConn serves the extension over an already-connected net.Conn, with no
// subprocess or socket. A fused Piglet Binary uses this to run the extension
// inside the host process over an in-memory pipe. The connection is closed when the serve
// loop ends. Mirrors RunWithSocket's handshake and main loop.
func (e *Extension) RunWithConn(nc net.Conn) error {
	defer func() { _ = nc.Close() }()

	e.runCtx, e.runCancel = context.WithCancel(context.Background())
	defer e.runCancel()
	e.eventMu.Lock()
	e.conn = newConn(nc)
	conn := e.conn
	e.eventMu.Unlock()
	conn.start()

	// Send register message.
	if err := conn.send(envelope{
		Type: msgRegister,
		Register: &registerMsg{
			Name:           e.name,
			Tools:          e.tools,
			Commands:       e.commands,
			Shortcuts:      e.shortcuts,
			Handlers:       e.handlers,
			Flags:          e.flags,
			Providers:      e.providers,
			Renderers:      e.renderers,
			EntryRenderers: e.entryRenderers,
		},
	}); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	// Wait for ready.
	env, ok := <-conn.incoming
	if !ok {
		return fmt.Errorf("connection closed before ready")
	}
	if env.Type != msgReady || env.Ready == nil {
		return fmt.Errorf("expected ready message, got %s", env.Type)
	}
	e.mu.Lock()
	e.sessionName = env.Ready.SessionName
	e.cwd = env.Ready.Cwd
	e.mode = env.Ready.Mode
	e.width = env.Ready.Width
	e.height = env.Ready.Height
	e.model = env.Ready.Model
	e.mu.Unlock()

	// Process the initial State snapshot embedded in the Ready message.
	// This carries the same model map (including provider) that
	// state_update notifies deliver later, ensuring ModelProvider() and
	// ModelQualified() are populated before session_start fires.
	// Wrap in {"state": ...} to match the state_update notify format.
	if len(env.Ready.State) > 0 {
		wrapped := []byte(`{"state":`)
		wrapped = append(wrapped, env.Ready.State...)
		wrapped = append(wrapped, '}')
		e.handleNotify(envelope{
			Notify: &notifyMsg{
				Method: "state_update",
				Args:   wrapped,
			},
		})
	}

	// Main message loop.
	return e.loop()
}

func (e *Extension) loop() error {
	for env := range e.conn.incoming {
		switch env.Type {
		case msgRequest:
			e.requestWG.Go(func() { e.handleRequest(env.ID, env.Request) })
		case msgCancel:
			e.cancelRequest(env)
		case msgNotify:
			e.handleNotify(env)
			e.markNotifyHandled()
		case msgShutdown:
			return e.stopRequests()
		}
	}
	return e.stopRequests()
}

func (e *Extension) stopRequests() error {
	if e.runCancel != nil {
		e.runCancel()
	}
	e.cancelAllRequests()
	done := make(chan struct{})
	go func() {
		e.requestWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(extensionHandlerStopTimeout):
		return errors.New("extension handlers did not stop before the shutdown deadline")
	}
}

func (e *Extension) handleRequest(id string, req *requestMsg) {
	_ = e.conn.requestState(id, "started", "")
	if req == nil {
		_ = e.conn.respond(id, nil, fmt.Errorf("nil request"))
		return
	}

	// A handler runs on its own goroutine (see loop: `go handleRequest`). Without
	// this guard a single panicking handler would crash the whole extension
	// process, silently killing every capability it provides until the user
	// runs /reload. Recover, fail just this request, and keep serving. This
	// mirrors upstream's per-request error isolation (a thrown handler error in
	// the TS runtime rejects one call, it does not tear down the runtime).
	var boundaryData map[string]any
	defer func() {
		if r := recover(); r != nil {
			name := req.Tool
			if name == "" {
				name = req.Event
			}
			fmt.Fprintf(os.Stderr, "extension: recovered from panic in %s %q: %v\n%s\n", req.Method, name, r, debug.Stack())
			var result any
			if boundaryData != nil {
				result = map[string]any{"_pigBoundaryEntries": boundaryData["entries"], "_pigBoundaryResult": nil}
			}
			_ = e.conn.respond(id, result, fmt.Errorf("handler panicked: %v", r))
		}
	}()

	requestRoot := e.runCtx
	if requestRoot == nil {
		requestRoot = context.Background()
	}
	reqCtx, cancel := context.WithCancel(requestRoot)
	if id != "" {
		e.requestsMu.Lock()
		e.requests[id] = cancel
		e.requestsMu.Unlock()
		defer func() {
			e.requestsMu.Lock()
			delete(e.requests, id)
			e.requestsMu.Unlock()
		}()
	}
	defer cancel()

	ctx := Context{ext: e, requestID: id, ctx: reqCtx}

	switch req.Method {
	case methodOAuthLogin, methodOAuthRefresh, methodOAuthGetAPIKey,
		methodOAuthCredentialStatus, methodOAuthStoreCredentials, methodOAuthDeleteCredentials:
		e.dispatchOAuth(id, req)

	case "terminal_input":
		e.dispatchTerminalInput(id, req)

	case "tool_call":
		handler, ok := e.toolFuncs[req.Tool]
		if !ok {
			_ = e.conn.respond(id, nil, fmt.Errorf("unknown tool: %s", req.Tool))
			return
		}
		ctx.toolCallID = req.ToolCallID
		var params map[string]any
		if len(req.Args) > 0 {
			if err := json.Unmarshal(req.Args, &params); err != nil {
				_ = e.conn.respond(id, nil, fmt.Errorf("decode tool arguments: %w", err))
				return
			}
		}
		if prepare := e.toolPrepareFuncs[req.Tool]; prepare != nil {
			var err error
			params, err = prepare(params)
			if err != nil {
				_ = e.conn.respond(id, nil, err)
				return
			}
		}
		result, err := handler(ctx, params)
		_ = e.conn.respond(id, result, err)

	case "command":
		handler, ok := e.commandFuncs[req.Tool]
		if !ok {
			_ = e.conn.respond(id, nil, fmt.Errorf("unknown command: %s", req.Tool))
			return
		}
		var args string
		if len(req.Args) > 0 {
			_ = json.Unmarshal(req.Args, &args)
		}
		err := handler(ctx, args)
		_ = e.conn.respond(id, nil, err)

	case "event":
		e.eventMu.Lock()
		handler, ok := e.eventFuncs[req.HandlerID]
		e.eventMu.Unlock()
		if !ok {
			_ = e.conn.respond(id, nil, fmt.Errorf("unknown event handler %d for %s", req.HandlerID, req.Event))
			return
		}
		var data map[string]any
		if len(req.Args) > 0 {
			_ = json.Unmarshal(req.Args, &data)
		}
		if req.Event == "agent_before_settle" {
			boundaryData = data
		}
		snapshot := snapshotContextMessages(req.Event, data)
		result, err := handler(ctx, data)
		if snapshot != nil && err == nil {
			result = contextEventResult(data, snapshot, result)
		}
		if req.Event == "agent_before_settle" {
			if err != nil {
				result = nil
			}
			result = map[string]any{"_pigBoundaryEntries": data["entries"], "_pigBoundaryResult": result}
		}
		_ = e.conn.respond(id, result, err)

	case "shortcut":
		handler, ok := e.shortcutFuncs[req.Tool]
		if !ok {
			_ = e.conn.respond(id, nil, fmt.Errorf("unknown shortcut: %s", req.Tool))
			return
		}
		err := handler(ctx)
		_ = e.conn.respond(id, nil, err)

	case "render_message":
		handler, ok := e.rendererFuncs[req.Tool]
		if !ok {
			_ = e.conn.respond(id, nil, fmt.Errorf("unknown renderer: %s", req.Tool))
			return
		}
		var payload struct {
			Message map[string]any       `json:"message"`
			Options MessageRenderOptions `json:"options"`
			Width   int                  `json:"width"`
		}
		if len(req.Args) > 0 {
			_ = json.Unmarshal(req.Args, &payload)
		}
		lines, err := handler(ctx, payload.Message, payload.Options, payload.Width)
		_ = e.conn.respond(id, map[string]any{"lines": lines}, err)

	case "render_entry":
		handler, ok := e.entryRendererFuncs[req.Tool]
		if !ok {
			_ = e.conn.respond(id, nil, fmt.Errorf("unknown entry renderer: %s", req.Tool))
			return
		}
		var payload struct {
			Entry   map[string]any     `json:"entry"`
			Options EntryRenderOptions `json:"options"`
			Width   int                `json:"width"`
		}
		if len(req.Args) > 0 {
			_ = json.Unmarshal(req.Args, &payload)
		}
		lines, err := handler(ctx, payload.Entry, payload.Options, payload.Width)
		_ = e.conn.respond(id, map[string]any{"lines": lines}, err)

	default:
		_ = e.conn.respond(id, nil, fmt.Errorf("unknown request method: %s", req.Method))
	}
}

// reportHostCallFailure writes a failed host call the caller cannot return to
// stderr, which the host captures in the extension's log.
func reportHostCallFailure(method string, err error, result *callResultMsg) {
	if err == nil && result != nil && result.Error != nil {
		err = errors.New(result.Error.Message)
	}
	fmt.Fprintf(os.Stderr, "pig: host call %s failed: %v\n", method, err)
}

// markNotifyHandled records that the main loop applied one queued notify frame.
func (e *Extension) markNotifyHandled() {
	e.notifyMu.Lock()
	e.notifyHandled++
	if e.notifyChanged != nil {
		close(e.notifyChanged)
		e.notifyChanged = nil
	}
	e.notifyMu.Unlock()
}

// waitNotifications blocks until the main loop has applied the first target
// queued notify frames, until stop closes, or until the connection closes.
func (e *Extension) waitNotifications(target uint64, stop <-chan struct{}) {
	for {
		e.notifyMu.Lock()
		if e.notifyHandled >= target {
			e.notifyMu.Unlock()
			return
		}
		if e.notifyChanged == nil {
			e.notifyChanged = make(chan struct{})
		}
		changed := e.notifyChanged
		e.notifyMu.Unlock()
		select {
		case <-changed:
		case <-stop:
			return
		case <-e.conn.done:
			return
		}
	}
}

// handleNotify processes broadcast notifications from the host.
// Currently handles "state_update" to keep cached fields (model, thinking,
// etc.) in sync with the host.
func (e *Extension) handleNotify(env envelope) {
	if env.Notify == nil {
		return
	}
	switch env.Notify.Method {
	case "model_stream_event":
		var payload struct {
			StreamID string         `json:"streamId"`
			Event    map[string]any `json:"event"`
		}
		if err := json.Unmarshal(env.Notify.Args, &payload); err != nil {
			return
		}
		e.modelStreamsMu.RLock()
		stream := e.modelStreams[payload.StreamID]
		e.modelStreamsMu.RUnlock()
		if stream != nil {
			stream.push(payload.Event)
		}
	case "state_update":
		var payload struct {
			State struct {
				Model   map[string]any  `json:"model"`
				Session json.RawMessage `json:"session"`
			} `json:"state"`
		}
		if err := json.Unmarshal(env.Notify.Args, &payload); err != nil {
			return
		}
		// Session replication: apply incremental entries before any handler
		// runs, so GetBranch()/GetEntries() are current and local.
		var sessionState struct {
			SessionFile string `json:"sessionFile"`
		}
		if len(payload.State.Session) > 0 {
			_ = json.Unmarshal(payload.State.Session, &sessionState)
		}
		e.session.applySessionUpdate(payload.State.Session)

		e.mu.Lock()
		if sessionState.SessionFile != "" {
			e.sessionFile = sessionState.SessionFile
		}
		defer e.mu.Unlock()
		if m := payload.State.Model; m != nil {
			if id, ok := m["id"].(string); ok && id != "" {
				e.model = id
			} else if name, ok := m["name"].(string); ok && name != "" {
				e.model = name
			}
			// Extract provider ID from nested {"provider": {"id": "..."}}
			// or flat {"provider": "..."}.
			if prov, ok := m["provider"]; ok {
				switch v := prov.(type) {
				case map[string]any:
					if pid, ok := v["id"].(string); ok {
						e.modelProvider = pid
					}
				case string:
					e.modelProvider = v
				}
			}
		}
	case "width_change":
		var payload struct {
			Width int `json:"width"`
		}
		if err := json.Unmarshal(env.Notify.Args, &payload); err != nil {
			return
		}
		if payload.Width > 0 {
			e.mu.Lock()
			e.width = payload.Width
			e.mu.Unlock()
			e.notifyWidthChange(payload.Width)
			e.overlaysMu.RLock()
			overlays := make(map[string]*remoteOverlay, len(e.overlays))
			maps.Copy(overlays, e.overlays)
			e.overlaysMu.RUnlock()
			for _, overlay := range overlays {
				overlay.requestRender()
			}
		}
	case "height_change":
		var payload struct {
			Height int `json:"height"`
		}
		if err := json.Unmarshal(env.Notify.Args, &payload); err != nil {
			return
		}
		if payload.Height > 0 {
			e.mu.Lock()
			e.height = payload.Height
			e.mu.Unlock()
		}
	case "ui.custom.input":
		var payload struct {
			Key  string `json:"key"`
			Data string `json:"data"`
		}
		if err := json.Unmarshal(env.Notify.Args, &payload); err != nil || payload.Key == "" {
			return
		}
		e.overlaysMu.RLock()
		overlay := e.overlays[payload.Key]
		e.overlaysMu.RUnlock()
		if overlay != nil {
			overlay.enqueueInput(e.conn, payload.Key, payload.Data)
		}
	}
}

func (e *Extension) cancelRequest(env envelope) {
	id := env.ID
	if id == "" && env.Cancel != nil {
		id = env.Cancel.RequestID
	}
	if id == "" {
		return
	}
	e.requestsMu.Lock()
	cancel := e.requests[id]
	e.requestsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if e.conn != nil {
		e.conn.cancelParentCalls(id)
	}
}

func (e *Extension) cancelAllRequests() {
	e.requestsMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(e.requests))
	for _, cancel := range e.requests {
		cancels = append(cancels, cancel)
	}
	e.requests = make(map[string]context.CancelFunc)
	e.requestsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// ── Typed parameter helpers ──────────────────────────────────────────────────

// Param extracts a typed parameter from the params map.
// Returns the zero value if the key is missing or the wrong type.
func Param[T any](params map[string]any, key string) T {
	v, ok := params[key]
	if !ok {
		var zero T
		return zero
	}
	typed, ok := v.(T)
	if !ok {
		var zero T
		return zero
	}
	return typed
}

// ToolError is a structured error return from a tool handler.
// When returned from a ToolFunc, the host displays it as a tool error
// with the content visible to the LLM.
type ToolError struct {
	Content string // Error message visible to the LLM
}

func (e *ToolError) Error() string { return e.Content }

// NewToolError creates a structured tool error.
func NewToolError(content string) *ToolError {
	return &ToolError{Content: content}
}

// ToolResult is a structured result from a tool handler with optional
// preview and details. When returned from a ToolFunc:
//   - Content is sent to the LLM as the tool result
//   - Preview (if non-empty) is shown in the TUI when collapsed;
//     Ctrl+O expands to show full Content. Without Preview, the TUI
//     shows a generic tail-of-output preview.
//   - Details is optional structured data for per-tool renderers
//
// For simple string results, returning the string directly from
// ToolFunc is equivalent to ToolResult{Content: s}.
//
// Images follow Content as upstream image blocks, and Terminate mirrors
// upstream's terminate: the agent stops after the current tool batch when every
// result in it sets Terminate.
type ToolResult struct {
	Content   string
	Images    []ImageContent
	Preview   string
	Details   any
	IsError   bool
	Terminate bool
}

// ImageContent is an image block in a tool result, mirroring upstream's
// ImageContent.
type ImageContent struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// MarshalJSON writes the wire tool result. Content stays a string unless the
// result carries images, which need upstream's block array.
func (r ToolResult) MarshalJSON() ([]byte, error) {
	type block struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		Data     string `json:"data,omitempty"`
		MimeType string `json:"mimeType,omitempty"`
	}
	var content any = r.Content
	if len(r.Images) > 0 {
		blocks := make([]block, 0, len(r.Images)+1)
		if r.Content != "" {
			blocks = append(blocks, block{Type: "text", Text: r.Content})
		}
		for _, image := range r.Images {
			blocks = append(blocks, block{Type: "image", Data: image.Data, MimeType: image.MimeType})
		}
		content = blocks
	}
	return json.Marshal(struct {
		Content   any    `json:"content"`
		Preview   string `json:"preview,omitempty"`
		Details   any    `json:"details,omitempty"`
		IsError   bool   `json:"is_error,omitempty"`
		Terminate bool   `json:"terminate,omitempty"`
	}{content, r.Preview, r.Details, r.IsError, r.Terminate})
}

// widthChangeSub pairs a width handler with the token used to remove it.
type widthChangeSub struct {
	id      uint64
	handler WidthChangeHandler
}

// terminalInputSub pairs a raw-input handler with the token used to remove it.
type terminalInputSub struct {
	id      uint64
	handler TerminalInputHandler
}

// dispatchTerminalInput answers the host's consume question for one input
// chunk. The host is blocked on this reply and upstream's handler is
// synchronous, so handlers run inline and a panicking handler degrades to
// not-consumed rather than capturing the user's keystroke.
func (e *Extension) dispatchTerminalInput(id string, req *requestMsg) {
	var args struct {
		Data string `json:"data"`
	}
	if len(req.Args) > 0 {
		_ = json.Unmarshal(req.Args, &args)
	}

	e.terminalInputMu.RLock()
	subs := slices.Clone(e.terminalInputFuncs)
	e.terminalInputMu.RUnlock()

	current := args.Data
	for _, sub := range subs {
		result := runTerminalInputHandler(sub.handler, current)
		if result.Consume {
			_ = e.conn.respond(id, map[string]any{"consume": true}, nil)
			return
		}
		if result.Data != nil {
			current = *result.Data
		}
	}
	verdict := map[string]any{"consume": false}
	if current != args.Data {
		verdict["data"] = current
	}
	_ = e.conn.respond(id, verdict, nil)
}

// runTerminalInputHandler isolates one handler so a panic cannot swallow the
// keystroke or take down the extension; a panic yields no verdict.
func runTerminalInputHandler(handler TerminalInputHandler, data string) (result TerminalInputResult) {
	defer func() {
		if recover() != nil {
			result = TerminalInputResult{}
		}
	}()
	return handler(data)
}

func readSessionEntries(path string) []json.RawMessage {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	var entries []json.RawMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 128*1024*1024)
	for scanner.Scan() {
		raw := bytes.Clone(scanner.Bytes())
		if !json.Valid(raw) {
			return nil
		}
		if len(entries) == 0 {
			var identity struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &identity) != nil {
				return nil
			}
			if identity.Type == "session" {
				continue
			}
		}
		entries = append(entries, raw)
	}
	if scanner.Err() != nil {
		return nil
	}
	return entries
}

// ensureSessionLog enrolls this extension in session-log replication the first
// time it reads the log, and blocks until the backlog is installed so the
// readers above it stay synchronous.
//
// Extensions start unsubscribed because the host would otherwise replicate the
// whole session into every loaded extension, including the majority that only
// ever ask for a session id or act on events. On a large session that is the
// difference between a few megabytes and hundreds per extension.
func (e *Extension) ensureSessionLog() {
	e.session.subMu.Lock()
	defer e.session.subMu.Unlock()
	if e.session.subscribed.Load() {
		return
	}
	// Mark subscribed before the call, not after. The host starts sending the
	// log the moment it registers the subscription, and those pushes must be
	// applied rather than dropped. It also stands regardless of outcome: a host
	// that cannot serve the log will not serve a retry either, and retrying on
	// every read would turn a local read back into an IPC call per event.
	e.session.subscribed.Store(true)
	if e.conn == nil {
		return
	}
	type sessionPage struct {
		Entries    []json.RawMessage `json:"entries"`
		EntryCount int               `json:"entryCount"`
		HasMore    bool              `json:"hasMore"`
		LeafID     string            `json:"leafId"`
	}
	fetch := func(cursor int, complete bool) (sessionPage, bool) {
		result, err := e.conn.call("watchSessionLog", map[string]any{"cursor": cursor, "complete": complete})
		if err != nil || result == nil {
			return sessionPage{}, false
		}
		var page sessionPage
		if err := json.Unmarshal(result.Result, &page); err != nil {
			return sessionPage{}, false
		}
		return page, true
	}

	e.mu.RLock()
	entries := readSessionEntries(e.sessionFile)
	e.mu.RUnlock()
	cursor := len(entries)
	leafID := ""
	for {
		requestedCursor := cursor
		page, ok := fetch(cursor, false)
		if !ok {
			return
		}
		if page.EntryCount-len(page.Entries) != requestedCursor {
			entries = nil
		}
		entries = append(entries, page.Entries...)
		cursor = page.EntryCount
		leafID = page.LeafID
		if !page.HasMore {
			break
		}
	}
	e.session.seed(entries, cursor, leafID)

	for {
		requestedCursor := cursor
		page, ok := fetch(cursor, true)
		if !ok {
			return
		}
		if page.EntryCount-len(page.Entries) != requestedCursor {
			entries = nil
		}
		entries = append(entries, page.Entries...)
		cursor = page.EntryCount
		leafID = page.LeafID
		e.session.seed(entries, cursor, leafID)
		if !page.HasMore && len(page.Entries) == 0 {
			return
		}
	}
}

// notifyWidthChange runs width handlers after e.width is updated, so a handler
// that calls Context.Width observes the new value. Handlers run on the message
// loop goroutine and must not block it; a handler that re-pushes a footer only
// issues a host call, which is what this exists for.
func (e *Extension) notifyWidthChange(width int) {
	e.widthChangeMu.RLock()
	subs := slices.Clone(e.widthChangeFuncs)
	e.widthChangeMu.RUnlock()
	if len(subs) == 0 {
		return
	}
	ctx := Context{ext: e}
	for _, sub := range subs {
		sub.handler(ctx, width)
	}
}
