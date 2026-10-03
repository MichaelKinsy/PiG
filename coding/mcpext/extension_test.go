package mcpext_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// Ports the MCP-side cases of packages/coding-agent/test/suite/agent-session-mcp.test.ts.
//
// Upstream drives a whole AgentSession with a faux provider. The cases that
// call codemode scripts or tool_search (`codemode`, `tool_search`), and the ones
// that need the runner (registerMcpServer ownership, the unhandled-server
// report), belong to the families that own those. The cases below exercise the
// extension through its event methods against a host that behaves like the
// runner, and call the tools it registered directly.

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

var serverTools = []map[string]any{
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

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(call string) {
	l.mu.Lock()
	l.calls = append(l.calls, call)
	l.mu.Unlock()
}

func (l *callLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

// fakeServerOptions are the options of createFakeServer of agent-session-mcp.test.ts (0.99.2): the server instructions
// and a delay of the answer to `initialize`; a negative delay never answers.
type fakeServerOptions struct {
	instructions    string
	initializeDelay time.Duration
}

// createFakeServer is createFakeServer of agent-session-mcp.test.ts: a minimal
// MCP server over an in-memory transport that records the tool calls it receives.
func createFakeServer(calls *callLog, listTools func() []map[string]any, resources bool, options ...fakeServerOptions) (client, server *mcptest.InMemoryTransport) {
	var opts fakeServerOptions
	if len(options) > 0 {
		opts = options[0]
	}
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
				return map[string]any{"content": []any{map[string]any{"type": "image", "data": tinyPNGBase64, "mimeType": "image/png"}}}
			}
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": "server exploded"}}, "isError": true}
		}
		return map[string]any{}
	}
	server.OnMessage(func(message mcp.JSONRPCMessage) {
		if !message.IsRequest() {
			return
		}
		send := func() {
			raw, _ := json.Marshal(respond(message))
			_ = server.Send(mcp.NewResult(*message.ID, raw))
		}
		switch {
		case message.Method != "initialize" || opts.initializeDelay == 0:
			go send()
		case opts.initializeDelay > 0:
			time.AfterFunc(opts.initializeDelay, send)
		}
	})
	return client, server
}

type setupOptions struct {
	autoEnableCodemode *bool
	builtInTools       []string
	toolExposure       *extension.OrderedExposures
	resources          bool
	withoutToolSearch  bool
	description        string
	instructions       string
	other              func(host *fakeHost)
}

type sessionHarness struct {
	host     *fakeHost
	ext      *mcpext.Extension
	calls    *callLog
	servers  *serverSet
	notes    *notifications
	ctx      mcpext.EventContext
	options  *extension.BuildSystemPromptOptions
	servers2 struct {
		mu   sync.Mutex
		list []*mcptest.InMemoryTransport
	}
}

func (h *sessionHarness) serverTransport() *mcptest.InMemoryTransport {
	h.servers2.mu.Lock()
	defer h.servers2.mu.Unlock()
	return h.servers2.list[len(h.servers2.list)-1]
}

func setupSession(t *testing.T, exposure extension.McpExposure, listTools func() []map[string]any, options setupOptions) *sessionHarness {
	t.Helper()
	if listTools == nil {
		listTools = func() []map[string]any { return serverTools }
	}
	h := &sessionHarness{host: newFakeHost(options.builtInTools...), calls: &callLog{}, servers: &serverSet{}, notes: &notifications{}}
	if options.other != nil {
		options.other(h.host)
	}
	h.host.registerBuiltin(mcpext.CodemodeToolName, "builtin:codemode")
	if !options.withoutToolSearch {
		h.host.registerBuiltin(mcpext.ToolSearchToolName, "builtin:tool-search")
	}
	config := extension.McpServerConfig{URL: "http://unused.invalid", Exposure: exposure, ToolExposure: options.toolExposure, Description: options.description}
	entry := mcpext.McpServerEntry{Name: "docs", Config: config, Source: "test"}
	h.ext = mcpext.New(h.host, mcpext.Options{
		LoadConfig: func(mcpext.EventContext) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: []mcpext.McpServerEntry{entry}, AutoEnableCodemode: options.autoEnableCodemode}
		},
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			client, server := createFakeServer(h.calls, listTools, options.resources, fakeServerOptions{instructions: options.instructions})
			h.servers2.mu.Lock()
			h.servers2.list = append(h.servers2.list, server)
			h.servers2.mu.Unlock()
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	})
	h.ctx = eventContext(t.TempDir(), h.notes)
	t.Cleanup(h.ext.SessionShutdown)
	return h
}

// start binds the extension, waits for the servers to connect (setup() of agent-session-mcp.test.ts waits for their
// tools: the first prompt waits only for servers with direct tools), and runs the first prompt's `before_agent_start`.
func (h *sessionHarness) start() {
	h.ext.SessionStart(h.ctx)
	h.ext.Pending()
	h.prompt()
}

