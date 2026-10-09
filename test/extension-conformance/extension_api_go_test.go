package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/extensionapi"
)

// The rows of the Pi 0.99.1 extension API additions (registerMcpServer, registerVirtualModel, getSettings, executeTool and ctx.tools, tool exposure fields, structured results, provider_stream_event, mcp_servers_change, typed provider models) for the Go SDK. Every value a row asserts comes from the host or from the extension's own logic, so an SDK that never makes the call cannot pass by defaulting: the settings are a distinctive object, the router echoes the request it saw, and the nested tools return text only the real tools produce.

const extensionAPIFusedPlacement = "fused"

// extensionAPIHarness is a host with the Go SDK fixture loaded in one placement and bound the way a session binds it.
type extensionAPIHarness struct {
	t      *testing.T
	host   *subprocess.Host
	bridge *subprocess.UIBridge
	ext    *extension.Extension
	runner *inproc.Runner
	// loaded is ext for the host's callbacks, which run on the host's goroutines while the extension loads and after.
	loaded atomic.Pointer[extension.Extension]

	mu sync.Mutex
	// active is the active tool set SetActiveTools installed; nil until then, when every callable tool is active.
	active []string
	// updateRejections are the first update-sink errors of the slow nested calls the host rejected.
	updateRejections []string
	providers        map[string]extension.ProviderConfig
	virtualModels    map[string]extension.VirtualModelDefinition
	nested           map[string]int
	// ownSignal is whether the host marked each nested call (by tool name) as running with its caller's explicit signal.
	ownSignal map[string]bool
	settings  map[string]any
	// waitStarted, waitEnded and release drive the wait_release nested tool: it reports its start, then ends with "Aborted" when its call is cancelled or "released" when the test closes release.
	// hangStarted and hangAborted report that the hang nested tool started and that its call was cancelled.
	hangStarted, hangAborted chan struct{}
	waitStarted              chan struct{}
	waitEnded                chan string
	release                  chan struct{}
}

var extensionAPISettings = map[string]any{"defaultProvider": "router-probe", "fullscreenWheelScrollLines": float64(7), "nested": map[string]any{"list": []any{"a", "b"}}}

func newExtensionAPIHarness(t *testing.T, placement string) *extensionAPIHarness {
	t.Helper()
	root := findModuleRoot(t)
	dir := t.TempDir()
	h := &extensionAPIHarness{t: t, providers: map[string]extension.ProviderConfig{}, virtualModels: map[string]extension.VirtualModelDefinition{}, nested: map[string]int{}, settings: extensionAPISettings, hangStarted: make(chan struct{}, 1), hangAborted: make(chan struct{}, 1), waitStarted: make(chan struct{}, 1), waitEnded: make(chan string, 1), release: make(chan struct{})}
	h.host = subprocess.NewHostWithConfigRoot(dir, t.TempDir())
	t.Cleanup(func() { h.host.Shutdown("test complete") })
	h.bridge = subprocess.NewUIBridge(func() {})
	h.host.SetUIBridge(h.bridge)
	h.bridge.SetActions(&subprocess.HostCallbacks{
		GetSettings:      func() extension.Settings { return h.settings },
		GetCallableTools: h.callableTools,
		ExecuteTool:      h.executeTool,
		SetActiveTools: func(names []string) {
			h.mu.Lock()
			h.active = slices.Clone(names)
			h.mu.Unlock()
		},
	})
	// A virtual model an earlier-loaded extension queued before the runner binds; the fixture unregisters it (loader.ts:228-232).
	var peer extension.API = mcpRegistryAPI{runtime: h.host.Runtime(), extensionPath: "/ext/peer.go"}
	peer.RegisterVirtualModel(extension.ExtensionVirtualModel{Provider: "router", ID: "victim", Name: "Victim", Route: func(context.Context, extension.ModelRouteRequest) (extension.ModelRoute, error) {
		return extension.ModelRoute{}, nil
	}})
	h.host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
		h.mu.Lock()
		h.providers[name] = config
		h.mu.Unlock()
		return nil
	}, func(string) {})

	switch placement {
	case extensionAPIFusedPlacement:
		ext, err := h.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: extensionapi.Name, Path: "/ext/" + extensionapi.Name + ".go", Enabled: true}, fixtureFactory(t).RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		h.ext = ext
	default:
		cfg := subprocess.ExtConfig{
			Name: extensionapi.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/extension-api-go"), Enabled: true, Isolation: "strict",
			RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
			SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/extension-api-go", Package: "example.com/extension-api-go",
		}
		configs := []subprocess.ExtConfig{cfg}
		if placement == "packed" {
			cfg.Isolation = "shared-ok"
			configs = []subprocess.ExtConfig{cfg, providerPeer(t, root, cfg)}
			packed := false
			for _, cell := range subprocess.PlanCells(configs, nil) {
				packed = packed || len(cell.Extensions) == 2
			}
			if !packed {
				t.Fatal("the fixture was not packed with its peer")
			}
		}
		loaded, failures := h.host.LoadAll(t.Context(), configs)
		if len(failures) != 0 || len(loaded) != len(configs) {
			t.Fatalf("load: %v", failures)
		}
		h.ext = &loaded[0]
	}
	h.loaded.Store(h.ext)
	h.runner = inproc.NewRunner([]extension.Extension{*h.ext}, dir, h.host.Runtime())
	// The runner delivers mcp_servers_change on its own goroutine, so an event can still be in flight when the test ends and the host shuts the extension down; that failure is not the row's.
	var closing atomic.Bool
	t.Cleanup(func() { closing.Store(true) })
	h.runner.AddErrorListener(func(err *extension.ExtensionError) {
		if !closing.Load() {
			t.Errorf("extension error: %+v", err)
		}
	})
	h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, &extension.ProviderActions{
		RegisterProvider:   func(string, extension.ProviderConfig) error { return nil },
		UnregisterProvider: func(string) {},
		RegisterVirtualModel: func(definition extension.VirtualModelDefinition) error {
			h.mu.Lock()
			h.virtualModels[definition.Provider+"/"+definition.ID] = definition
			h.mu.Unlock()
			return nil
		},
		UnregisterVirtualModel: func(provider, id string) {
			h.mu.Lock()
			delete(h.virtualModels, provider+"/"+id)
			h.mu.Unlock()
		},
	})
	return h
}

