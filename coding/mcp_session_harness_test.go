//go:build !pig_strip_mcp

package coding

import (
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"

	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// The harness of packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 0.99.2): a Session with the built-in
// codemode, tool-search and MCP extensions, loaded the way the CLI loads them (builtin.Resolve), and a fake MCP server
// over an in-memory transport. The MCP extension is reached only through the Session: its tools, prompt section and
// waits are observed in the transcript, the tool registry and the tool results.

const mcpTinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

var mcpServerTools = []map[string]any{
	{
		"name": "search", "description": "Search the docs.",
		"inputSchema":  map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}},
		"outputSchema": map[string]any{"type": "object", "properties": map[string]any{"hits": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"hits"}},
	},
	{
		"name": "fail", "description": "Always fails.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		"annotations": map[string]any{"title": "Fail", "destructiveHint": true, "readOnlyHint": false, "idempotentHint": "yes"},
	},
	{"name": "shot", "description": "Returns an image.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
}

type mcpCallLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *mcpCallLog) add(call string) {
	l.mu.Lock()
	l.calls = append(l.calls, call)
	l.mu.Unlock()
}

func (l *mcpCallLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

// mcpFakeServerOptions are the options of createFakeServer: the server instructions and the delay of the answer to
// `initialize`; a negative delay never answers.
type mcpFakeServerOptions struct {
	instructions    string
	initializeDelay time.Duration
}

// mcpFakeServer is createFakeServer of agent-session-mcp.test.ts: a minimal MCP server over an in-memory transport that
// records the tool calls it receives.
func mcpFakeServer(calls *mcpCallLog, listTools func() []map[string]any, resources bool, opts mcpFakeServerOptions) (client, server *mcptest.InMemoryTransport) {
	client, server = mcptest.NewInMemoryTransportPair()
	respond := func(request mcp.JSONRPCMessage) any {
		var params map[string]any
		_ = json.Unmarshal(request.Params, &params)
		switch request.Method {
		case "initialize":
			caps := map[string]any{"tools": map[string]any{}}
			if resources {
				caps["resources"] = map[string]any{}
			}
			result := map[string]any{"protocolVersion": mcp.LatestProtocolVersion, "capabilities": caps, "serverInfo": map[string]any{"name": "docs", "version": "1.0.0"}}
			if opts.instructions != "" {
				result["instructions"] = opts.instructions
			}
			return result
		case "tools/list":
			return map[string]any{"tools": listTools()}
		case "resources/list":
			return map[string]any{"resources": []any{
				map[string]any{"uri": "docs://intro", "name": "intro", "mimeType": "text/markdown", "_meta": map[string]any{"x": 1}},
				// MCP App user interfaces are left out.
				map[string]any{"uri": "ui://docs/viewer", "name": "viewer", "mimeType": "text/html;profile=mcp-app"},
			}}
		case "resources/templates/list":
			return map[string]any{"resourceTemplates": []any{
				map[string]any{"uriTemplate": "docs://pages/{slug}", "name": "page", "icons": []any{map[string]any{"src": "data:image/png;base64,AAAA"}}},
			}}
		case "resources/read":
			uri, _ := params["uri"].(string)
			calls.add("read:" + uri)
			return map[string]any{"contents": []any{map[string]any{"uri": uri, "mimeType": "text/markdown", "text": "# " + uri}}}
		case "tools/call":
			name, _ := params["name"].(string)
			arguments, _ := params["arguments"].(map[string]any)
			encoded, _ := json.Marshal(arguments)
			if arguments == nil {
				encoded = []byte("{}")
			}
			calls.add(name + ":" + string(encoded))
			switch name {
			case "search":
				query, _ := arguments["query"].(string)
				hits := []string{query + " guide", query + " faq"}
				return map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Join(hits, "\n")}}, "structuredContent": map[string]any{"hits": hits}}
			case "shot":
				return map[string]any{"content": []any{map[string]any{"type": "image", "data": mcpTinyPNGBase64, "mimeType": "image/png"}}}
			}
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": "server exploded"}}, "isError": true}
		}
		return map[string]any{}
	}
	server.OnMessage(func(message mcp.JSONRPCMessage) {
		if !message.IsRequest() {
			return
		}
		send := func(result any) {
			raw, _ := json.Marshal(result)
			_ = server.Send(mcp.NewResult(*message.ID, raw))
		}
		switch {
		case message.Method != "initialize" || opts.initializeDelay == 0:
			// Upstream answers in a microtask, in arrival order: the request is handled here, in the delivery order, and only
			// the reply leaves on its own goroutine (a listener must not Send to the transport that delivers to it).
			result := respond(message)
			go send(result)
		case opts.initializeDelay > 0:
			time.AfterFunc(opts.initializeDelay, func() { send(respond(message)) })
		}
	})
	return client, server
}

