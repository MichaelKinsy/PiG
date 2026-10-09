package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// wireExt is an extension runtime reduced to its wire behavior: it registers, answers pings and host requests, records notifies and cancels, and makes calls on request.
type wireExt struct {
	t       *testing.T
	conn    net.Conn
	write   sync.Mutex
	respond func(*Envelope) *ResponsePayload
	// handle answers a request on its own goroutine, so the handler can make calls the read loop must deliver results for.
	handle   func(*Envelope) *ResponsePayload
	notifies chan *Envelope
	cancels  chan *Envelope
	requests chan *Envelope
	results  sync.Map // call id -> chan *CallResultPayload
	nextCall atomic.Int64
}

func newWireExt(t *testing.T) *wireExt {
	return &wireExt{t: t, notifies: make(chan *Envelope, 64), cancels: make(chan *Envelope, 8), requests: make(chan *Envelope, 8)}
}

func (w *wireExt) serve(reg *RegisterPayload) func(net.Conn) error {
	return func(conn net.Conn) error {
		w.conn = conn
		if err := w.send(&Envelope{Type: MsgRegister, Register: reg}); err != nil {
			return err
		}
		for {
			env, err := readEnvelopeFrom(conn)
			if err != nil {
				return nil
			}
			switch env.Type {
			case MsgPing:
				_ = w.send(&Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			case MsgRequest:
				w.requests <- env
				if w.handle != nil {
					go func() {
						if response := w.handle(env); response != nil {
							_ = w.send(&Envelope{Type: MsgResponse, ID: env.ID, Response: response})
						}
					}()
				} else if w.respond != nil {
					if response := w.respond(env); response != nil {
						_ = w.send(&Envelope{Type: MsgResponse, ID: env.ID, Response: response})
					}
				}
			case MsgNotify:
				w.notifies <- env
			case MsgCancel:
				w.cancels <- env
			case MsgCallResult:
				if ch, ok := w.results.Load(env.ID); ok {
					ch.(chan *CallResultPayload) <- env.CallResult
				}
			}
		}
	}
}

func (w *wireExt) send(env *Envelope) error {
	w.write.Lock()
	defer w.write.Unlock()
	return writeEnvelopeTo(w.conn, env)
}

// call sends one extension→host call and waits for its result.
func (w *wireExt) call(method string, args any, parentRequestID string) *CallResultPayload {
	w.t.Helper()
	result, err := w.callErr(method, args, parentRequestID)
	if err != nil {
		w.t.Fatal(err)
	}
	return result
}

// callErr is call for a goroutine other than the test's, which must not call t.Fatal.
func (w *wireExt) callErr(method string, args any, parentRequestID string) (*CallResultPayload, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	id := method + "-" + string(rune('a'+w.nextCall.Add(1)))
	ch := make(chan *CallResultPayload, 1)
	w.results.Store(id, ch)
	if err := w.send(&Envelope{Type: MsgCall, ID: id, Call: &CallPayload{Method: method, Args: raw, ParentRequestID: parentRequestID}}); err != nil {
		return nil, err
	}
	select {
	case result := <-ch:
		return result, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("no result for call %s", method)
	}
}

// request returns the next host request of the method the extension received.
func (w *wireExt) request(method string) *Envelope {
	w.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case env := <-w.requests:
			if env.Request != nil && env.Request.Method == method {
				return env
			}
		case <-timeout:
			w.t.Fatalf("no %s request", method)
			return nil
		}
	}
}

func loadWireExt(t *testing.T, host *Host, w *wireExt, reg *RegisterPayload) (*extension.Extension, error) {
	t.Helper()
	// The load context owns the extension's connection, so it lives until the test ends.
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	return host.LoadInProcess(ctx, ExtConfig{Name: reg.Name, Path: "/ext/" + reg.Name + ".ts", Enabled: true}, w.serve(reg))
}

func newWireHost(t *testing.T) *Host {
	t.Helper()
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	return host
}