// fixtureFactory builds the fixture extension. A factory that panics, as upstream's factory that throws, fails the test that loads it.
func fixtureFactory(t *testing.T) *sdk.Extension {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("the fixture factory failed: %v", recovered)
		}
	}()
	return extensionapi.Extension()
}

func extensionAPIPlacements() []string {
	return []string{extensionAPIFusedPlacement, "strict", "packed"}
}

// callable mirrors upstream's callable set (types.ts:496-509): direct, codemode and deferred tools, never model-only or hidden.
func (h *extensionAPIHarness) callable(name string) (extension.RegisteredTool, bool) {
	loaded := h.loaded.Load()
	if loaded == nil {
		return extension.RegisteredTool{}, false
	}
	tool, ok := loaded.Tools[name]
	if !ok {
		return tool, false
	}
	switch tool.Definition.Exposure {
	case extension.ToolExposureModelOnly, extension.ToolExposureHidden:
		return tool, false
	}
	return tool, true
}

func (h *extensionAPIHarness) callableTools() []extension.AgentTool {
	var tools []extension.AgentTool
	loaded := h.loaded.Load()
	if loaded == nil {
		return tools
	}
	h.mu.Lock()
	active := h.active
	h.mu.Unlock()
	for _, name := range loaded.ToolOrder {
		if active != nil && !slices.Contains(active, name) {
			continue
		}
		if tool, ok := h.callable(name); ok {
			def := tool.Definition
			tools = append(tools, extension.AgentTool{Name: def.Name, Label: def.Label, Description: def.Description, Parameters: def.Parameters, OutputSchema: def.OutputSchema})
		}
	}
	return tools
}