// mcpNotifications collects the notifications the extensions show.
type mcpNotifications struct {
	mu   sync.Mutex
	list []string
}

func (n *mcpNotifications) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.list)
}

func (n *mcpNotifications) last() string {
	all := n.all()
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// mcpTestUI is upstream's createTestUiContext: notifications are recorded and the input dialog answers as the test says.
type mcpTestUI struct {
	extension.UIContext
	notes *mcpNotifications
	input func(ctx context.Context, title, placeholder string) string
}

func (u *mcpTestUI) Notify(message, _ string) {
	u.notes.mu.Lock()
	u.notes.list = append(u.notes.list, message)
	u.notes.mu.Unlock()
}

func (u *mcpTestUI) Input(ctx context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	if u.input == nil {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return u.input(ctx, title, placeholder), nil
}

type mcpSessionOptions struct {
	// builtInTools are the tools active at the start (upstream initialActiveToolNames). The MCP extension activates codemode or tool_search.
	builtInTools       []string
	extensions         []extension.Extension
	toolExposure       *extension.OrderedExposures
	resources          bool
	withoutToolSearch  bool
	description        string
	instructions       string
	autoEnableCodemode *bool
	name               string
	initializeDelay    time.Duration
	startupWait        *int
	// config replaces the single configured server, for the tests of servers registered by extensions.
	config *[]mcpext.McpServerEntry
	// transport replaces the fake server's transport factory.
	createTransport mcpext.TransportFactory
	uiInput         func(ctx context.Context, title, placeholder string) string
	noBind          bool
	// onlyMCP leaves the codemode and tool-search extensions out.
	onlyMCP bool
	// realTransport leaves the transport to the extension: stdio and streamable HTTP from the server config.
	realTransport bool
	credentials   *mcpext.McpOAuthCredentialStore
	openURL       func(url string)
	// allowedTools and excludedTools are `--tools` and `--exclude-tools`. A non-nil allowedTools also gives the Session the built-in tools, as the upstream suite harness always has them.
	allowedTools, excludedTools map[string]struct{}
	// sessionManager is the session to continue.
	sessionManager *codingagent.Session
	// skipToolWait leaves out the wait for `mcp__docs__search` to register (upstream waitForTools: false).
	skipToolWait bool
}

type mcpSession struct {
	*recoveryHarness
	calls *mcpCallLog
	notes *mcpNotifications
	ui    *mcpTestUI

	mu      sync.Mutex
	servers []*mcptest.InMemoryTransport
}

// loadMcpBuiltin resolves a built-in extension the way the CLI's resource loader runs it (resource-loader.ts
// loadExtensionPaths, builtinExtensions), naming it by its `builtin:<name>` path.
func loadMcpBuiltin(t *testing.T, runtime *extension.ExtensionRuntime, name string, options builtin.Options) extension.Extension {
	t.Helper()
	entry, err := builtin.Resolve("builtin:"+name, options)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := factoryload.LoadExtensionFromFactory(entry.Factory, ".", extension.CreateEventBus(), runtime, entry.Path(), factoryload.WithSourceInfo(codingagent.PiSourceInfo{Path: entry.Path(), Source: codingagent.SyntheticPathSource(entry.Path()), Scope: "temporary", Origin: "top-level"}))
	if err != nil {
		t.Fatal(err)
	}
	info := codingagent.PiSourceInfo{Path: entry.Path(), Source: codingagent.SyntheticPathSource(entry.Path()), Scope: "temporary", Origin: "top-level"}
	ext.Name, ext.Path, ext.ResolvedPath, ext.SourceInfo, ext.Replaceable, ext.Hidden = entry.Name, entry.Path(), entry.Path(), info, entry.Replaceable, true
	return ext
}

func newMCPSession(t *testing.T, exposure extension.McpExposure, listTools func() []map[string]any, opts mcpSessionOptions, responses ...scriptedResponse) *mcpSession {
	t.Helper()
	if listTools == nil {
		listTools = func() []map[string]any { return mcpServerTools }
	}
	if opts.name == "" {
		opts.name = "docs"
	}
	s := &mcpSession{calls: &mcpCallLog{}, notes: &mcpNotifications{}}
	entries := []mcpext.McpServerEntry{{
		Name:   opts.name,
		Config: extension.McpServerConfig{URL: "http://unused.invalid", Exposure: exposure, ToolExposure: opts.toolExposure, Description: opts.description},
		Source: "test",
	}}
	if opts.config != nil {
		entries = *opts.config
	}
	createTransport := opts.createTransport
	if createTransport == nil && !opts.realTransport {
		createTransport = func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			client, server := mcpFakeServer(s.calls, listTools, opts.resources, mcpFakeServerOptions{instructions: opts.instructions, initializeDelay: opts.initializeDelay})
			s.mu.Lock()
			s.servers = append(s.servers, server)
			s.mu.Unlock()
			_ = server.Start()
			return client, nil
		}
	}
	options := builtin.Options{Mcp: mcpext.Options{
		LoadConfig: func(context.Context) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: entries, AutoEnableCodemode: opts.autoEnableCodemode}
		},
		CreateTransport: createTransport,
		StartupWaitMs:   opts.startupWait,
		Credentials:     opts.credentials,
		OpenURL:         opts.openURL,
		LogPath:         t.TempDir() + "/mcp.log",
	}}
	if options.Mcp.Credentials == nil {
		options.Mcp.Credentials = mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "")
	}
	extensions := slices.Clone(opts.extensions)
	runtime := extension.CreateExtensionRuntime()
	if !opts.onlyMCP {
		extensions = append(extensions, loadMcpBuiltin(t, runtime, "codemode", options))
		if !opts.withoutToolSearch {
			extensions = append(extensions, loadMcpBuiltin(t, runtime, "tool-search", options))
		}
	}
	extensions = append(extensions, loadMcpBuiltin(t, runtime, "mcp", options))
	harness := harnessOptions{tools: []agent.AgentTool{}, extensions: extensions, runtime: runtime, allowedTools: opts.allowedTools, excludedTools: opts.excludedTools, sessionManager: opts.sessionManager}
	if opts.allowedTools != nil {
		harness.tools = nil
	}
	s.recoveryHarness = newBoundaryHarness(t, harness, responses...)
	s.ui = &mcpTestUI{UIContext: extension.NoopUIContext, notes: s.notes, input: opts.uiInput}
	if opts.builtInTools != nil {
		s.session.SetActiveToolsByName(opts.builtInTools)
	}
	if opts.noBind {
		return s
	}
	if err := s.session.BindExtensions(t.Context(), ExtensionBindings{UIContext: s.ui}); err != nil {
		t.Fatal(err)
	}
	return s
}