// Upstream types.ts:579-607 (outputSchema, exposure, namespace, annotations, defaultActive, prepareLoadout) as registerTool stores them, and types.ts:2063 (ToolInfo).
func TestRegisterPayloadCarriesToolExposureFields(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "exposure", Tools: []ToolDecl{
		{
			Name: "search", Label: "Search", Description: "Search", Parameters: json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}}}`),
			Exposure:     "codemode", Namespace: &extension.ToolNamespace{Name: "mcp__docs", Description: "Docs server", Instructions: "Search before reading."},
			Annotations:   &extension.ToolAnnotations{ReadOnlyHint: new(true), OpenWorldHint: new(false)},
			DefaultActive: new(false), PreparesLoadout: true,
		},
		{Name: "plain", Label: "Plain", Description: "Plain", Parameters: json.RawMessage(`{"type":"object"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	search := ext.Tools["search"].Definition
	if string(search.OutputSchema) != `{"type":"object","properties":{"n":{"type":"number"}}}` || search.Exposure != extension.ToolExposureCodemode {
		t.Fatalf("outputSchema=%s exposure=%q", search.OutputSchema, search.Exposure)
	}
	if search.Namespace == nil || search.Namespace.Name != "mcp__docs" || search.Namespace.Description != "Docs server" || search.Namespace.Instructions != "Search before reading." {
		t.Fatalf("namespace = %+v", search.Namespace)
	}
	if a := search.Annotations; a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint || a.DestructiveHint != nil {
		t.Fatalf("annotations = %+v", search.Annotations)
	}
	if search.DefaultActive == nil || *search.DefaultActive {
		t.Fatalf("defaultActive = %v, want false", search.DefaultActive)
	}
	if search.PrepareLoadout == nil {
		t.Fatal("a tool that declares prepares_loadout has no PrepareLoadout")
	}
	plain := ext.Tools["plain"].Definition
	if plain.PrepareLoadout != nil || plain.Exposure != "" || plain.DefaultActive != nil || plain.OutputSchema != nil || plain.Namespace != nil || plain.Annotations != nil {
		t.Fatalf("an undeclared field was set on %+v", plain)
	}
}

// Upstream types.ts:601-607 (prepareLoadout), 541-563 (ToolLoadout, ToolLoadoutChanges): the tool sees the declared, callable and registered tools plus each tool's exposure and namespace, and answers the changes, or nothing.
func TestPrepareLoadoutRunsInTheExtension(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	changes := `{"descriptions":{"read":"Read a file (via codemode)"},"hiddenDeclarations":["grep"]}`
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestPrepareLoadout {
			return nil
		}
		return &ResponsePayload{Result: json.RawMessage(changes)}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "loadout", Tools: []ToolDecl{
		{Name: "codemode", Description: "Run scripts", Parameters: json.RawMessage(`{"type":"object"}`), PreparesLoadout: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	read := extension.AgentTool{Name: "read", Label: "Read", Description: "Read", Parameters: json.RawMessage(`{"type":"object"}`)}
	grep := extension.AgentTool{Name: "grep", Label: "Grep", Description: "Grep", Parameters: json.RawMessage(`{"type":"object"}`)}
	loadout := extension.ToolLoadout{
		Declared: []extension.AgentTool{read, grep}, Callable: []extension.AgentTool{read}, Registered: []extension.AgentTool{read, grep},
		GetExposure: func(name string) extension.ToolExposure {
			if name == "grep" {
				return extension.ToolExposureDeferred
			}
			return extension.ToolExposureDirect
		},
		GetNamespace: func(name string) *extension.ToolNamespace {
			if name == "grep" {
				return &extension.ToolNamespace{Name: "search"}
			}
			return nil
		},
		GetPromptGuidelines: func(name string) []string {
			if name == "grep" {
				return []string{"Use grep for patterns."}
			}
			return nil
		},
	}
	if ext.Tools["codemode"].Definition.PrepareLoadout == nil {
		t.Fatal("a tool that declares prepares_loadout has no PrepareLoadout")
	}
	got := ext.Tools["codemode"].Definition.PrepareLoadout(loadout)
	if got == nil || got.Descriptions["read"] != "Read a file (via codemode)" || len(got.HiddenDeclarations) != 1 || got.HiddenDeclarations[0] != "grep" {
		t.Fatalf("changes = %+v", got)
	}
	env := <-w.requests
	if env.Request.Method != RequestPrepareLoadout || env.Request.Tool != "codemode" {
		t.Fatalf("request = %+v", env.Request)
	}
	var payload ToolLoadoutPayload
	if err := json.Unmarshal(env.Request.Args, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Declared) != 2 || len(payload.Callable) != 1 || len(payload.Registered) != 2 ||
		payload.Exposures["grep"] != extension.ToolExposureDeferred || payload.Exposures["read"] != extension.ToolExposureDirect ||
		payload.Namespaces["grep"] == nil || payload.Namespaces["grep"].Name != "search" || payload.Namespaces["read"] != nil ||
		!reflect.DeepEqual(payload.PromptGuidelines, map[string][]string{"grep": {"Use grep for patterns."}}) {
		t.Fatalf("payload = %+v", payload)
	}

	// types.ts:607: `LoadoutChanges | undefined`. A null answer is no change.
	changes = `null`
	if got := ext.Tools["codemode"].Definition.PrepareLoadout(loadout); got != nil {
		t.Fatalf("null answer produced %+v", got)
	}

	// agent-session.ts:1520-1533: a hook that throws is reported as a prepare_loadout extension error with its message, so the extension's error must reach the session as the hook's panic, not vanish as "no changes".
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestPrepareLoadout {
			return nil
		}
		return &ResponsePayload{Error: &ErrorInfo{Message: "loadout exploded"}}
	}
	var thrown *extension.ToolLoadoutChanges
	recovered := func() (recovered any) {
		defer func() { recovered = recover() }()
		thrown = ext.Tools["codemode"].Definition.PrepareLoadout(loadout)
		return nil
	}()
	if err, ok := recovered.(error); !ok || err.Error() != "loadout exploded" {
		t.Fatalf("a throwing prepareLoadout returned %+v and panicked with %#v, want a panic with the extension's error", thrown, recovered)
	}
}

func mcpDecl(name, url string) McpServerDecl {
	return McpServerDecl{Name: name, Config: json.RawMessage(`{"url":"` + url + `"}`)}
}

// Upstream loader.ts:456-478: servers registered while the factory ran reach the runtime, owned by the extension's path, in registration order.
func TestRegisterPayloadRegistersMcpServersWithTheRuntime(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "plugin", McpServers: []McpServerDecl{mcpDecl("docs", "http://docs.invalid"), mcpDecl("wiki", "http://wiki.invalid")}})
	if err != nil {
		t.Fatal(err)
	}
	servers := host.Runtime().McpServers().List()
	if len(servers) != 2 || servers[0].Name != "docs" || servers[0].Config.URL != "http://docs.invalid" || servers[1].Name != "wiki" ||
		servers[0].ExtensionPath != ext.Path || servers[1].ExtensionPath != ext.Path {
		t.Fatalf("servers = %+v (extension path %q)", servers, ext.Path)
	}
}