// prompt runs `before_agent_start` with fresh prompt options, as every prompt does.
func (h *sessionHarness) prompt() {
	h.options = &extension.BuildSystemPromptOptions{}
	h.ext.BeforeAgentStart(h.ctx, h.options)
}

// serversSection is the `mcp_servers` prompt section the last prompt left in its options.
func (h *sessionHarness) serversSection() (string, bool) { return sectionOf(h.options) }

func sectionOf(options *extension.BuildSystemPromptOptions) (string, bool) {
	if options == nil || options.Sections == nil {
		return "", false
	}
	for _, section := range *options.Sections {
		if section.Name == mcpext.McpServersSection && section.Value != nil {
			return *section.Value, true
		}
	}
	return "", false
}

func (h *sessionHarness) execute(t *testing.T, tool string, params string) (agent.AgentToolResult, error) {
	t.Helper()
	definition, ok := h.host.tool(tool)
	if !ok {
		t.Fatalf("tool %s is not registered", tool)
	}
	raw, err := definition.Execute(t.Context(), "call-1", json.RawMessage(params), nil)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	return raw.(agent.AgentToolResult), nil
}

func TestAgentSessionMCPDeclaresDirectlyExposedMCPToolsToTheModel(t *testing.T) {
	h := setupSession(t, extension.McpExposureDirect, nil, setupOptions{})
	h.start()

	if got := h.host.GetActiveTools(); !slices.Equal(got, []string{"mcp__docs__search", "mcp__docs__fail", "mcp__docs__shot"}) {
		t.Fatalf("active tools = %v", got)
	}
	// Boolean annotation hints are passed on for permission extensions.
	annotations := map[string]*extension.ToolAnnotations{}
	for _, tool := range h.host.GetAllTools() {
		annotations[tool.Name] = tool.Annotations
	}
	jsonEqual(t, annotations["mcp__docs__fail"], `{"destructiveHint":true,"readOnlyHint":false}`)
	if annotations["mcp__docs__search"] != nil {
		t.Fatalf("search annotations = %v", annotations["mcp__docs__search"])
	}
	result, err := h.execute(t, "mcp__docs__search", `{"query":"direct"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Text(); got != "direct guide\ndirect faq" {
		t.Fatalf("text = %q", got)
	}
}

func TestAgentSessionMCPWithdrawsAndRestoresMCPToolsTheServerChanges(t *testing.T) {
	for _, exposure := range []extension.McpExposure{extension.McpExposureDirect, extension.McpExposureCodemode} {
		t.Run(string(exposure), func(t *testing.T) {
			var mu sync.Mutex
			tools := serverTools
			h := setupSession(t, exposure, func() []map[string]any {
				mu.Lock()
				defer mu.Unlock()
				return tools
			}, setupOptions{})
			h.start()
			// Direct tools are declared to the model, codemode tools are only callable from codemode.
			reachable := func() []string {
				if exposure == extension.McpExposureDirect {
					return h.host.GetActiveTools()
				}
				return h.host.callable()
			}
			listChanged := func(want func([]string) bool) {
				if err := h.serverTransport().Send(mcp.NewNotification("notifications/tools/list_changed", nil)); err != nil {
					t.Fatal(err)
				}
				waitFor(t, "the tool list to change", func() bool { return want(reachable()) })
			}
			if !slices.Contains(reachable(), "mcp__docs__fail") {
				t.Fatalf("reachable = %v", reachable())
			}

			mu.Lock()
			tools = serverTools[:0:0]
			for _, tool := range serverTools {
				if tool["name"] != "fail" {
					tools = append(tools, tool)
				}
			}
			mu.Unlock()
			listChanged(func(names []string) bool { return !slices.Contains(names, "mcp__docs__fail") })
			if !slices.Contains(reachable(), "mcp__docs__search") {
				t.Fatalf("reachable = %v", reachable())
			}

			mu.Lock()
			tools = serverTools
			mu.Unlock()
			listChanged(func(names []string) bool { return slices.Contains(names, "mcp__docs__fail") })
		})
	}
}

func textOfResult(t *testing.T, result agent.AgentToolResult) string {
	t.Helper()
	return result.Text()
}

func TestAgentSessionMCPListsAndReadsResourcesWithCodexResourceTools(t *testing.T) {
	h := setupSession(t, extension.McpExposureDirect, nil, setupOptions{resources: true})
	h.start()

	// The resource tools take the exposure of the servers they reach.
	active := h.host.GetActiveTools()
	for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
		if !slices.Contains(active, name) {
			t.Fatalf("active tools = %v, missing %s", active, name)
		}
	}
	listed, err := h.execute(t, "list_mcp_resources", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, json.RawMessage(textOfResult(t, listed)), `{"resources":[{"server":"docs","uri":"docs://intro","name":"intro","mimeType":"text/markdown"}]}`)
	templates, err := h.execute(t, "list_mcp_resource_templates", `{"server":"docs"}`)
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, json.RawMessage(textOfResult(t, templates)), `{"server":"docs","resourceTemplates":[{"server":"docs","uriTemplate":"docs://pages/{slug}","name":"page"}]}`)
	read, err := h.execute(t, "read_mcp_resource", `{"server":"docs","uri":"docs://pages/setup"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := textOfResult(t, read); got != "# docs://pages/setup" {
		t.Fatalf("read text = %q", got)
	}
	_, err = h.execute(t, "read_mcp_resource", `{"server":"nope","uri":"docs://x"}`)
	if err == nil || err.Error() != `MCP server "nope" has no resources. Servers with resources: docs` {
		t.Fatalf("read err = %v", err)
	}
	if calls := h.calls.all(); !slices.Equal(calls, []string{"read:docs://pages/setup"}) {
		t.Fatalf("calls = %v", calls)
	}
	for _, tool := range h.host.GetAllTools() {
		if tool.Name == "read_mcp_resource" {
			jsonEqual(t, tool.Annotations, `{"readOnlyHint":true}`)
		}
	}
}

func TestAgentSessionMCPMakesTheResourceToolsCallableFromCodemodeForCodemodeServers(t *testing.T) {
	h := setupSession(t, extension.McpExposureCodemode, nil, setupOptions{resources: true})
	h.start()

	if got := h.host.GetActiveTools(); !slices.Equal(got, []string{"codemode"}) {
		t.Fatalf("active tools = %v", got)
	}
	callable := h.host.callable()
	for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
		if !slices.Contains(callable, name) {
			t.Errorf("callable = %v, missing %s", callable, name)
		}
	}
	// A script's calls run the same tools.
	listed, err := h.execute(t, "list_mcp_resources", `{"server":"docs"}`)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Resources []struct{ URI string } `json:"resources"`
	}
	if err := json.Unmarshal(listed.StructuredContent, &payload); err != nil || len(payload.Resources) != 1 || payload.Resources[0].URI != "docs://intro" {
		t.Fatalf("structured = %s, %v", listed.StructuredContent, err)
	}
	read, err := h.execute(t, "read_mcp_resource", `{"server":"docs","uri":"docs://intro"}`)
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, json.RawMessage(read.StructuredContent), `{"server":"docs","uri":"docs://intro","contents":[{"uri":"docs://intro","mimeType":"text/markdown","text":"# docs://intro"}]}`)
	if calls := h.calls.all(); !slices.Equal(calls, []string{"read:docs://intro"}) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestAgentSessionMCPAppliesPerToolExposureOverrides(t *testing.T) {
	h := setupSession(t, extension.McpExposureHidden, nil, setupOptions{toolExposure: extension.NewOrderedExposures("search", "direct", "s*", "codemode")})
	h.start()

	// `search` is declared, `shot` is only callable from codemode, `fail` keeps the server's `hidden`. Codemode is
	// activated from the config before the server connects, so it comes first (agent-session-mcp.test.ts 0.99.2).
	if got := h.host.GetActiveTools(); !slices.Equal(got, []string{"codemode", "mcp__docs__search"}) {
		t.Fatalf("active tools = %v", got)
	}
	if got := h.host.callable(); !slices.Equal(got, []string{"mcp__docs__search", "mcp__docs__shot"}) {
		t.Fatalf("callable = %v", got)
	}
}