// setupMCP is setup() of agent-session-mcp.test.ts: it also waits for the tools of the servers that are not direct, since
// the first prompt waits only for the servers with direct tools.
func setupMCP(t *testing.T, exposure extension.McpExposure, listTools func() []map[string]any, opts mcpSessionOptions) *mcpSession {
	t.Helper()
	s := newMCPSession(t, exposure, listTools, opts)
	if !opts.skipToolWait {
		mcpWaitFor(t, "mcp__docs__search to be registered", func() bool { return s.hasTool("mcp__docs__search") })
		if opts.resources && opts.allowedTools == nil && exposure != extension.McpExposureHidden { // a --tools filter decides which resource tools exist at all
			// Pi registers a server's tools and the resource tools in one synchronous turn, so waiting for the search tool is enough there. Here each registration is its own call, and a prompt that starts between them runs a script that finds only some of the tools.
			for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
				mcpWaitFor(t, name+" to be registered", func() bool { return s.hasTool(name) })
			}
		}
	}
	return s
}

// mcpWaitFor is vi.waitFor: it polls until the condition holds.
func mcpWaitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *mcpSession) hasTool(name string) bool {
	return slices.ContainsFunc(s.session.GetAllTools(), func(tool extension.ToolInfo) bool { return tool.Name == name })
}