// Upstream loader.ts:458-465 throw inside the factory, which fails the load with the loader's message.
func TestRegisterPayloadRejectsInvalidAndForeignMcpServers(t *testing.T) {
	host := newWireHost(t)
	first := newWireExt(t)
	if _, err := loadWireExt(t, host, first, &RegisterPayload{Name: "first", McpServers: []McpServerDecl{mcpDecl("taken", "http://x.invalid")}}); err != nil {
		t.Fatal(err)
	}
	second := newWireExt(t)
	_, err := loadWireExt(t, host, second, &RegisterPayload{Name: "second", McpServers: []McpServerDecl{mcpDecl("taken", "http://z.invalid")}})
	if err == nil || !strings.Contains(err.Error(), `MCP server "taken" is already registered by extension "/ext/first.ts"`) {
		t.Fatalf("foreign name error = %v", err)
	}
	third := newWireExt(t)
	_, err = loadWireExt(t, host, third, &RegisterPayload{Name: "third", McpServers: []McpServerDecl{{Name: "no good", Config: json.RawMessage(`{"url":"http://x.invalid"}`)}}})
	if err == nil || !strings.Contains(err.Error(), `Invalid MCP server registered by extension "/ext/third.ts": invalid server name "no good"`) {
		t.Fatalf("invalid config error = %v", err)
	}
	if got := len(host.Runtime().McpServers().List()); got != 1 {
		t.Fatalf("a rejected load left %d servers registered", got)
	}
}

// Upstream loader.ts:456-478 as calls: the result carries every registered server so the SDK's replicated getMcpServers sees the change; failures come back as call errors.
func TestRegisterMcpServerCalls(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "plugin"})
	if err != nil {
		t.Fatal(err)
	}
	other := newWireExt(t)
	if _, err := loadWireExt(t, host, other, &RegisterPayload{Name: "other", McpServers: []McpServerDecl{mcpDecl("taken", "http://t.invalid")}}); err != nil {
		t.Fatal(err)
	}

	result := w.call(CallRegisterMcpServer, mcpDecl("late", "http://late.invalid"), "")
	if result.Error != nil {
		t.Fatalf("register: %+v", result.Error)
	}
	var listed McpServersResult
	if err := json.Unmarshal(result.Result, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Servers) != 2 || listed.Servers[0].Name != "taken" || listed.Servers[1].Name != "late" || listed.Servers[1].ExtensionPath != ext.Path {
		t.Fatalf("listed = %+v", listed.Servers)
	}

	if result := w.call(CallRegisterMcpServer, mcpDecl("taken", "http://z.invalid"), ""); result.Error == nil ||
		!strings.Contains(result.Error.Message, `MCP server "taken" is already registered by extension "/ext/other.ts"`) {
		t.Fatalf("foreign register result = %+v", result)
	}
	if result := w.call(CallRegisterMcpServer, McpServerDecl{Name: "bad", Config: json.RawMessage(`{"command":7}`)}, ""); result.Error == nil ||
		!strings.Contains(result.Error.Message, `Invalid MCP server registered by extension "/ext/plugin.ts"`) {
		t.Fatalf("invalid register result = %+v", result)
	}

	// Unregistering a name another extension owns leaves it; the caller's own goes.
	if result := w.call(CallUnregisterMcpServer, McpServerRef{Name: "taken"}, ""); result.Error != nil {
		t.Fatal(result.Error)
	}
	result = w.call(CallUnregisterMcpServer, McpServerRef{Name: "late"}, "")
	if err := json.Unmarshal(result.Result, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Servers) != 1 || listed.Servers[0].Name != "taken" {
		t.Fatalf("after unregister = %+v", listed.Servers)
	}
}