// "exposes codemode-only MCP tools through codemode" (0.99.2): the codemode description lists neither the server nor
// its tools. Its listing skips tools with the `deferred` exposure, so codemode-exposed MCP tools register as deferred
// (tools.ts toToolExposure); they stay callable from scripts, which find them with searchTools().
func TestAgentSessionMCPRegistersCodemodeServersToolsAsDeferredSoTheCodemodeDescriptionDoesNotListThem(t *testing.T) {
	for exposure, want := range map[extension.McpExposure]extension.ToolExposure{
		extension.McpExposureCodemode: extension.ToolExposureDeferred,
		extension.McpExposureDeferred: extension.ToolExposureDeferred,
		extension.McpExposureDirect:   extension.ToolExposureDirect,
		extension.McpExposureHidden:   extension.ToolExposureHidden,
	} {
		t.Run(string(exposure), func(t *testing.T) {
			h := setupSession(t, exposure, nil, setupOptions{})
			h.start()
			for _, tool := range h.host.GetAllTools() {
				if strings.HasPrefix(tool.Name, "mcp__docs__") && tool.Exposure != want {
					t.Errorf("%s exposure = %s, want %s", tool.Name, tool.Exposure, want)
				}
			}
		})
	}
	// The toToolExposure mapping itself.
	if got := mcpext.ToToolExposure(extension.McpExposureCodemode); got != extension.ToolExposureDeferred {
		t.Errorf("ToToolExposure(codemode) = %s", got)
	}
}

