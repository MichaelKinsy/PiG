package extensionconformance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Rows for the Pi 0.99.1 extension API additions (types.ts, runner.ts, loader.ts, virtual-models.ts) as the Rust SDK realizes them,
// run against the real subprocess Host in isolated and packed mode. Every asserted value comes from the host callbacks below or from
// the fixture's own logic (testdata/extapi-rust), so an SDK that never makes the call cannot pass by defaulting: a constant
// getSettings, an empty getMcpServers, or an executeTool that answers locally would all fail.

const (
	rustAPISettingsMarker = "rust-api-settings-marker"
	rustAPICallableTool   = "conf-callable"
)

type rustAPIHost struct {
	host *subprocess.Host
	ext  extension.Extension

	mu        sync.Mutex
	executed  []string
	providers map[string]extension.ProviderConfig
	// active is the active tool set SetActiveTools installed; nil until then.
	active []string
	// updateRejections are the first update-sink errors of the nested calls the host rejected.
	updateRejections []string
	// waitStarted, waitEnded and release drive the wait_release nested tool: it reports its start, then ends with "Aborted" when its call is cancelled or "released" when the test closes release.
	waitStarted chan struct{}
	waitEnded   chan string
	release     chan struct{}
}

func loadRustAPIExtension(t *testing.T, packed bool) *rustAPIHost {
	t.Helper()
	h := &rustAPIHost{waitStarted: make(chan struct{}, 1), waitEnded: make(chan string, 1), release: make(chan struct{})}
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetActions(&subprocess.HostCallbacks{
		GetSettings: func() extension.Settings {
			return extension.Settings{"marker": rustAPISettingsMarker, "fullscreenWheelScrollLines": float64(7)}
		},
		GetCallableTools: func() []extension.AgentTool {
			h.mu.Lock()
			active := h.active
			h.mu.Unlock()
			tools := []extension.AgentTool{{Name: rustAPICallableTool, Label: "Callable", Description: "Callable from other tools", Parameters: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "only", Label: "Only", Parameters: json.RawMessage(`{"type":"object"}`)}}
			if active == nil {
				return tools
			}
			var kept []extension.AgentTool
			for _, tool := range tools {
				if slices.Contains(active, tool.Name) {
					kept = append(kept, tool)
				}
			}
			return kept
		},
		SetActiveTools: func(names []string) {
			h.mu.Lock()
			h.active = slices.Clone(names)
			h.mu.Unlock()
		},
		ExecuteTool: func(ctx context.Context, callerID, name string, args json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
			if name == "wait_release" {
				outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}}
				h.waitStarted <- struct{}{}
				select {
				case <-ctx.Done():
					h.waitEnded <- "Aborted"
					outcome.IsError = true
				case <-h.release:
					h.waitEnded <- "released"
					outcome.Result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "released"}}}
				}
				return outcome, nil
			}
			h.mu.Lock()
			h.executed = append(h.executed, callerID+"|"+name+"|"+string(args))
			h.mu.Unlock()
			// Pi's runToolCall rejects with the first update sink error after the tool returned (agent-loop.ts:820-849).
			if update := options.OnUpdate; update != nil {
				var first error
				for _, step := range []string{"partial-one", "partial-two"} {
					if err := update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: step}}}); err != nil && first == nil {
						first = err
					}
				}
				if first != nil {
					h.mu.Lock()
					h.updateRejections = append(h.updateRejections, first.Error())
					h.mu.Unlock()
					return extension.AgentToolCallOutcome{}, first
				}
			}
			return extension.AgentToolCallOutcome{
				// A value no SDK defaults to (types.ts:454): an SDK that drops durationMs reports none.
				DurationMs: new(int64(4321)),
				ToolCall:   ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{"q": "conformance"}},
				Result: agent.AgentToolResult{
					Content:           []ai.ToolResultMessageContent{ai.TextContent{Text: "nested-ok"}},
					Details:           map[string]any{"n": float64(1)},
					StructuredContent: json.RawMessage(`{"answer":42}`),
				},
			}, nil
		},
	})
	h.host = subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { h.host.Shutdown("test done") })
	h.host.SetUIBridge(bridge)
	// A virtual model an earlier-loaded extension queued before the runner binds; the fixture unregisters it (loader.ts:228-232).
	if err := h.host.Runtime().RegisterVirtualModel(extension.VirtualModelDefinition{Provider: "conformance", ID: "victim", Name: "Victim", Route: func(context.Context, extension.ModelRouteRequest) (extension.ModelRoute, error) {
		return extension.ModelRoute{}, nil
	}}, "/ext/peer.rs"); err != nil {
		t.Fatal(err)
	}
	h.providers = map[string]extension.ProviderConfig{}
	h.host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.providers[name] = config
		return nil
	}, func(string) {})

	source, err := filepath.Abs("testdata/extapi-rust")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := subprocess.ResolveExtConfigWithIdentity(source, "extapi")
	if err != nil {
		t.Fatal(err)
	}
	cfgs := []subprocess.ExtConfig{cfg}
	if packed {
		sibling, err := filepath.Abs("testdata/provider-rust-sibling")
		if err != nil {
			t.Fatal(err)
		}
		siblingCfg, _, err := subprocess.ResolveExtConfigWithIdentity(sibling, "provider-sibling")
		if err != nil {
			t.Fatal(err)
		}
		cfgs = append(cfgs, siblingCfg)
	} else {
		cfgs[0].Isolation = "isolated"
	}
	loaded, loadErrors := h.host.LoadAll(t.Context(), cfgs)
	if len(loadErrors) > 0 {
		t.Fatal(loadErrors[0])
	}
	for _, ext := range loaded {
		if ext.Name == "extapi" {
			h.ext = ext
		}
	}
	if h.ext.Name == "" {
		t.Fatalf("extapi not loaded: %v", loaded)
	}
	return h
}