func (s *mcpSession) respond(responses ...scriptedResponse) {
	s.provider.mu.Lock()
	s.provider.responses = append(s.provider.responses, responses...)
	s.provider.mu.Unlock()
}

func (s *mcpSession) prompt(t *testing.T, text string) {
	t.Helper()
	boundaryPrompt(t, s.recoveryHarness, text)
}

func (s *mcpSession) lastServer() *mcptest.InMemoryTransport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servers[len(s.servers)-1]
}

// declaredToolNames are the tools the transcript declared to the model, in order.
func (s *mcpSession) declaredToolNames() []string {
	names := []string{}
	for _, message := range s.session.Messages() {
		if message.System != nil {
			for _, tool := range message.System.ToolsAdded {
				names = append(names, tool.Name)
			}
		}
	}
	return names
}

// serversSection is the `mcp_servers` prompt section as the model currently has it.
func (s *mcpSession) serversSection() (value string, present bool) {
	for _, message := range s.session.Messages() {
		if message.System == nil {
			continue
		}
		for _, section := range message.System.Sections {
			if section.Name == mcpext.McpServersSection {
				present = section.Value != nil
				value = ""
				if present {
					value = *section.Value
				}
			}
		}
	}
	return value, present
}

func (s *mcpSession) systemMessages() []*ai.SystemMessage {
	var out []*ai.SystemMessage
	for _, message := range s.session.Messages() {
		if message.System != nil {
			out = append(out, message.System)
		}
	}
	return out
}

func (s *mcpSession) nestedToolNames() []string { return s.session.CallableToolNames() }

// description is `harness.session.agent.state.tools.find(...).description`.
func (s *mcpSession) description(name string) (string, bool) {
	for _, tool := range s.session.Tools() {
		if tool.Name() == name {
			return tool.Schema().Description, true
		}
	}
	return "", false
}

// toolResult is getToolResult of the suite harness: the last tool result of the tool.
func (s *mcpSession) toolResult(t *testing.T, name string) agent.ToolResultMessage {
	t.Helper()
	for _, message := range slices.Backward(s.session.Messages()) {
		if result := message.ToolResult; result != nil && result.ToolName == name {
			return *result
		}
	}
	t.Fatalf("no %s tool result", name)
	return agent.ToolResultMessage{}
}

func mcpText(result agent.ToolResultMessage) string {
	var text []string
	for _, block := range result.Content {
		if block, ok := block.(ai.TextContent); ok {
			text = append(text, block.Text)
		}
	}
	return strings.Join(text, "\n")
}

type mcpToolCall struct {
	name string
	args ai.JsonObject
}

// mcpToolCalls answers with one assistant message that calls each tool, stopping for tool use.
func mcpToolCalls(calls ...mcpToolCall) scriptedResponse {
	return func(messages []ai.Message) *ai.AssistantMessage {
		reply := boundaryReply("", ai.StopReasonToolUse, 0)(messages)
		reply.Content = nil
		for i, call := range calls {
			reply.Content = append(reply.Content, ai.ToolCall{ID: "call-" + call.name + "-" + string(rune('a'+i)), Name: call.name, Arguments: call.args})
		}
		return reply
	}
}

func mcpCodemode(code string) scriptedResponse {
	return mcpToolCalls(mcpToolCall{"codemode", ai.JsonObject{"code": code}})
}

func mcpDone() scriptedResponse { return boundaryReply("done", ai.StopReasonStop, 0) }