// "routes tools whose names differ only in - and _" (#10239): both tools get a hash suffix whatever the order of the
// server's list, and a call reaches the tool the model found.
func TestAgentSessionMCPRoutesToolsWhoseNamesDifferOnlyInDashAndUnderscore(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%v", reverse), func(t *testing.T) {
			tools := []map[string]any{
				{"name": "read-file", "description": "dashed", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
				{"name": "read_file", "description": "underscored", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
			}
			if reverse {
				slices.Reverse(tools)
			}
			h := setupSession(t, extension.McpExposureCodemode, func() []map[string]any { return append(slices.Clone(tools), serverTools...) }, setupOptions{})
			h.start()

			byDescription := map[string]string{}
			for _, tool := range h.host.GetAllTools() {
				byDescription[tool.Description] = tool.Name
			}
			dashed, underscored := byDescription["dashed"], byDescription["underscored"]
			suffixed := regexp.MustCompile(`^mcp__docs__read_file_[0-9a-f]{8}$`)
			if dashed == underscored || !suffixed.MatchString(dashed) || !suffixed.MatchString(underscored) {
				t.Fatalf("names = %q and %q, want two distinct hash-suffixed names", dashed, underscored)
			}
			for _, name := range []string{dashed, underscored} {
				if _, err := h.execute(t, name, `{}`); err != nil {
					t.Fatal(err)
				}
			}
			// The call that found `dashed` reached read-file, the other read_file.
			if got := h.calls.all(); !slices.Equal(got, []string{"read-file:{}", "read_file:{}"}) {
				t.Fatalf("calls = %v", got)
			}
		})
	}
}

// "describes the server with its configured description and returns its instructions to scripts": the namespace of the
// server's tools carries the configured description and, separately, the server instructions that
// describeNamespace() returns; the system prompt lists the server with the configured description.
func TestAgentSessionMCPDescribesTheServerWithItsConfiguredDescriptionAndKeepsItsInstructionsSeparate(t *testing.T) {
	h := setupSession(t, extension.McpExposureCodemode, nil, setupOptions{
		description: "Search the product docs", instructions: "Always search before reading.", builtInTools: []string{"tool_search"},
	})
	h.start()

	for _, tool := range h.host.GetAllTools() {
		if !strings.HasPrefix(tool.Name, "mcp__docs__") {
			continue
		}
		jsonEqual(t, tool.Namespace, `{"name":"mcp__docs","description":"Search the product docs","instructions":"Always search before reading."}`)
	}
	section, _ := h.serversSection()
	if !strings.Contains(section, "- mcp__docs (codemode): Search the product docs") || strings.Contains(section, "Always search") {
		t.Fatalf("section = %q", section)
	}
}

// Without a configured description the namespace has no description: the model-facing listings never carry the
// instructions, and scripts read them with describeNamespace().
func TestAgentSessionMCPNamespaceHasNoDescriptionWithoutAConfiguredOne(t *testing.T) {
	h := setupSession(t, extension.McpExposureCodemode, nil, setupOptions{instructions: "Docs search.\nLong guidance."})
	h.start()

	for _, tool := range h.host.GetAllTools() {
		if strings.HasPrefix(tool.Name, "mcp__docs__") {
			jsonEqual(t, tool.Namespace, `{"name":"mcp__docs","instructions":"Docs search.\nLong guidance."}`)
		}
	}
}

// "lists servers by the first line of their instructions without a configured description"
func TestAgentSessionMCPListsServersByTheFirstLineOfTheirInstructionsWithoutAConfiguredDescription(t *testing.T) {
	h := setupSession(t, extension.McpExposureDeferred, nil, setupOptions{instructions: "Docs search.\nLong guidance."})
	h.start()

	section, _ := h.serversSection()
	if !strings.Contains(section, "- mcp__docs (tool_search): Docs search.\n") && !strings.HasSuffix(section, "- mcp__docs (tool_search): Docs search.") || strings.Contains(section, "Long guidance") {
		t.Fatalf("section = %q", section)
	}
}

func TestAgentSessionMCPDoesNotActivateCodemodeWhenAutoEnableCodemodeIsFalse(t *testing.T) {
	h := setupSession(t, extension.McpExposureCodemode, nil, setupOptions{autoEnableCodemode: new(false)})
	h.start()

	if got := h.host.GetActiveTools(); len(got) != 0 {
		t.Fatalf("active tools = %v", got)
	}
	if !slices.Contains(h.host.callable(), "mcp__docs__search") {
		t.Fatalf("callable = %v", h.host.callable())
	}
	want := []string{"MCP tools are only reachable from the codemode or tool_search tool, but neither is active (autoEnableCodemode is false); they cannot be called."}
	if got := h.notes.all(); !slices.Equal(got, want) {
		t.Fatalf("notifications = %q", got)
	}
}

func TestAgentSessionMCPTreatsCodemodeMCPToolsAsReachableThroughAnActiveToolSearch(t *testing.T) {
	h := setupSession(t, extension.McpExposureCodemode, nil, setupOptions{autoEnableCodemode: new(false), builtInTools: []string{"tool_search"}})
	h.start()

	if got := h.notes.all(); len(got) != 0 {
		t.Fatalf("notifications = %q", got)
	}
	if got := h.host.GetActiveTools(); !slices.Equal(got, []string{"tool_search"}) {
		t.Fatalf("active tools = %v", got)
	}
}

