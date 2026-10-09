package mcpext_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// jsonEqual reports whether got marshals to the same JSON value as want, the
// way vitest's toEqual compares plain objects.
func jsonEqual(t *testing.T, got any, want string) {
	t.Helper()
	gotBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var g, w any
	if err := json.Unmarshal(gotBytes, &g); err != nil {
		t.Fatalf("unmarshal got %s: %v", gotBytes, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("unmarshal want %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %s\nwant %s", gotBytes, want)
	}
}

// waitFor is the test-side equivalent of awaiting a macrotask: it polls until
// condition holds and fails after a generous bound.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// hookTransport lets a test replace the client end's Send, as
// `transport.send = async ...` does upstream.
type hookTransport struct {
	*mcptest.InMemoryTransport
	send func(mcp.JSONRPCMessage) error
}

func (h *hookTransport) Send(message mcp.JSONRPCMessage) error { return h.send(message) }

// SendOrdered keeps the embedded transport's promoted SendOrdered from bypassing send.
func (h *hookTransport) SendOrdered(message mcp.JSONRPCMessage, placed func()) error {
	defer placed()
	return h.send(message)
}

type fakeTransportOptions struct {
	methods         *methodLog
	noTools         bool
	expireFirstCall bool
}

type methodLog struct {
	mu      sync.Mutex
	methods []string
}

func (l *methodLog) add(m string) {
	l.mu.Lock()
	l.methods = append(l.methods, m)
	l.mu.Unlock()
}

func (l *methodLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.methods)
}

// serverSet records the server ends of the transports a test created.
type serverSet struct {
	mu      sync.Mutex
	servers []*mcptest.InMemoryTransport
}

func (s *serverSet) add(t *mcptest.InMemoryTransport) {
	s.mu.Lock()
	s.servers = append(s.servers, t)
	s.mu.Unlock()
}

func (s *serverSet) last() *mcptest.InMemoryTransport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servers[len(s.servers)-1]
}