// executeTool stands in for the session's nested-call action: it numbers the call `<calling id>/<n>` and runs the extension's own tools over the wire, so the nested tool's text is the real tool's.
func (h *extensionAPIHarness) executeTool(ctx context.Context, callerID, name string, args json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
	h.mu.Lock()
	if h.ownSignal == nil {
		h.ownSignal = map[string]bool{}
	}
	h.ownSignal[name] = extension.OwnsSignal(ctx)
	h.nested[callerID]++
	id := fmt.Sprintf("%s/%d", callerID, h.nested[callerID])
	h.mu.Unlock()
	outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: id, Name: name, Arguments: ai.JsonObject{}}}
	textResult := func(text string, isError bool) (extension.AgentToolCallOutcome, error) {
		outcome.Result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: map[string]any{}, IsError: isError}
		outcome.IsError = isError
		return outcome, nil
	}
	switch name {
	case "slow":
		// Pi's runToolCall rejects with the first update sink error after the tool returned, and never reaches afterToolCall or tool_execution_end for that call (agent-loop.ts:820-849; nested-tool-calls.ts:219-248).
		update := options.OnUpdate
		var first error
		for _, step := range []string{"one", "two"} {
			if update != nil {
				if err := update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: step}}, Details: map[string]any{"step": step}}); err != nil && first == nil {
					first = err
				}
			}
		}
		if first != nil {
			h.mu.Lock()
			h.updateRejections = append(h.updateRejections, first.Error())
			h.mu.Unlock()
			return extension.AgentToolCallOutcome{}, first
		}
		return textResult("done", false)
	case "timed":
		// A value no SDK defaults to: an SDK that drops the outcome's durationMs reports none (types.ts:454).
		outcome.DurationMs = new(int64(4321))
		return textResult("timed", false)
	case "hang":
		h.hangStarted <- struct{}{}
		<-ctx.Done()
		h.hangAborted <- struct{}{}
		return textResult("Aborted", true)
	case "wait_release":
		h.waitStarted <- struct{}{}
		select {
		case <-ctx.Done():
			h.waitEnded <- "Aborted"
			return textResult("Aborted", true)
		case <-h.release:
			h.waitEnded <- "released"
			return textResult("released", false)
		}
	}
	tool, ok := h.callable(name)
	if !ok {
		return textResult("Tool "+name+" not found", true)
	}
	result, err := tool.Definition.Execute(ctx, id, args, nil)
	if err != nil {
		return textResult(err.Error(), true)
	}
	outcome.Result = result
	if typed, ok := result, true; ok {
		outcome.IsError = typed.IsError
	}
	return outcome, nil
}

// run executes one of the fixture's tools as tool call callID and returns its result.
func (h *extensionAPIHarness) run(ctx context.Context, name, callID string, args string) (agent.AgentToolResult, error) {
	h.t.Helper()
	tool, ok := h.ext.Tools[name]
	if !ok {
		h.t.Fatalf("the fixture has no tool %q (tools: %v)", name, h.ext.ToolOrder)
	}
	result, err := tool.Definition.Execute(ctx, callID, json.RawMessage(args), nil)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	typed, ok := result, true
	if !ok {
		h.t.Fatalf("tool %s returned %T", name, result)
	}
	return typed, nil
}

func (h *extensionAPIHarness) probe() map[string]any {
	h.t.Helper()
	result, err := h.run(h.t.Context(), "probe_state", "probe", `{}`)
	if err != nil {
		h.t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(result.Text()), &report); err != nil {
		h.t.Fatalf("probe_state = %q: %v", result.Text(), err)
	}
	return report
}