func TestAgentSessionMCPReachesDeferredMCPToolsThroughCodemodeWhenToolSearchIsNotAvailable(t *testing.T) {
	h := setupSession(t, extension.McpExposureDeferred, nil, setupOptions{builtInTools: []string{"codemode"}, withoutToolSearch: true})
	h.start()

	if got := h.host.GetActiveTools(); !slices.Equal(got, []string{"codemode"}) {
		t.Fatalf("active tools = %v", got)
	}
	if !slices.Contains(h.host.callable(), "mcp__docs__search") {
		t.Fatalf("callable = %v", h.host.callable())
	}
	if got := h.notes.all(); len(got) != 0 {
		t.Fatalf("notifications = %q", got)
	}
}

func TestAgentSessionMCPWarnsWhenDeferredMCPToolsHaveNeitherToolSearchNorCodemode(t *testing.T) {
	h := setupSession(t, extension.McpExposureDeferred, nil, setupOptions{withoutToolSearch: true})
	// No codemode tool is registered either.
	h.host.mu.Lock()
	delete(h.host.tools, mcpext.CodemodeToolName)
	h.host.order = slices.DeleteFunc(h.host.order, func(name string) bool { return name == mcpext.CodemodeToolName })
	h.host.mu.Unlock()
	h.start()

	if got := h.host.GetActiveTools(); len(got) != 0 {
		t.Fatalf("active tools = %v", got)
	}
	want := []string{"MCP tools are only reachable from the codemode or tool_search tool, but neither is active; they cannot be called."}
	if got := h.notes.all(); !slices.Equal(got, want) {
		t.Fatalf("notifications = %q", got)
	}
}

func TestAgentSessionMCPDoesNotActivateAnotherExtensionsToolNamedCodemode(t *testing.T) {
	h := &sessionHarness{host: newFakeHost(), calls: &callLog{}, servers: &serverSet{}, notes: &notifications{}}
	// Registered first, so it wins over the codemode extension's codemode.
	h.host.registerFrom("another-extension", extension.ToolDefinition{Name: "codemode", Exposure: extension.ToolExposureCodemode, Description: "Another extension's codemode tool."}, false)
	entry := mcpext.McpServerEntry{Name: "docs", Config: extension.McpServerConfig{URL: "http://unused.invalid"}, Source: "test"}
	h.ext = mcpext.New(h.host, mcpext.Options{
		LoadConfig: func(mcpext.EventContext) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: []mcpext.McpServerEntry{entry}}
		},
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			client, server := createFakeServer(h.calls, func() []map[string]any { return serverTools }, false)
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	})
	h.ctx = eventContext(t.TempDir(), h.notes)
	t.Cleanup(h.ext.SessionShutdown)
	h.start()

	if got := h.host.GetActiveTools(); len(got) != 0 {
		t.Fatalf("active tools = %v", got)
	}
}

// slowHarness is setupSlow() of agent-session-mcp.test.ts: a server named `slow` (by default) whose answer to
// `initialize` takes initializeDelay, without waiting for it.
type slowHarness struct {
	*sessionHarness
	calls *callLog
}

type slowConfig struct {
	exposure     extension.McpExposure
	toolExposure *extension.OrderedExposures
	name         string
	instructions string
	startupWait  time.Duration
}

func setupSlow(t *testing.T, config slowConfig, initializeDelay time.Duration) *slowHarness {
	t.Helper()
	if config.name == "" {
		config.name = "slow"
	}
	h := &sessionHarness{host: newFakeHost(), calls: &callLog{}, servers: &serverSet{}, notes: &notifications{}}
	h.host.registerBuiltin(mcpext.CodemodeToolName, "builtin:codemode")
	h.host.registerBuiltin(mcpext.ToolSearchToolName, "builtin:tool-search")
	entry := mcpext.McpServerEntry{
		Name: config.name, Source: "test",
		Config: extension.McpServerConfig{URL: "http://unused.invalid", Exposure: config.exposure, ToolExposure: config.toolExposure},
	}
	h.ext = mcpext.New(h.host, mcpext.Options{
		LoadConfig: func(mcpext.EventContext) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: []mcpext.McpServerEntry{entry}}
		},
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			client, server := createFakeServer(h.calls, func() []map[string]any { return serverTools }, false, fakeServerOptions{instructions: config.instructions, initializeDelay: initializeDelay})
			_ = server.Start()
			return client, nil
		},
		StartupWait: config.startupWait,
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	})
	h.ctx = eventContext(t.TempDir(), h.notes)
	t.Cleanup(h.ext.SessionShutdown)
	h.ext.SessionStart(h.ctx)
	return &slowHarness{sessionHarness: h, calls: h.calls}
}

// never is an initializeDelay for a server that never answers `initialize`.
const never = -time.Second

// within fails the test when f takes longer than the limit: the code under test must not wait for what it does not need.
func within(t *testing.T, what string, limit time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%s did not return within %s", what, limit)
	}
}

func toolRegistered(h *fakeHost, name string) bool {
	_, ok := h.tool(name)
	return ok
}