// Upstream types.ts:1846-1855, loader.ts:480-497 and virtual-models.ts:87-101: a virtual model declared by an extension is queued with its definition, and its route runs in the extension.
func TestVirtualModelDeclarationRoutesThroughTheExtension(t *testing.T) {
	host := newWireHost(t)
	// The route names the physical model by provider and id; coding.ModelRuntime.ResolveModel resolves it (model-runtime.ts:1000-1009).
	isPhysical := func(model *ai.Model) bool {
		return model != nil && model.ProviderMeta.ProviderID == "anthropic" && model.ID == "physical"
	}
	w := newWireExt(t)
	route := `{"model":{"provider":"anthropic","id":"physical"},"thinkingLevel":"high","state":{"n":2}}`
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestVirtualModelRoute {
			return nil
		}
		return &ResponsePayload{Result: json.RawMessage(route)}
	}
	if _, err := loadWireExt(t, host, w, &RegisterPayload{Name: "router", VirtualModels: []VirtualModelDecl{
		{Provider: "router", ID: "auto", Name: "Auto", ThinkingLevels: []string{"off", "high"}, ContextWindow: 200000, MaxTokens: 8000, Input: []string{"text"}},
	}}); err != nil {
		t.Fatal(err)
	}
	pending := host.Runtime().PendingVirtualModelRegistrations()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	def := pending[0].Definition
	if def.Provider != "router" || def.ID != "auto" || def.Name != "Auto" || def.ContextWindow != 200000 || def.MaxTokens != 8000 ||
		len(def.ThinkingLevels) != 2 || def.ThinkingLevels[1] != ai.ThinkingHigh || len(def.Input) != 1 || def.Input[0] != "text" || def.Route == nil {
		t.Fatalf("definition = %+v", def)
	}
	if pending[0].ExtensionPath != "/ext/router.ts" {
		t.Fatalf("extension path = %q", pending[0].ExtensionPath)
	}

	got, err := def.Route(t.Context(), extension.ModelRouteRequest{
		Model: &ai.Model{ID: "auto"}, ThinkingLevel: ai.ThinkingLow, Reason: extension.ModelRouteReasonUser,
		State:    json.RawMessage(`{"n":1}`),
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isPhysical(got.Model) || got.ThinkingLevel != ai.ThinkingHigh || string(got.State) != `{"n":2}` {
		t.Fatalf("route = %+v", got)
	}
	env := <-w.requests
	var args VirtualModelRouteArgs
	if err := json.Unmarshal(env.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	if env.Request.Method != RequestVirtualModelRoute || args.Provider != "router" || args.ID != "auto" ||
		args.Request.Model["id"] != "auto" || args.Request.ThinkingLevel != "low" || args.Request.Reason != "user" ||
		string(args.Request.State) != `{"n":1}` || len(args.Request.Messages) != 1 || args.Request.Previous != nil || args.Request.Failed != nil {
		t.Fatalf("args = %+v", args)
	}

	// model-runtime.ts:1000-1009: the route goes back as the router returned it; the model runtime, not the router call, rejects a model that is not physical (`... routed to anthropic/missing, which is not a physical model.`), so the route names the model it was given.
	route = `{"model":{"provider":"anthropic","id":"missing"},"thinkingLevel":"off"}`
	unknown, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "auto"}, Reason: extension.ModelRouteReasonDirect})
	if err != nil || unknown.Model == nil || unknown.Model.ProviderMeta.ProviderID != "anthropic" || unknown.Model.ID != "missing" {
		t.Fatalf("unknown physical model route = %+v, %v", unknown, err)
	}
	route = `{"model":{"provider":"anthropic","id":"physical"},"thinkingLevel":"off"}`
	got, err = def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "auto"}, Reason: extension.ModelRouteReasonDirect})
	if err != nil || got.State != nil {
		t.Fatalf("route without state = %+v, %v", got, err)
	}
}