func (h *rustAPIHost) execute(t *testing.T, tool, toolCallID string) agent.AgentToolResult {
	t.Helper()
	registered, ok := h.ext.Tools[tool]
	if !ok {
		t.Fatalf("tool %q not registered", tool)
	}
	result, err := registered.Definition.Execute(t.Context(), toolCallID, json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	typed, ok := result, true
	if !ok {
		t.Fatalf("%s result type %T", tool, result)
	}
	return typed
}

func (h *rustAPIHost) details(t *testing.T, tool, toolCallID string) map[string]any {
	t.Helper()
	result := h.execute(t, tool, toolCallID)
	details, ok := result.Details.(map[string]any)
	if !ok {
		encoded, err := json.Marshal(result.Details)
		if err != nil {
			t.Fatal(err)
		}
		details = map[string]any{}
		if err := json.Unmarshal(encoded, &details); err != nil {
			t.Fatalf("%s details %s: %v", tool, encoded, err)
		}
	}
	return details
}

func forEachRustAPIMode(t *testing.T, run func(t *testing.T, h *rustAPIHost)) {
	for _, mode := range []struct {
		name   string
		packed bool
	}{{"isolated", false}, {"packed", true}} {
		t.Run(mode.name, func(t *testing.T) { run(t, loadRustAPIExtension(t, mode.packed)) })
	}
}

// types.ts:582-607: outputSchema, exposure, namespace, annotations, defaultActive and prepareLoadout reach the host's tool definition.
func TestRustSDKToolDefinitionFields(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		lookup := h.ext.Tools["lookup"].Definition
		if string(lookup.OutputSchema) != `{"type":"object","properties":{"hits":{"type":"number"}}}` || lookup.Exposure != extension.ToolExposureCodemode {
			t.Fatalf("outputSchema=%s exposure=%q", lookup.OutputSchema, lookup.Exposure)
		}
		if lookup.Namespace == nil || lookup.Namespace.Name != "mcp__conformance" || lookup.Namespace.Description != "Conformance server" || lookup.Namespace.Instructions != "Use the conformance lookup." {
			t.Fatalf("namespace=%+v", lookup.Namespace)
		}
		if a := lookup.Annotations; a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint || a.DestructiveHint != nil || a.IdempotentHint != nil {
			t.Fatalf("annotations=%+v", lookup.Annotations)
		}
		if lookup.DefaultActive == nil || *lookup.DefaultActive {
			t.Fatalf("defaultActive=%v, want false", lookup.DefaultActive)
		}
		if lookup.PrepareLoadout != nil {
			t.Fatal("lookup declares no prepareLoadout")
		}
		if h.ext.Tools["orchestrate"].Definition.PrepareLoadout == nil {
			t.Fatal("orchestrate declares prepareLoadout but the host has none")
		}
		plain := h.ext.Tools["probe"].Definition
		if plain.OutputSchema != nil || plain.Exposure != "" || plain.Namespace != nil || plain.Annotations != nil || plain.DefaultActive != nil {
			t.Fatalf("undeclared fields set on %+v", plain)
		}
	})
}

// agent types.ts:433-440: structuredContent and isError survive the subprocess boundary, and a failure without a throw keeps its details.
func TestRustSDKStructuredContentAndIsError(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		lookup := h.execute(t, "lookup", "call-1")
		if string(lookup.StructuredContent) != `{"hits":3}` || lookup.IsError {
			t.Fatalf("lookup structuredContent=%s isError=%v", lookup.StructuredContent, lookup.IsError)
		}
		failing := h.execute(t, "failing", "call-2")
		if !failing.IsError || string(failing.StructuredContent) != `{"ok":false}` {
			t.Fatalf("failing isError=%v structuredContent=%s", failing.IsError, failing.StructuredContent)
		}
		if !reflect.DeepEqual(failing.Details, map[string]any{"status": float64(503)}) && !strings.Contains(toJSON(t, failing.Details), `"status":503`) {
			t.Fatalf("failing details=%v", failing.Details)
		}
	})
}