func TestAgentSessionMCPDoesNotHoldTheFirstPromptForCodemodeServersThatAreStillConnecting(t *testing.T) {
	// The server never answers `initialize`. The default startup wait is 10 seconds: the prompt must not wait at all.
	h := setupSlow(t, slowConfig{}, never)

	within(t, "before_agent_start", 2*time.Second, h.prompt)

	if got := h.host.GetActiveTools(); !slices.Equal(got, []string{"codemode"}) {
		t.Fatalf("active tools = %v, want codemode activated from the config before the server connects", got)
	}
	if got := h.notes.all(); len(got) != 0 {
		t.Fatalf("notifications = %q", got)
	}
}

func TestAgentSessionMCPListsAServerBeforeItConnectsAndAddsItsSummaryWithTheNextPrompt(t *testing.T) {
	h := setupSlow(t, slowConfig{instructions: "Slow docs.\nMore."}, 30*time.Millisecond)

	h.prompt()
	first, ok := h.serversSection()
	if !ok || !slices.Contains(strings.Split(first, "\n"), "- mcp__slow (codemode)") {
		t.Fatalf("first section = %q, want the server listed by name only", first)
	}
	waitFor(t, "the server's tools", func() bool { return toolRegistered(h.host, "mcp__slow__search") })
	h.prompt()
	second, _ := h.serversSection()
	if !strings.Contains(second, "- mcp__slow (codemode): Slow docs.") || strings.Contains(second, "More.") {
		t.Fatalf("second section = %q", second)
	}
}

func TestAgentSessionMCPWaitsForServersWithDirectToolsBeforeListingTheServers(t *testing.T) {
	h := setupSlow(t, slowConfig{exposure: extension.McpExposureDirect, toolExposure: extension.NewOrderedExposures("shot", "codemode"), instructions: "Slow docs."}, 30*time.Millisecond)

	h.prompt()

	if section, _ := h.serversSection(); !strings.Contains(section, "- mcp__slow (codemode): Slow docs.") {
		t.Fatalf("section = %q", section)
	}
}

func TestAgentSessionMCPLeavesServersWithOnlyDirectToolsOutOfTheServersSection(t *testing.T) {
	h := setupSlow(t, slowConfig{exposure: extension.McpExposureDirect}, 0)

	h.prompt()

	if section, ok := h.serversSection(); ok {
		t.Fatalf("section = %q, want none", section)
	}
}

func TestAgentSessionMCPRemovesAStaleServersSectionWhenNoServerNeedsIt(t *testing.T) {
	h := setupSession(t, extension.McpExposureDirect, nil, setupOptions{})
	h.start()
	stale := "stale"
	sections := ai.OrderedSections{{Name: mcpext.McpServersSection, Value: &stale}, {Name: "other", Value: &stale}}
	options := &extension.BuildSystemPromptOptions{Sections: &sections}

	h.ext.BeforeAgentStart(h.ctx, options)

	// `delete sections[MCP_SERVERS_SECTION]`: only the section of this extension goes.
	if len(*options.Sections) != 1 || (*options.Sections)[0].Name != "other" {
		t.Fatalf("sections = %+v, want only `other`", *options.Sections)
	}
}

func TestAgentSessionMCPReplacesAnExistingServersSectionInPlace(t *testing.T) {
	h := setupSession(t, extension.McpExposureCodemode, nil, setupOptions{})
	h.start()
	stale := "stale"
	sections := ai.OrderedSections{{Name: mcpext.McpServersSection, Value: &stale}, {Name: "other", Value: &stale}}
	options := &extension.BuildSystemPromptOptions{Sections: &sections}

	h.ext.BeforeAgentStart(h.ctx, options)

	got := *options.Sections
	if len(got) != 2 || got[0].Name != mcpext.McpServersSection || *got[0].Value == stale || got[1].Name != "other" {
		t.Fatalf("sections = %+v, want the section replaced in place", got)
	}
}

func codemodeCall(code string) map[string]any { return map[string]any{"code": code} }

func TestAgentSessionMCPCodemodeScriptWaitsForTheServersItNames(t *testing.T) {
	h := setupSlow(t, slowConfig{}, 30*time.Millisecond)
	if toolRegistered(h.host, "mcp__slow__search") {
		t.Fatal("the server connected before the script ran")
	}

	h.ext.ToolCall(t.Context(), "codemode", codemodeCall(`return (await tools.mcp__slow__search({ query: "q" })).structuredContent;`))

	if !toolRegistered(h.host, "mcp__slow__search") {
		t.Fatal("the script ran before the server it names connected")
	}
}

func TestAgentSessionMCPCodemodeScriptWaitsForServersWhoseScriptIdentifiersDifferFromTheirNames(t *testing.T) {
	h := setupSlow(t, slowConfig{name: "slow-docs"}, 30*time.Millisecond)

	h.ext.ToolCall(t.Context(), "codemode", codemodeCall(`await tools.mcp__slow_docs__search({ query: "q" });`))

	if !toolRegistered(h.host, "mcp__slow_docs__search") {
		t.Fatal("the script ran before the server connected")
	}
}