// Upstream virtual-models.ts:66 (`signal`): cancelling the request cancels the router, and an error the router throws fails the request with its message.
func TestVirtualModelRouteCancellationAndErrors(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	fail := false
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestVirtualModelRoute {
			return nil
		}
		if fail {
			return &ResponsePayload{Error: &ErrorInfo{Message: "router exploded"}}
		}
		return nil // Never answers: the request ends by cancellation.
	}
	if _, err := loadWireExt(t, host, w, &RegisterPayload{Name: "router", VirtualModels: []VirtualModelDecl{{Provider: "router", ID: "auto", Name: "Auto"}}}); err != nil {
		t.Fatal(err)
	}
	pending := host.Runtime().PendingVirtualModelRegistrations()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	def := pending[0].Definition

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := def.Route(ctx, extension.ModelRouteRequest{Model: &ai.Model{ID: "auto"}, Reason: extension.ModelRouteReasonUser})
		done <- err
	}()
	request := <-w.requests
	cancel()
	select {
	case cancelled := <-w.cancels:
		if cancelled.Cancel == nil || cancelled.Cancel.RequestID != request.ID {
			t.Fatalf("cancel = %+v, want request %s", cancelled.Cancel, request.ID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the router was not cancelled")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled route error = %v", err)
	}

	fail = true
	if _, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "auto"}, Reason: extension.ModelRouteReasonUser}); err == nil || !strings.Contains(err.Error(), "router exploded") {
		t.Fatalf("router error = %v", err)
	}
}

// Upstream loader.ts:480-497 as calls.
func TestRegisterVirtualModelCalls(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	if _, err := loadWireExt(t, host, w, &RegisterPayload{Name: "router"}); err != nil {
		t.Fatal(err)
	}
	if result := w.call(CallRegisterVirtualModel, VirtualModelDecl{Provider: "router", ID: "late", Name: "Late"}, ""); result.Error != nil {
		t.Fatalf("register: %+v", result.Error)
	}
	pending := host.Runtime().PendingVirtualModelRegistrations()
	if len(pending) != 1 || pending[0].Definition.ID != "late" || pending[0].ExtensionPath != "/ext/router.ts" {
		t.Fatalf("pending = %+v", pending)
	}
	if result := w.call(CallUnregisterVirtualModel, VirtualModelRef{Provider: "router", ID: "late"}, ""); result.Error != nil {
		t.Fatalf("unregister: %+v", result.Error)
	}
	if got := len(host.Runtime().PendingVirtualModelRegistrations()); got != 0 {
		t.Fatalf("unregister left %d registrations", got)
	}
}