// Upstream loader.ts:456-497 and types.ts:579-607: what a factory registers while it runs reaches the host when the extension loads. The MCP server keeps its header order and its owner is the extension's path; the tool exposure fields arrive with the tool; the virtual model is queued until the runner binds.
func TestExtensionAPIRegistrationsAtLoadGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			servers := h.host.Runtime().McpServers().List()
			if len(servers) != 1 || servers[0].Name != "docs" || servers[0].ExtensionPath != h.ext.Path || servers[0].Config.URL != "http://docs.invalid/mcp" || servers[0].Config.Exposure != extension.McpExposureDeferred {
				t.Fatalf("mcp servers = %+v (extension path %q)", servers, h.ext.Path)
			}
			if servers[0].Config.Description != "Docs search" || servers[0].Config.OAuth == nil || servers[0].Config.OAuth.ClientName != "Conformance Client" {
				t.Fatalf("description and oauth.clientName did not reach the host: %+v", servers[0].Config)
			}
			if got := servers[0].Config.Headers.Keys(); !reflect.DeepEqual(got, []string{"X-Zed", "X-Alpha"}) {
				t.Fatalf("header order = %v", got)
			}
			if got := servers[0].Config.ToolExposure.Keys(); !reflect.DeepEqual(got, []string{"search_*", "*"}) {
				t.Fatalf("toolExposure order = %v", got)
			}

			helper := h.ext.Tools["helper"].Definition
			if helper.Exposure != extension.ToolExposureCodemode || helper.Namespace == nil || helper.Namespace.Name != "helpers" || helper.Namespace.Description != "Helper tools" || helper.Namespace.Instructions != "Call helper first." ||
				helper.Annotations == nil || helper.Annotations.ReadOnlyHint == nil || !*helper.Annotations.ReadOnlyHint || helper.Annotations.OpenWorldHint == nil || *helper.Annotations.OpenWorldHint ||
				!strings.Contains(string(helper.OutputSchema), `"n"`) {
				t.Fatalf("helper definition = %+v", helper)
			}
			if soft := h.ext.Tools["soft_fail"].Definition; soft.DefaultActive == nil || *soft.DefaultActive {
				t.Fatalf("soft_fail defaultActive = %v", soft.DefaultActive)
			}
			if echo := h.ext.Tools["echo"].Definition; echo.Exposure != "" || echo.DefaultActive != nil || echo.PrepareLoadout != nil {
				t.Fatalf("echo declares fields it never set: %+v", echo)
			}
			if h.ext.Tools["run_tools"].Definition.Exposure != extension.ToolExposureModelOnly || h.ext.Tools["run_tools"].Definition.PrepareLoadout == nil {
				t.Fatalf("run_tools = %+v", h.ext.Tools["run_tools"].Definition)
			}

			h.mu.Lock()
			auto, ok := h.virtualModels["router/auto"]
			h.mu.Unlock()
			if !ok || auto.Name != "Auto" || auto.ContextWindow != 200000 || auto.MaxTokens != 8192 || !reflect.DeepEqual(auto.ThinkingLevels, []ai.ModelThinkingLevel{"off", "high"}) {
				t.Fatalf("virtual model = %+v (registered: %v)", auto, ok)
			}
		})
	}
}

// mcpRegistryAPI is the Go reference for the MCP server and virtual model members of extension.API: the host runtime's registries behind
// registerMcpServer, unregisterMcpServer, getMcpServers, registerVirtualModel and unregisterVirtualModel, as one extension (extensionPath) sees it.
type mcpRegistryAPI struct {
	extension.API
	runtime       *extension.ExtensionRuntime
	extensionPath string
}

func (a mcpRegistryAPI) RegisterMcpServer(name string, config extension.McpServerConfig) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return a.runtime.RegisterMcpServer(a.extensionPath, name, raw)
}

func (a mcpRegistryAPI) UnregisterMcpServer(name string) {
	a.runtime.UnregisterMcpServer(a.extensionPath, name)
}

func (a mcpRegistryAPI) RegisterVirtualModel(model extension.ExtensionVirtualModel) {
	if err := a.runtime.RegisterVirtualModel(model, a.extensionPath); err != nil {
		panic(err)
	}
}

func (a mcpRegistryAPI) UnregisterVirtualModel(provider, id string) {
	a.runtime.UnregisterVirtualModel(provider, id)
}

func (a mcpRegistryAPI) GetMcpServers() []extension.RegisteredMcpServer {
	return a.runtime.McpServers().List()
}