func TestAgentSessionMCPCodemodeScriptThatSearchesOrEnumeratesWaitsForEveryServer(t *testing.T) {
	for _, code := range []string{`await searchTools("docs")`, `await describeNamespace("slow")`, `await describeTool("x")`, `return ALL_TOOLS.length`} {
		t.Run(code, func(t *testing.T) {
			h := setupSlow(t, slowConfig{}, 30*time.Millisecond)
			h.ext.ToolCall(t.Context(), "codemode", codemodeCall(code))
			if !toolRegistered(h.host, "mcp__slow__search") {
				t.Fatal("the script ran before the server connected")
			}
		})
	}
}

func TestAgentSessionMCPCodemodeScriptDoesNotWaitForServersItDoesNotName(t *testing.T) {
	h := setupSlow(t, slowConfig{}, never)

	within(t, "tool_call", 2*time.Second, func() { h.ext.ToolCall(t.Context(), "codemode", codemodeCall("return 1 + 1;")) })
}

func TestAgentSessionMCPWaitingForAServerEndsWhenTheCallIsAborted(t *testing.T) {
	h := setupSlow(t, slowConfig{}, never)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(20*time.Millisecond, cancel)

	within(t, "tool_call", 5*time.Second, func() { h.ext.ToolCall(ctx, "codemode", codemodeCall(`await searchTools("x")`)) })
	// Already aborted: no wait at all.
	within(t, "tool_call", 2*time.Second, func() { h.ext.ToolCall(ctx, "codemode", codemodeCall(`await searchTools("x")`)) })
}

func TestAgentSessionMCPToolSearchAndTheResourceToolsWaitForEveryServer(t *testing.T) {
	for _, tool := range []string{"tool_search", "list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
		t.Run(tool, func(t *testing.T) {
			h := setupSlow(t, slowConfig{exposure: extension.McpExposureDeferred}, 30*time.Millisecond)
			if tool != "tool_search" {
				h.host.registerBuiltin(tool, "mcp")
			}

			h.ext.ToolCall(t.Context(), tool, map[string]any{"query": "search the docs", "limit": 1})

			if !toolRegistered(h.host, "mcp__slow__search") {
				t.Fatal("the call ran before the server connected")
			}
		})
	}
}

func TestAgentSessionMCPOtherToolsDoNotWaitForServers(t *testing.T) {
	h := setupSlow(t, slowConfig{}, never)
	h.host.registerBuiltin("read", "builtin:read")

	within(t, "tool_call", 2*time.Second, func() {
		h.ext.ToolCall(t.Context(), "read", map[string]any{"path": "a"})
		h.ext.ToolCall(t.Context(), "not registered", nil)
	})
}

func TestAgentSessionMCPHoldsTheFirstPromptForServersWithDirectTools(t *testing.T) {
	h := setupSlow(t, slowConfig{exposure: extension.McpExposureDirect}, 30*time.Millisecond)

	h.prompt()

	if !slices.Contains(h.host.GetActiveTools(), "mcp__slow__search") {
		t.Fatalf("active tools = %v, want the direct tools declared to the first prompt", h.host.GetActiveTools())
	}
}

func TestAgentSessionMCPHoldsTheFirstPromptForServersWithDirectToolsOnlyUpToTheStartupWait(t *testing.T) {
	h := setupSlow(t, slowConfig{exposure: extension.McpExposureDirect, startupWait: 20 * time.Millisecond}, never)

	within(t, "before_agent_start", 5*time.Second, h.prompt)

	if got := h.host.GetActiveTools(); len(got) != 0 {
		t.Fatalf("active tools = %v", got)
	}
	want := []string{"MCP servers are still connecting; their tools become available once connected."}
	if got := h.notes.all(); !slices.Equal(got, want) {
		t.Fatalf("notifications = %q", got)
	}
}

func TestAgentSessionMCPDoesNotLetCodemodeCallItselfOrInactiveDirectTools(t *testing.T) {
	h := setupSession(t, extension.McpExposureDirect, nil, setupOptions{})
	h.start()
	h.host.SetActiveTools([]string{"codemode", "mcp__docs__search"})

	if got := h.host.callable(); !slices.Equal(got, []string{"mcp__docs__search"}) {
		t.Fatalf("callable = %v", got)
	}
}

// --- MCP servers registered by extensions ---