// types.ts:1711 (getSettings), 1839 (getMcpServers), 385 (tools): synchronous getters answered from the host's replicated state.
func TestRustSDKReplicatedGetters(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		details := h.details(t, "probe", "call-1")
		settings, _ := details["settings"].(map[string]any)
		if settings["marker"] != rustAPISettingsMarker || settings["fullscreenWheelScrollLines"] != float64(7) {
			t.Fatalf("getSettings=%v", details["settings"])
		}
		servers := toJSON(t, details["servers"])
		if !strings.Contains(servers, `"name":"loaded"`) || !strings.Contains(servers, `https://mcp.invalid/loaded`) {
			t.Fatalf("getMcpServers=%s, want the server the extension registered while loading", servers)
		}
		if !strings.Contains(servers, `"description":"Loaded server"`) || !strings.Contains(servers, `"auth":{"provider":"loaded-provider"}`) {
			t.Fatalf("getMcpServers=%s, want the description and auth.provider the extension registered", servers)
		}
		tools := toJSON(t, details["tools"])
		if !strings.Contains(tools, `"name":"`+rustAPICallableTool+`"`) || !strings.Contains(tools, `"outputSchema"`) {
			t.Fatalf("ctx.tools=%s, want the host's callable tools", tools)
		}
	})
}

// loader.ts:456-478: registerMcpServer at load and after load, unregisterMcpServer, validation errors as the extension's throw.
func TestRustSDKMcpServerRegistration(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		names := func() []string {
			var out []string
			for _, server := range h.host.Runtime().McpServers().List() {
				out = append(out, server.Name)
			}
			return out
		}
		if got := names(); !reflect.DeepEqual(got, []string{"loaded"}) {
			t.Fatalf("servers after load = %v", got)
		}
		registered := h.execute(t, "register_late", "call-1")
		if got := names(); !reflect.DeepEqual(got, []string{"loaded", "late"}) {
			t.Fatalf("servers after registerMcpServer = %v", got)
		}
		if listed := toJSON(t, registered.Details); !strings.Contains(listed, `"name":"late"`) || !strings.Contains(listed, `"name":"loaded"`) {
			t.Fatalf("getMcpServers after registerMcpServer = %s", listed)
		}
		// mcp-servers.ts:151-166,192-196 and loader.ts:479 (0.99.2): the registration made with the `codemode-deferred` alias reads back with the exposure it stands for in getMcpServers.
		if listed := toJSON(t, registered.Details); !strings.Contains(listed, `"exposure":"codemode","toolExposure":{"t*":"codemode"}`) || strings.Contains(listed, "codemode-deferred") {
			t.Fatalf("getMcpServers after registerMcpServer = %s, want the alias resolved", listed)
		}
		registeredTool := h.ext.Tools["register_invalid"].Definition
		if _, err := registeredTool.Execute(t.Context(), "call-2", json.RawMessage(`{}`), nil); err == nil || !strings.Contains(err.Error(), `invalid server name "not valid"`) {
			t.Fatalf("invalid registration error = %v, want the host's validation message", err)
		}
		unregistered := h.execute(t, "unregister_late", "call-3")
		if got := names(); !reflect.DeepEqual(got, []string{"loaded"}) {
			t.Fatalf("servers after unregisterMcpServer = %v", got)
		}
		if listed := toJSON(t, unregistered.Details); strings.Contains(listed, `"name":"late"`) {
			t.Fatalf("getMcpServers after unregisterMcpServer = %s", listed)
		}
	})
}

// types.ts:1929-1991: a provider registered with chat, image and classifier model entries reaches the host's registry with each entry's
// type and variant fields, so the Rust SDK's pass-through config loses none of them.
func TestRustSDKProviderModelTypes(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		h.mu.Lock()
		config, ok := h.providers["conformance-media"]
		h.mu.Unlock()
		if !ok || len(config.Models) != 3 {
			t.Fatalf("registered provider = %+v (found %v)", config, ok)
		}
		chat, image, classifier := config.Models[0], config.Models[1], config.Models[2]
		if chat.ID != "chat-1" || (chat.Type != "" && chat.Type != ai.ModelTypeChat) || chat.ContextWindow != 128000 || !chat.Reasoning {
			t.Fatalf("chat entry = %+v", chat)
		}
		if image.ID != "image-1" || image.Type != ai.ModelTypeImage || image.API != "openrouter-images" || !reflect.DeepEqual(image.Output, []string{"image", "text"}) {
			t.Fatalf("image entry = %+v", image)
		}
		if classifier.ID != "classifier-1" || classifier.Type != ai.ModelTypeClassifier || classifier.API != "llama-cpp-classify" || classifier.ContextWindow != 8192 {
			t.Fatalf("classifier entry = %+v", classifier)
		}
	})
}