// Upstream types.ts:1708 (getSettings), 1839 (getMcpServers) and 385 (tools): the extension reads them synchronously, from state the host replicated. Registering an MCP server after load reaches the runtime's registry, a name another extension owns is refused with upstream's message, and unregistering removes only the extension's own.
func TestExtensionAPIReplicatedStateAndLateRegistrationGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			report := h.probe()
			if !reflect.DeepEqual(report["settings"], any(extensionAPISettings)) {
				t.Fatalf("settings = %#v, want %#v (error %v)", report["settings"], extensionAPISettings, report["settingsError"])
			}
			wantServers := []any{"docs@" + h.ext.Path}
			if !reflect.DeepEqual(report["mcpServers"], wantServers) {
				t.Fatalf("mcpServers = %#v, want %v", report["mcpServers"], wantServers)
			}
			// executeTool and ctx.tools: the callable tools are the host's, in the host's order.
			if want := []any{"echo", "helper", "soft_fail", "nested_updates", "nested_duration", "nested_cancel", "nested_own_signal", "probe_state", "live_tools", "nested_throw", "detached_call", "register_late", "unregister_late"}; !reflect.DeepEqual(report["callableTools"], want) {
				t.Fatalf("callableTools = %#v, want %v", report["callableTools"], want)
			}

			// Another extension's registration: refused with upstream's message.
			var other extension.API = mcpRegistryAPI{runtime: h.host.Runtime(), extensionPath: "/ext/other.ts"}
			var api extension.API = mcpRegistryAPI{runtime: h.host.Runtime(), extensionPath: h.ext.Path}
			if err := other.RegisterMcpServer("taken", extension.McpServerConfig{URL: "http://taken.invalid"}); err != nil {
				t.Fatal(err)
			}
			refused, err := h.run(t.Context(), "register_late", "c1", `{"mcp":"taken"}`)
			if err != nil {
				t.Fatal(err)
			}
			if want := `register failed: MCP server "taken" is already registered by extension "/ext/other.ts"`; refused.Text() != want {
				t.Fatalf("foreign registration = %q, want %q", refused.Text(), want)
			}

			// mcp-servers.ts:151-166,192-196 and loader.ts:479 (0.99.2): the server registered with the codemode-deferred alias reads back through GetMcpServers with the exposure the alias stands for.
			registered, err := h.run(t.Context(), "register_late", "c2", `{"mcp":"late"}`)
			if err != nil || registered.Text() != "registered late sees docs+taken+late exposed codemode/codemode" {
				t.Fatalf("register_late = %q, %v", registered.Text(), err)
			}
			names := []string{}
			for _, server := range api.GetMcpServers() {
				names = append(names, server.Name)
			}
			if want := []string{"docs", "taken", "late"}; !reflect.DeepEqual(names, want) {
				t.Fatalf("registry = %v, want %v", names, want)
			}
			late, _ := h.host.Runtime().McpServers().List()[2], 0
			if late.ExtensionPath != h.ext.Path || late.Config.Command != "late-server" || !reflect.DeepEqual(late.Config.Env.Keys(), []string{"B", "A"}) {
				t.Fatalf("late server = %+v", late)
			}
			h.mu.Lock()
			_, hasLate := h.virtualModels["router/late"]
			h.mu.Unlock()
			if !hasLate {
				t.Fatal("the late virtual model did not reach the host")
			}
			// The extension's next read sees its own change and the other extension's server.
			if got := h.probe()["mcpServers"]; !reflect.DeepEqual(got, []any{"docs@" + h.ext.Path, "taken@/ext/other.ts", "late@" + h.ext.Path}) {
				t.Fatalf("mcpServers after registration = %#v", got)
			}
			if _, err := h.run(t.Context(), "unregister_late", "c3", `{"mcp":"late"}`); err != nil {
				t.Fatal(err)
			}
			if got := h.probe()["mcpServers"]; !reflect.DeepEqual(got, []any{"docs@" + h.ext.Path, "taken@/ext/other.ts"}) {
				t.Fatalf("mcpServers after unregister = %#v", got)
			}
			// The reference registers and unregisters a virtual model through the API; the host sees both (loader.ts:228-232, types.ts registerVirtualModel).
			other.RegisterVirtualModel(extension.ExtensionVirtualModel{Provider: "router", ID: "ref", Name: "Ref", Route: func(context.Context, extension.ModelRouteRequest) (extension.ModelRoute, error) {
				return extension.ModelRoute{}, nil
			}})
			h.mu.Lock()
			_, hasRef := h.virtualModels["router/ref"]
			h.mu.Unlock()
			if !hasRef {
				t.Fatal("the virtual model the reference registered did not reach the host")
			}
			other.UnregisterVirtualModel("router", "ref")
			h.mu.Lock()
			_, hasRef = h.virtualModels["router/ref"]
			h.mu.Unlock()
			if hasRef {
				t.Fatal("the virtual model the reference unregistered is still at the host")
			}
			// The reference's own unregistration removes only the server its extension registered.
			other.UnregisterMcpServer("taken")
			names = names[:0]
			for _, server := range api.GetMcpServers() {
				names = append(names, server.Name)
			}
			if want := []string{"docs"}; !reflect.DeepEqual(names, want) {
				t.Fatalf("registry after the reference unregistered taken = %v, want %v", names, want)
			}
			h.mu.Lock()
			_, hasLate = h.virtualModels["router/late"]
			h.mu.Unlock()
			if hasLate {
				t.Fatal("the late virtual model survived unregistration")
			}
		})
	}
}