func setupRegistered(t *testing.T, configured []mcpext.McpServerEntry) (*sessionHarness, *serverList) {
	t.Helper()
	host := newFakeHost()
	host.registerBuiltin(mcpext.CodemodeToolName, "builtin:codemode")
	connected := &serverList{}
	h := &sessionHarness{host: host, calls: &callLog{}, servers: &serverSet{}, notes: &notifications{}}
	h.ext = mcpext.New(host, mcpext.Options{
		LoadConfig: func(mcpext.EventContext) mcpext.LoadedMcpConfig { return mcpext.LoadedMcpConfig{Servers: configured} },
		CreateTransport: func(entry mcpext.McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.Transport, error) {
			connected.add(entry)
			client, server := createFakeServer(&callLog{}, func() []map[string]any { return serverTools }, false)
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	})
	h.ctx = eventContext(t.TempDir(), h.notes)
	t.Cleanup(h.ext.SessionShutdown)
	return h, connected
}

type serverList struct {
	mu      sync.Mutex
	entries []mcpext.McpServerEntry
}

func (l *serverList) add(e mcpext.McpServerEntry) {
	l.mu.Lock()
	l.entries = append(l.entries, e)
	l.mu.Unlock()
}

func (l *serverList) all() []mcpext.McpServerEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.entries)
}

func registered(name, url string, exposure extension.McpExposure) extension.RegisteredMcpServer {
	return extension.RegisteredMcpServer{Name: name, Config: extension.McpServerConfig{URL: url, Exposure: exposure}, ExtensionPath: "plugin.ts"}
}

func TestAgentSessionMCPServersRegisteredByExtensionsConnectsServersRegisteredWhileExtensionsLoad(t *testing.T) {
	h, connected := setupRegistered(t, nil)
	h.host.setServers(registered("plugin", "http://plugin.invalid", extension.McpExposureDirect))
	h.start()

	entries := connected.all()
	if len(entries) != 1 || entries[0].Name != "plugin" || entries[0].Scope != "extension" {
		t.Fatalf("connected = %#v", entries)
	}
	if !slices.Contains(h.host.GetActiveTools(), "mcp__plugin__search") {
		t.Fatalf("active tools = %v", h.host.GetActiveTools())
	}
}

func TestAgentSessionMCPServersRegisteredByExtensionsConnectsAndDisconnectsServersRegisteredDuringTheSession(t *testing.T) {
	h, connected := setupRegistered(t, nil)
	h.ext.SessionStart(h.ctx)

	h.host.setServers(registered("late", "http://late.invalid", ""))
	h.ext.McpServersChange(h.ctx)
	if !slices.Contains(h.host.callable(), "mcp__late__search") {
		t.Fatalf("callable = %v", h.host.callable())
	}
	entries := connected.all()
	if len(entries) != 1 || entries[0].Name != "late" {
		t.Fatalf("connected = %#v", entries)
	}
	// Codemode-exposed tools need the codemode tool, which is activated for them.
	if !slices.Contains(h.host.GetActiveTools(), "codemode") {
		t.Fatalf("active tools = %v", h.host.GetActiveTools())
	}

	h.host.setServers()
	h.ext.McpServersChange(h.ctx)
	if slices.Contains(h.host.callable(), "mcp__late__search") {
		t.Fatalf("callable = %v", h.host.callable())
	}
}

// "prefers the mcp.json server over a registered %s": "my_docs" shares the namespace of "my-docs" (#10239).
func TestAgentSessionMCPServersRegisteredByExtensionsPrefersTheMCPJSONServerOverARegisteredServerOfTheSameName(t *testing.T) {
	for _, name := range []string{"my-docs", "my_docs"} {
		t.Run(name, func(t *testing.T) {
			configured := mcpext.McpServerEntry{Name: "my-docs", Config: extension.McpServerConfig{URL: "http://config.invalid"}, Source: "mcp.json"}
			h, connected := setupRegistered(t, []mcpext.McpServerEntry{configured})
			h.host.setServers(registered(name, "http://plugin.invalid", ""))
			h.start()

			waitFor(t, "the configured server to connect", func() bool { return len(connected.all()) == 1 })
			time.Sleep(10 * time.Millisecond)
			entries := connected.all()
			if len(entries) != 1 || entries[0].Name != "my-docs" || entries[0].Config.URL != "http://config.invalid" || entries[0].Source != "mcp.json" {
				t.Fatalf("connected = %#v", entries)
			}
			want := `overridden: "` + name + `" registered by plugin.ts is overridden by "my-docs" in mcp.json`
			if !strings.Contains(strings.Join(h.ext.Notices(), "\n"), want) {
				t.Fatalf("notices = %v, want %q", h.ext.Notices(), want)
			}
		})
	}
}

// runtime.ts connect stores `client.instructions?.trim() || undefined`. String.prototype.trim removes U+FEFF and keeps
// U+0085 (Node: "\uFEFF Docs.\u0085".trim() is "Docs.\u0085"), and the namespace carries that value
// (index.ts:352-357).
func TestAgentSessionMCPTrimsServerInstructionsAsJavaScriptDoes(t *testing.T) {
	h := setupSession(t, extension.McpExposureCodemode, nil, setupOptions{instructions: "\uFEFF Docs.\u0085"})
	h.start()

	found := false
	for _, tool := range h.host.GetAllTools() {
		if strings.HasPrefix(tool.Name, "mcp__docs__") {
			found = true
			jsonEqual(t, tool.Namespace, `{"name":"mcp__docs","instructions":"Docs.\u0085"}`)
		}
	}
	if !found {
		t.Fatal("no mcp__docs tool registered")
	}
}