// runner.ts:966-983: executeTool names the calling tool call, delivers partial results in order, and returns the host's outcome.
func TestRustSDKExecuteTool(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		details := h.details(t, "nested", "call-9")
		h.mu.Lock()
		executed := append([]string(nil), h.executed...)
		h.mu.Unlock()
		if !reflect.DeepEqual(executed, []string{`call-9|target|{"q":"conformance"}`}) {
			t.Fatalf("host executeTool calls = %v", executed)
		}
		if got := toJSON(t, details["updates"]); got != `["partial-one","partial-two"]` {
			t.Fatalf("onUpdate = %s, want both partial results in order", got)
		}
		outcome := toJSON(t, details["outcome"])
		for _, want := range []string{`"id":"call-9/1"`, `"durationMs":4321`, `"text":"nested-ok"`, `"structuredContent":{"answer":42}`} {
			if !strings.Contains(outcome, want) {
				t.Fatalf("outcome %s lacks %s", outcome, want)
			}
		}
	})
}

// virtual-models.ts:87-101 and pi.registerVirtualModel (packages/coding-agent/src/core/extensions/types.ts:1872, loader.ts:500-509): the virtual model's route runs in the extension with the host's request.
func TestRustSDKVirtualModelRoute(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		pending := h.host.Runtime().PendingVirtualModelRegistrations()
		if len(pending) != 2 || pending[0].Definition.Provider != "conformance" || pending[0].Definition.ID != "auto" || pending[0].Definition.Name != "Auto" || pending[0].Definition.ContextWindow != 64000 || pending[1].Definition.ID != "identity" {
			t.Fatalf("registerVirtualModel: pending virtual models = %+v", pending)
		}
		route, err := pending[0].Definition.Route(t.Context(), extension.ModelRouteRequest{
			Model:         &ai.Model{ID: "auto"},
			ThinkingLevel: "high",
			Reason:        extension.ModelRouteReasonRetry,
			Messages:      []ai.Message{ai.UserMessage{Content: ai.UserText("one")}, ai.UserMessage{Content: ai.UserText("two")}},
		})
		// model-runtime.ts:1000-1009: the route goes back as the router returned it. The host's registry has no such model, so the route names the pair the router answered and the model runtime, not the router call, rejects it.
		if err != nil || route.Model == nil || route.Model.ProviderMeta.ProviderID != "conformance" || route.Model.ID != "phys-retry-2-false" {
			t.Fatalf("route = %+v, %v; want the router's answer conformance/phys-retry-2-false", route, err)
		}
	})
}

// agent-session.ts:1498-1531: prepareLoadout runs in the extension with the host's tools, exposures and namespaces.
func TestRustSDKPrepareLoadout(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		tool := func(name string) extension.AgentTool {
			return extension.AgentTool{Name: name, Label: name, Description: name, Parameters: json.RawMessage(`{"type":"object"}`)}
		}
		changes := h.ext.Tools["orchestrate"].Definition.PrepareLoadout(extension.ToolLoadout{
			Declared:   []extension.AgentTool{tool("orchestrate"), tool("read"), tool("grep")},
			Callable:   []extension.AgentTool{tool("read"), tool("grep")},
			Registered: []extension.AgentTool{tool("orchestrate"), tool("read"), tool("grep"), tool("conf-deferred")},
			GetExposure: func(name string) extension.ToolExposure {
				if name == "conf-deferred" {
					return extension.ToolExposureDeferred
				}
				return extension.ToolExposureDirect
			},
			GetNamespace: func(name string) *extension.ToolNamespace {
				if name == "conf-deferred" {
					return &extension.ToolNamespace{Name: "mcp__loadout"}
				}
				return nil
			},
			GetPromptGuidelines: func(name string) []string {
				if name == "grep" {
					return []string{"Use grep for patterns.", "Quote regexes."}
				}
				return nil
			},
		})
		if changes == nil {
			t.Fatal("prepareLoadout returned no changes")
		}
		if got := changes.Descriptions["orchestrate"]; got != `Runs read,grep | deferred | Some("mcp__loadout") | ["Use grep for patterns.", "Quote regexes."] | []` {
			t.Fatalf("description = %q", got)
		}
		if !reflect.DeepEqual(changes.HiddenDeclarations, []string{"read", "grep"}) {
			t.Fatalf("hiddenDeclarations = %v", changes.HiddenDeclarations)
		}
	})
}

func toJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