// pi.registerVirtualModel (packages/coding-agent/src/core/extensions/types.ts:1872, loader.ts:500-509) and virtual-models.ts:55-85: the host's route request runs the extension's router. The route's state echoes the request it saw, so the values are the request's, and the extension returns the routed model, level and state.
func TestExtensionAPIVirtualModelRouteRunsInTheExtensionGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			h.mu.Lock()
			auto := h.virtualModels["router/auto"]
			h.mu.Unlock()
			if auto.Route == nil {
				t.Fatal("registerVirtualModel: router/auto has no route")
			}
			model := &ai.Model{ID: "auto", ProviderMeta: ai.ProviderMetadata{ProviderID: "router"}}
			route, err := auto.Route(t.Context(), extension.ModelRouteRequest{
				Model: model, ThinkingLevel: "high", Reason: extension.ModelRouteReasonUser,
				Previous: &extension.ModelRoutePrevious{Model: &ai.Model{ID: "sonnet", ProviderMeta: ai.ProviderMetadata{ProviderID: "anthropic"}}},
				State:    json.RawMessage(`{"turns":4}`),
				Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}, ai.UserMessage{Content: ai.UserText("again")}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if route.Model == nil || route.Model.ID != "opus" || route.ThinkingLevel != "high" {
				t.Fatalf("route = %+v", route)
			}
			var state map[string]any
			if err := json.Unmarshal(route.State, &state); err != nil {
				t.Fatalf("state %s: %v", route.State, err)
			}
			want := map[string]any{"turns": float64(5), "reason": "user", "messages": float64(2), "previous": "sonnet", "selected": "auto", "level": "high"}
			if !reflect.DeepEqual(state, want) {
				t.Fatalf("state = %#v, want %#v", state, want)
			}
			retry, err := auto.Route(t.Context(), extension.ModelRouteRequest{Model: model, ThinkingLevel: "off", Reason: extension.ModelRouteReasonRetry})
			if err != nil || retry.Model == nil || retry.Model.ID != "haiku" || retry.ThinkingLevel != "off" {
				t.Fatalf("retry route = %+v, %v", retry, err)
			}
		})
	}
}

// Upstream agent-session-tool-orchestration.test.ts:41-50, 76-84 and 91: the orchestrator tool calls a hidden helper, a direct tool and itself; the nested calls are `<parent>/<n>`, the model-only tool is not callable, and the tool's text is the upstream expectation. prepareLoadout answers upstream's descriptions and hidden declarations from the loadout it is given.
func TestExtensionAPIExecuteToolOrchestrationGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			result, err := h.run(t.Context(), "run_tools", "call-1", `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if want := "helped | echo: hi | Tool run_tools not found"; result.Text() != want {
				t.Fatalf("run_tools = %q, want %q", result.Text(), want)
			}
			h.mu.Lock()
			count := h.nested["call-1"]
			h.mu.Unlock()
			if count != 3 {
				t.Fatalf("nested calls of call-1 = %d, want 3", count)
			}

			// types.ts:541-563: the loadout the host builds and the changes the extension returns.
			echo := extension.AgentTool{Name: "echo", Label: "echo", Description: "Echo text.", Parameters: json.RawMessage(`{"type":"object"}`)}
			helper := extension.AgentTool{Name: "helper", Label: "helper", Description: "Only reachable from other tools.", Parameters: json.RawMessage(`{"type":"object"}`)}
			runTools := extension.AgentTool{Name: "run_tools", Label: "run_tools", Description: "Runs tools.", Parameters: json.RawMessage(`{"type":"object"}`)}
			changes := h.ext.Tools["run_tools"].Definition.PrepareLoadout(extension.ToolLoadout{
				Declared: []extension.AgentTool{echo, runTools}, Callable: []extension.AgentTool{echo, helper}, Registered: []extension.AgentTool{echo, helper, runTools},
				GetExposure:  func(string) extension.ToolExposure { return extension.ToolExposureDirect },
				GetNamespace: func(string) *extension.ToolNamespace { return nil },
				GetPromptGuidelines: func(name string) []string {
					if name == "echo" {
						return []string{"Echo with care.", "Quote the text."}
					}
					return nil
				},
			})
			wantChanges := &extension.ToolLoadoutChanges{
				Descriptions:       map[string]string{"run_tools": "Runs tools: echo, helper [Echo with care. | Quote the text.]", "echo": "Echo text (also callable from run_tools)."},
				HiddenDeclarations: []string{"echo"},
			}
			if !reflect.DeepEqual(changes, wantChanges) {
				t.Fatalf("changes = %+v, want %+v", changes, wantChanges)
			}
		})
	}
}

// Upstream types.ts:454 and nested-tool-calls.ts:246: executeTool's outcome carries durationMs when the tool ran, and none otherwise.
func TestExtensionAPIExecuteToolOutcomeDurationGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			result, err := h.run(t.Context(), "nested_duration", "call-d", `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if result.Text() != "4321|none" {
				t.Fatalf("nested_duration = %q, want the host's 4321 and none for an outcome without one", result.Text())
			}
		})
	}
}