// createFakeTransport is createTransport of mcp-extension.test.ts: an
// in-memory server that answers initialize, tools/list, and tools/call with "ok".
func createFakeTransport(servers *serverSet, options fakeTransportOptions) mcp.Transport {
	client, server := mcptest.NewInMemoryTransportPair()
	servers.add(server)
	server.OnMessage(func(message mcp.JSONRPCMessage) {
		if !message.IsRequest() {
			return
		}
		if options.methods != nil {
			options.methods.add(message.Method)
		}
		var response mcp.JSONRPCMessage
		id := *message.ID
		switch message.Method {
		case "initialize":
			caps := `{"tools":{}}`
			if options.noTools {
				caps = `{"prompts":{}}`
			}
			response = mcp.NewResult(id, json.RawMessage(fmt.Sprintf(`{"protocolVersion":%q,"capabilities":%s,"serverInfo":{"name":"fake","version":"1.0.0"}}`, mcp.LatestProtocolVersion, caps)))
		case "tools/list":
			if options.noTools {
				response = mcp.NewErrorResponse(id, -32601, "Method not found", nil)
			} else {
				response = mcp.NewResult(id, json.RawMessage(`{"tools":[]}`))
			}
		case "resources/read":
			response = mcp.NewResult(id, json.RawMessage(`{"contents":[{"uri":"docs://a","text":"ok"}]}`))
		default:
			response = mcp.NewResult(id, json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`))
		}
		go func() { _ = server.Send(response) }()
	})
	_ = server.Start()
	if !options.expireFirstCall {
		return client
	}
	return &hookTransport{InMemoryTransport: client, send: func(message mcp.JSONRPCMessage) error {
		// Simulates the HTTP transport's 404 for a session the server no longer knows.
		if message.Method == "tools/call" {
			return mcp.NewMcpSessionExpiredError("gone")
		}
		return client.Send(message)
	}}
}

// fakeHost is a Host that behaves like the runner for the parts the MCP
// extension uses: registering a tool replaces an earlier registration of the
// name, tools with direct exposure are activated on registration, and hidden
// tools are unreachable.
type fakeHost struct {
	mu    sync.Mutex
	order []string
	// registrations lists every RegisterTool call by tool name and exposure, in call order.
	registrations []string
	tools         map[string]extension.ToolDefinition
	paths         map[string]string
	active        []string
	servers       []extension.RegisteredMcpServer
}

func newFakeHost(active ...string) *fakeHost {
	return &fakeHost{tools: map[string]extension.ToolDefinition{}, paths: map[string]string{}, active: slices.Clone(active)}
}

func (h *fakeHost) RegisterTool(definition extension.ToolDefinition) {
	h.mu.Lock()
	h.registrations = append(h.registrations, definition.Name+":"+string(definition.Exposure))
	h.mu.Unlock()
	h.registerFrom("mcp", definition, true)
}

// registerFrom registers a tool for the extension at path. Later registrations
// by the same path replace; another path's registration of a taken name is ignored,
// as the first extension wins.
func (h *fakeHost) registerFrom(path string, definition extension.ToolDefinition, activateDirect bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if owner, taken := h.paths[definition.Name]; taken && owner != path {
		return
	}
	if _, exists := h.tools[definition.Name]; !exists {
		h.order = append(h.order, definition.Name)
	}
	h.tools[definition.Name] = definition
	h.paths[definition.Name] = path
	// A hidden tool is unreachable: activating it has no effect.
	if definition.Exposure == extension.ToolExposureHidden {
		h.active = slices.DeleteFunc(h.active, func(name string) bool { return name == definition.Name })
	}
	direct := definition.Exposure == "" || definition.Exposure == extension.ToolExposureDirect || definition.Exposure == extension.ToolExposureModelOnly
	if activateDirect && direct && !slices.Contains(h.active, definition.Name) {
		h.active = append(h.active, definition.Name)
	}
}

func (h *fakeHost) GetAllTools() []extension.ToolInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	infos := make([]extension.ToolInfo, 0, len(h.order))
	for _, name := range h.order {
		d := h.tools[name]
		exposure := d.Exposure
		if exposure == "" {
			exposure = extension.ToolExposureDirect
		}
		infos = append(infos, extension.ToolInfo{
			Name: name, Description: d.Description, Parameters: d.Parameters,
			SourceInfo: extension.SourceInfo{Path: h.paths[name]}, Exposure: exposure, Namespace: d.Namespace, Annotations: d.Annotations,
		})
	}
	return infos
}

func (h *fakeHost) GetActiveTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.active)
}

func (h *fakeHost) SetActiveTools(names []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = slices.Clone(names)
}

func (h *fakeHost) GetMcpServers() []extension.RegisteredMcpServer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.servers)
}

func (h *fakeHost) setServers(servers ...extension.RegisteredMcpServer) {
	h.mu.Lock()
	h.servers = servers
	h.mu.Unlock()
}

func (h *fakeHost) tool(name string) (extension.ToolDefinition, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d, ok := h.tools[name]
	return d, ok
}

// callable is the tools callable from codemode scripts: codemode and deferred
// tools whenever registered, direct tools while active, never hidden or
// model-only tools, and never the codemode tool itself.
func (h *fakeHost) callable() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var names []string
	for _, name := range h.order {
		if name == mcpext.CodemodeToolName {
			continue
		}
		switch h.tools[name].Exposure {
		case extension.ToolExposureCodemode, extension.ToolExposureDeferred:
			names = append(names, name)
		case "", extension.ToolExposureDirect:
			if slices.Contains(h.active, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// registerBuiltin registers a stand-in for a built-in tool, inactive until activated.
func (h *fakeHost) registerBuiltin(name, path string) {
	h.registerFrom(path, extension.ToolDefinition{Name: name}, false)
}

type notifications struct {
	mu       sync.Mutex
	messages []string
}

func (n *notifications) notify(message, _ string) {
	n.mu.Lock()
	n.messages = append(n.messages, message)
	n.mu.Unlock()
}

func (n *notifications) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.messages)
}

func eventContext(cwd string, n *notifications) mcpext.EventContext {
	return mcpext.EventContext{Cwd: cwd, IsProjectTrusted: func() bool { return true }, Notify: n.notify}
}

// unauthorizedResponse is `new Response(null, { status: 401 })`.
func unauthorizedResponse() *http.Response {
	return &http.Response{StatusCode: 401, Header: http.Header{}, Body: http.NoBody}
}