// Upstream runner.ts:966-983: executeTool runs another tool for the calling tool through the bound action. Partial results reach the extension in order before the outcome, the call runs under the calling request's cancellation, and the extension's own signal cancels it.
func TestExecuteToolCallRunsNestedCallsWithUpdatesAndCancellation(t *testing.T) {
	host := newWireHost(t)
	bridge := NewUIBridge(func() {})
	var mu sync.Mutex
	var seen struct {
		caller, name string
		args         string
		wantsUpdate  bool
	}
	started := make(chan context.Context, 2)
	bridge.SetActions(&HostCallbacks{ExecuteTool: func(ctx context.Context, callerID, name string, args json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
		mu.Lock()
		seen.caller, seen.name, seen.args, seen.wantsUpdate = callerID, name, string(args), options.OnUpdate != nil
		mu.Unlock()
		if name == "slow" {
			started <- ctx
			<-ctx.Done()
			return extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/2", Name: name}, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "aborted"}}}, IsError: true}, nil
		}
		update := options.OnUpdate
		_ = update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial 1"}}, Details: map[string]any{"n": 1}})
		_ = update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial 2"}}})
		return extension.AgentToolCallOutcome{
			ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: map[string]any{"a": float64(1)}},
			Result:   agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{"ok": true}},
		}, nil
	}})
	host.SetUIBridge(bridge)
	w := newWireExt(t)
	outcomes := make(chan *CallResultPayload, 4)
	w.handle = func(env *Envelope) *ResponsePayload {
		if env.Request.Method == RequestExecuteToolUpdate {
			return &ResponsePayload{Result: json.RawMessage(`null`)}
		}
		if env.Request.Method != "tool_call" {
			return nil
		}
		// The nested calls belong to the calling tool's request.
		switch env.Request.Tool {
		case "orchestrate":
			if result, err := w.callErr(CallExecuteTool, ExecuteToolArgs{CallerID: env.Request.ToolCallID, Name: "echo", Args: json.RawMessage(`{"a":1}`), ExecuteID: "x1", WantsUpdates: true}, env.ID); err == nil {
				outcomes <- result
			}
			return &ResponsePayload{Result: json.RawMessage(`{"content":"orchestrated"}`)}
		default: // "hang": a nested call that only the extension's signal or the parent request's cancellation ends.
			// The host drops the reply of a call whose calling request was cancelled, as it does for every call; the SDK rejects the call then. This handler runs beyond the test, so it never reports through t.
			if result, err := w.callErr(CallExecuteTool, ExecuteToolArgs{CallerID: env.Request.ToolCallID, Name: "slow", Args: json.RawMessage(`{}`), ExecuteID: "x2"}, env.ID); err == nil {
				outcomes <- result
			}
			return &ResponsePayload{Result: json.RawMessage(`{"content":"hung"}`)}
		}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "runner", Tools: []ToolDecl{
		{Name: "orchestrate", Description: "O", Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "hang", Description: "H", Parameters: json.RawMessage(`{"type":"object"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ext.Tools["orchestrate"].Definition.Execute(t.Context(), "call-1", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	var result *CallResultPayload
	select {
	case result = <-outcomes:
	case <-time.After(10 * time.Second):
		t.Fatal("the orchestrating tool received no executeTool result")
	}
	if result.Error != nil {
		t.Fatalf("executeTool: %+v", result.Error)
	}
	var outcome ExecuteToolOutcome
	if err := json.Unmarshal(result.Result, &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.IsError || outcome.ToolCall.ID != "call-1/1" || outcome.ToolCall.Name != "echo" {
		t.Fatalf("outcome = %+v", outcome)
	}
	var value struct {
		Content []struct{ Type, Text string } `json:"content"`
		Details map[string]any                `json:"details"`
	}
	if err := json.Unmarshal(outcome.Result, &value); err != nil || len(value.Content) != 1 || value.Content[0].Type != "text" || value.Content[0].Text != "done" || value.Details["ok"] != true {
		t.Fatalf("result = %s (%v)", outcome.Result, err)
	}
	mu.Lock()
	if seen.caller != "call-1" || seen.name != "echo" || seen.args != `{"a":1}` || !seen.wantsUpdate {
		t.Fatalf("action saw %+v", seen)
	}
	mu.Unlock()

	// Partial results arrive as requests, in order, each naming the call.
	for i, want := range []string{`{"content":[{"type":"text","text":"partial 1"}],"details":{"n":1}}`, `{"content":[{"type":"text","text":"partial 2"}]}`} {
		env := w.request(RequestExecuteToolUpdate)
		var update ExecuteToolUpdate
		if err := json.Unmarshal(env.Request.Args, &update); err != nil {
			t.Fatal(err)
		}
		if update.ExecuteID != "x1" || string(update.Result) != want {
			t.Fatalf("update %d = %+v (%s), want %s", i, update, update.Result, want)
		}
	}

	// The calling request's cancellation cancels the nested call (runner.ts:981 defaults the signal to the tool's).
	parent, cancelParent := context.WithCancel(t.Context())
	hung := make(chan error, 1)
	go func() {
		_, err := ext.Tools["hang"].Definition.Execute(parent, "call-2", json.RawMessage(`{}`), nil)
		hung <- err
	}()
	ctx := <-started
	if ctx.Err() != nil {
		t.Fatal("the nested call was cancelled before its caller")
	}
	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the calling tool did not cancel the nested call")
	}
	<-hung

	// The extension's own signal: executeTool.cancel cancels the running call.
	done := make(chan *CallResultPayload, 1)
	go func() {
		result, err := w.callErr(CallExecuteTool, ExecuteToolArgs{CallerID: "call-3", Name: "slow", Args: json.RawMessage(`{}`), ExecuteID: "x3"}, "")
		if err != nil {
			t.Error(err)
		}
		done <- result
	}()
	own := <-started
	if own.Err() != nil {
		t.Fatal("the nested call was cancelled before the extension cancelled it")
	}
	if cancelled := w.call(CallExecuteToolCancel, ExecuteToolCancel{ExecuteID: "x3"}, ""); cancelled.Error != nil {
		t.Fatalf("cancel: %+v", cancelled.Error)
	}
	select {
	case slow := <-done:
		var outcome ExecuteToolOutcome
		if err := json.Unmarshal(slow.Result, &outcome); err != nil || !outcome.IsError {
			t.Fatalf("cancelled outcome = %s (%v)", slow.Result, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("executeTool.cancel did not end the call")
	}
	if own.Err() == nil {
		t.Fatal("the action's context was not cancelled")
	}
}

// Upstream runner.ts:966-980: with no executeTool action the call returns an error outcome.
func TestExecuteToolCallWithoutActionReturnsErrorOutcome(t *testing.T) {
	host := newWireHost(t)
	host.SetUIBridge(NewUIBridge(func() {}))
	w := newWireExt(t)
	if _, err := loadWireExt(t, host, w, &RegisterPayload{Name: "runner"}); err != nil {
		t.Fatal(err)
	}
	result := w.call(CallExecuteTool, ExecuteToolArgs{CallerID: "call-9", Name: "echo", Args: json.RawMessage(`{}`), ExecuteID: "x1"}, "")
	var outcome ExecuteToolOutcome
	if result.Error != nil || json.Unmarshal(result.Result, &outcome) != nil {
		t.Fatalf("result = %+v", result)
	}
	if !outcome.IsError || outcome.ToolCall.ID != "call-9/0" || !strings.Contains(string(outcome.Result), "Nested tool calls are not available in this context") {
		t.Fatalf("outcome = %+v (%s)", outcome, outcome.Result)
	}
}

// Upstream types.ts:1708 (getSettings) and 1839 (getMcpServers): these synchronous readers are answered from replicated state. ctx.tools is not: it reads the session when it is read (runner.ts:958-961), so the snapshot carries no callable tools.
func TestSnapshotCarriesSettingsAndMcpServers(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetActions(&HostCallbacks{
		GetSettings: func() extension.Settings { return extension.Settings{"defaultTools": []any{"read"}, "theme": "dark"} },
	})
	state := bridge.Snapshot(nil, 0, false)
	var settings map[string]any
	if err := json.Unmarshal(state.Settings, &settings); err != nil || settings["theme"] != "dark" {
		t.Fatalf("settings = %s (%v)", state.Settings, err)
	}
	if state.McpServers == nil {
		t.Fatal("mcpServers must always be serialized so a snapshot clears prior values")
	}
}

// Upstream tool-definition-wrapper.ts and types.ts AgentToolResult.structuredContent: a subprocess tool's structured content survives the wire into the agent result.
func TestSubprocessToolResultCarriesStructuredContent(t *testing.T) {
	var result ToolResult
	if err := json.Unmarshal([]byte(`{"content":"ok","structured_content":{"n":1,"tags":["a"]}}`), &result); err != nil {
		t.Fatal(err)
	}
	if string(result.StructuredContent) != `{"n":1,"tags":["a"]}` {
		t.Fatalf("structured content = %s", result.StructuredContent)
	}
	host := newWireHost(t)
	w := newWireExt(t)
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != "tool_call" {
			return nil
		}
		return &ResponsePayload{Result: json.RawMessage(`{"content":"ok","structured_content":{"n":1}}`)}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "structured", Tools: []ToolDecl{{Name: "t", Description: "T", Parameters: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ext.Tools["t"].Definition.Execute(t.Context(), "call-1", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, ok := got, true; !ok || string(res.StructuredContent) != `{"n":1}` {
		t.Fatalf("result = %#v", got)
	}
}

// Upstream checks load-time registrations with the runtime's own rules, in registration order: registerMcpServer validates the name and throws inside the factory with the loader's message (loader.ts:456-461), and a virtual model queued while loading is checked only when bindCore flushes it, which reports the failure and keeps the extension loaded (loader.ts:480-488, runner.ts:497-513).
func TestRegisterPayloadChecksRegistrationsWithTheRuntimesRules(t *testing.T) {
	host := newWireHost(t)
	_, err := loadWireExt(t, host, newWireExt(t), &RegisterPayload{Name: "unnamed", McpServers: []McpServerDecl{{Name: "", Config: json.RawMessage(`{"url":"http://x.invalid"}`)}}})
	if err == nil || !strings.Contains(err.Error(), `Invalid MCP server registered by extension "/ext/unnamed.ts": invalid server name ""`) {
		t.Fatalf("empty MCP server name error = %v", err)
	}
	// A repeated name keeps its first position with the last config; an invalid entry fails even when a later one repeats its name.
	if _, err := loadWireExt(t, host, newWireExt(t), &RegisterPayload{Name: "repeat", McpServers: []McpServerDecl{
		mcpDecl("docs", "http://one.invalid"), mcpDecl("wiki", "http://wiki.invalid"), mcpDecl("docs", "http://two.invalid"),
	}}); err != nil {
		t.Fatal(err)
	}
	if got := host.Runtime().McpServers().List(); len(got) != 2 || got[0].Name != "docs" || got[0].Config.URL != "http://two.invalid" || got[1].Name != "wiki" {
		t.Fatalf("servers = %+v", got)
	}
	if _, err := loadWireExt(t, host, newWireExt(t), &RegisterPayload{Name: "late-fix", McpServers: []McpServerDecl{
		{Name: "fixed", Config: json.RawMessage(`{"command":7}`)}, mcpDecl("fixed", "http://ok.invalid"),
	}}); err == nil || !strings.Contains(err.Error(), `Invalid MCP server registered by extension "/ext/late-fix.ts"`) {
		t.Fatalf("an invalid entry followed by a valid one loaded: %v", err)
	}

	if _, err := loadWireExt(t, host, newWireExt(t), &RegisterPayload{Name: "blank-model", VirtualModels: []VirtualModelDecl{{Provider: "router", ID: " ", Name: "Blank"}}}); err != nil {
		t.Fatalf("a virtual model the bind flush rejects failed the load: %v", err)
	}
	var reported []*extension.ExtensionError
	host.Runtime().BindProviderActions(extension.ProviderActions{RegisterVirtualModel: func(definition extension.VirtualModelDefinition) error {
		if strings.TrimSpace(definition.ID) == "" {
			return errors.New("Virtual model provider and id must not be empty.")
		}
		return nil
	}}, func(err *extension.ExtensionError) { reported = append(reported, err) })
	if len(reported) != 1 || reported[0].ExtensionPath != "/ext/blank-model.ts" || reported[0].Event != "register_virtual_model" {
		t.Fatalf("reported = %+v", reported)
	}
}

// startCall sends one extension→host call without waiting; the channel receives its result.
func (w *wireExt) startCall(method string, args any, parentRequestID string) (<-chan *CallResultPayload, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	id := method + "-" + string(rune('a'+w.nextCall.Add(1)))
	ch := make(chan *CallResultPayload, 1)
	w.results.Store(id, ch)
	return ch, w.send(&Envelope{Type: MsgCall, ID: id, Call: &CallPayload{Method: method, Args: raw, ParentRequestID: parentRequestID}})
}

// Upstream types.ts:367-372 and runner.ts:981: the extension's own signal aborts the nested call whenever it fires after the call started. An SDK sends executeTool under the calling tool's request and executeTool.cancel outside it, so the host may run the cancel before the nested call reaches its handler (here the call waits behind an earlier call of the same request); the cancel must still reach the nested call.
func TestExecuteToolCancelSentBeforeTheCallRunsStillCancelsIt(t *testing.T) {
	host := newWireHost(t)
	bridge := NewUIBridge(func() {})
	blocked, release := make(chan struct{}), make(chan struct{})
	started := make(chan context.Context, 1)
	bridge.SetActions(&HostCallbacks{
		SetActiveTools: func([]string) {
			close(blocked)
			<-release
		},
		ExecuteTool: func(ctx context.Context, callerID, name string, _ json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
			started <- ctx
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
			return extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name}, IsError: ctx.Err() != nil}, nil
		},
	})
	host.SetUIBridge(bridge)
	w := newWireExt(t)
	outcome := make(chan error, 1)
	w.handle = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != "tool_call" {
			return nil
		}
		result, err := func() (*CallResultPayload, error) {
			if _, err := w.startCall("setActiveTools", map[string][]string{"tools": {"read"}}, env.ID); err != nil {
				return nil, err
			}
			<-blocked
			nested, err := w.startCall(CallExecuteTool, ExecuteToolArgs{CallerID: env.Request.ToolCallID, Name: "slow", Args: json.RawMessage(`{}`), ExecuteID: "x1"}, env.ID)
			if err != nil {
				return nil, err
			}
			if _, err := w.callErr(CallExecuteToolCancel, ExecuteToolCancel{ExecuteID: "x1"}, ""); err != nil {
				return nil, err
			}
			close(release)
			select {
			case result := <-nested:
				return result, nil
			case <-time.After(20 * time.Second):
				return nil, errors.New("no executeTool result")
			}
		}()
		if err == nil {
			var decoded ExecuteToolOutcome
			if err = json.Unmarshal(result.Result, &decoded); err == nil && !decoded.IsError {
				err = errors.New("the nested call ran to completion although the extension cancelled it")
			}
		}
		outcome <- err
		return &ResponsePayload{Result: json.RawMessage(`{"content":"done"}`)}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "canceller", Tools: []ToolDecl{{Name: "orchestrate", Description: "O", Parameters: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ext.Tools["orchestrate"].Definition.Execute(t.Context(), "call-1", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := <-outcome; err != nil {
		t.Fatal(err)
	}
	if ctx := <-started; ctx.Err() == nil {
		t.Fatal("the nested call's context was not cancelled")
	}
}