// Upstream types.ts:367-372 (onUpdate) and runner.ts:966-983: the nested tool's partial results reach the callback in order, before the outcome.
func TestExtensionAPIExecuteToolUpdatesAndCancellationGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			result, err := h.run(t.Context(), "nested_updates", "call-u", `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if result.Text() != "one,two,done" {
				t.Fatalf("nested_updates = %q, want the partial results in order and then the outcome", result.Text())
			}

			// runner.ts:981: the calling tool's cancellation is the nested call's default signal.
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan string, 1)
			go func() {
				out, err := h.run(ctx, "nested_cancel", "call-x", `{}`)
				if err != nil {
					done <- "error: " + err.Error()
					return
				}
				done <- out.Text()
			}()
			select {
			case <-h.hangStarted:
			case <-time.After(20 * time.Second):
				t.Fatal("the nested call did not start")
			}
			// runner.ts:981: without a signal option the nested call runs with the calling tool's signal, so it is not marked.
			h.mu.Lock()
			own := h.ownSignal["hang"]
			h.mu.Unlock()
			if own {
				t.Error("a nested call without a signal option was marked as running with its own")
			}
			cancel()
			select {
			case <-h.hangAborted:
			case <-time.After(20 * time.Second):
				t.Fatal("cancelling the calling request did not cancel the nested call")
			}
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the calling tool did not end")
			}
		})
	}
}

// Upstream runner.ts:980 (`signal: options.signal ?? signal`): a nested call given its own signal is not cancelled with the calling tool's request; it runs until it ends on its own.
func TestExtensionAPIExecuteToolOwnSignalOutlivesTheCallingRequestGo(t *testing.T) {
	t.Parallel()
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() { _, _ = h.run(ctx, "nested_own_signal", "call-o", `{}`) }()
			select {
			case <-h.waitStarted:
			case <-time.After(20 * time.Second):
				t.Fatal("the nested call did not start")
			}
			// runner.ts:979-981: the nested tool runs with the signal its caller passed, which the host marks for the tool's runtime.
			h.mu.Lock()
			own := h.ownSignal["wait_release"]
			h.mu.Unlock()
			if !own {
				t.Error("the nested call with an explicit signal was not marked as running with it")
			}
			cancel()
			// The host cancels the calls tied to a cancelled request at once; this one must still be running.
			select {
			case got := <-h.waitEnded:
				t.Fatalf("the nested call ended with the calling request: %s", got)
			case <-time.After(500 * time.Millisecond):
			}
			close(h.release)
			select {
			case got := <-h.waitEnded:
				if got != "released" {
					t.Fatalf("the nested call ended with %q, want it released", got)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the nested call did not end")
			}
		})
	}
}

// Upstream types.ts:424-446 (structuredContent, isError): the machine-readable result and the soft error flag come back on the tool result the host builds, and the model-facing content stays the text.
func TestExtensionAPIStructuredResultsGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			helped, err := h.run(t.Context(), "helper", "call-s", `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if helped.IsError || helped.Text() != "helped" || string(helped.StructuredContent) != `{"n":3}` {
				t.Fatalf("helper result = %+v", helped)
			}
			soft, err := h.run(t.Context(), "soft_fail", "call-f", `{}`)
			softDetails, _ := orderedjson.Map(soft.Details)
			if err != nil {
				t.Fatalf("a soft failure must not be a thrown error: %v", err)
			}
			if !soft.IsError || soft.Text() != "soft failure" || string(soft.StructuredContent) != `{"n":0}` || !reflect.DeepEqual(softDetails, map[string]any{"kept": true}) {
				t.Fatalf("soft_fail result = %+v", soft)
			}
		})
	}
}

// Upstream types.ts:699 (mcp_servers_change) and 884 (provider_stream_event): the runner delivers each to the extension's handler with upstream's payload. A registration after the runner bound fires mcp_servers_change with every registered server.
func TestExtensionAPIEventsGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			if !h.runner.HasHandlers("mcp_servers_change") || !h.runner.HasHandlers("provider_stream_event") {
				t.Fatal("the extension's handlers were not registered under the upstream event names")
			}
			if _, err := h.runner.Emit(t.Context(), extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "openai", API: "openai-responses", Model: "gpt-x", Data: map[string]any{"type": "response.created"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.run(t.Context(), "register_late", "c1", `{"mcp":"late"}`); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			var report map[string]any
			for {
				// The runner delivers mcp_servers_change on its own goroutine after the registration returned.
				if report = h.probe(); len(report["mcpChanges"].([]any)) > 0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !reflect.DeepEqual(report["streamEvents"], []any{"openai/openai-responses/gpt-x:response.created"}) {
				t.Fatalf("streamEvents = %#v", report["streamEvents"])
			}
			if !reflect.DeepEqual(report["mcpChanges"], []any{"docs+late"}) {
				t.Fatalf("mcpChanges = %#v, want the servers after the change", report["mcpChanges"])
			}
		})
	}
}

// Upstream types.ts:1929-1991 (ProviderChatModelConfig, ProviderImageModelConfig, ProviderClassifierModelConfig): a provider registered with the three model types reaches the host with each entry's discriminator and variant fields.
func TestExtensionAPIProviderModelTypesGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			h.mu.Lock()
			config, ok := h.providers["multi"]
			h.mu.Unlock()
			if !ok || len(config.Models) != 3 {
				t.Fatalf("provider config = %+v (registered: %v)", config, ok)
			}
			chat, image, classifier := config.Models[0], config.Models[1], config.Models[2]
			if chat.ID != "chat-1" || (chat.Type != "" && chat.Type != ai.ModelTypeChat) || chat.ContextWindow != 128000 || chat.MaxTokens != 4096 || !chat.Reasoning {
				t.Fatalf("chat entry = %+v", chat)
			}
			if image.ID != "img-1" || image.Type != ai.ModelTypeImage || image.API != "openrouter-images" || !reflect.DeepEqual(image.Output, []string{"image", "text"}) {
				t.Fatalf("image entry = %+v", image)
			}
			if classifier.ID != "cls-1" || classifier.Type != ai.ModelTypeClassifier || classifier.API != "llama-cpp-classify" || classifier.ContextWindow != 8192 {
				t.Fatalf("classifier entry = %+v", classifier)
			}
		})
	}
}

// Upstream types.ts:1044-1219 (parentToolCallId, structuredContent on tool_call and tool_result): the events of a nested call carry the parent's id to the extension's handlers, tool_result carries the structured content, and a handler's replacement structured content is the result the runner returns.
func TestExtensionAPIToolEventsCarryParentAndStructuredContentGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			if _, err := h.runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{
				ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "call-1/1", ParentToolCallID: "call-1"}, ToolName: "helper", Input: map[string]any{},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{
				ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "call-2"}, ToolName: "echo", Input: map[string]any{},
			}); err != nil {
				t.Fatal(err)
			}
			result, err := h.runner.EmitToolResult(t.Context(), extension.CustomToolResultEvent{
				ToolResultEventBase: extension.ToolResultEventBase{
					Type: "tool_result", ToolCallID: "call-1/1", ParentToolCallID: "call-1", Input: map[string]any{},
					Content: []any{map[string]any{"type": "text", "text": "helped"}}, StructuredContent: json.RawMessage(`{"n":3}`),
				},
				ToolName: "helper",
			})
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || string(result.StructuredContent) != `{"n":4}` {
				t.Fatalf("tool_result result = %+v, want the handler's structuredContent", result)
			}
			want := []any{"tool_call helper call-1/1<-call-1", "tool_call echo call-2<-<nil>", "tool_result call-1/1<-call-1 {\"n\":3}"}
			if got := h.probe()["toolEvents"]; !reflect.DeepEqual(got, want) {
				t.Fatalf("toolEvents = %#v, want %#v", got, want)
			}
		})
	}
}
