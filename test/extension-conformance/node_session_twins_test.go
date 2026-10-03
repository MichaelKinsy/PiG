package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Node twins of the upstream session-level tests for the 0.99.1 extension API. Upstream registers its extensions as in-process factories (`extensionFactories: [(pi) => ...]`); here the same factories run as Node extensions in the real Host and the assertions read the real Session, so the whole path (Node runtime, wire, Host, Session, ModelRuntime) is the proof. The Go ports of the same cases with in-process extensions are coding/session_tool_orchestration_upstream_test.go and coding/virtual_models_suite_upstream_test.go.

type nodeSessionRig struct {
	t       *testing.T
	session *coding.Session
	ui      *recordingUI
	faux    interface{ SetResponses([]ai.FauxResponseStep) }
	events  chan struct{}
}

// newNodeSessionRig builds a Session over a faux provider with the models, loads the Node fixtures into a Host wired to it, and binds the extensions. settings is the JSON body of settings.json.
func newNodeSessionRig(t *testing.T, isolation, settings string, models []ai.FauxModelDefinition, fixtures ...nodeAPIFixture) *nodeSessionRig {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts Node subprocesses)")
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{"+settings+"}"), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Models: models})
	runtime := services.ModelRuntime()
	if err := runtime.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}

	notify, status := &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	// The models the Node registry reads are the runtime's, as in the binary (cmd/pig/extensions.go wireSubprocessModelRegistry).
	detach := codingagent.WireModelOperations(bridge, codingagent.ModelOperationBindings{ModelLookup: runtime.GetModel, ModelCatalog: runtime.GetModels, Registry: services.Registry().ModelRegistry})
	t.Cleanup(detach)
	h, loaded := loadNodeFixtures(t, isolation, bridge, fixtures)

	runner := inproc.NewRunner(loaded, t.TempDir(), h.Runtime())
	bridge.SetUIPromptScope(runner)
	session, err := coding.NewSession(services, coding.SessionOptions{Model: runtime.GetModel("faux", models[0].ID), Runner: runner, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	bridge.SetHostAction("refreshTools", session.RefreshTools)
	bridge.SetHostAction("setActiveTools", session.SetActiveToolsByName)
	bridge.SetHostAction("getActiveTools", session.ActiveToolNames)
	bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo { return codingagent.ExtensionToolInfos(runner, nil, nil) })
	tools := session.ToolActions()
	bridge.SetHostAction("getCallableTools", tools.GetCallableTools)
	bridge.SetHostAction("executeTool", tools.ExecuteTool)

	events := make(chan struct{})
	go func() {
		defer close(events)
		for event := range session.Events() {
			coding.AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() { _ = session.Close(); <-events })
	if err := session.BindExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	return &nodeSessionRig{t: t, session: session, ui: ui, faux: faux, events: events}
}

func (r *nodeSessionRig) notifications() []string {
	var out []string
	for _, line := range r.ui.Recorded() {
		out = append(out, strings.TrimSuffix(line, ":info"))
	}
	return out
}

// tagged returns the notifications that start with tag+"|", in order, with the tag removed.
func (r *nodeSessionRig) tagged(tag string) []string {
	var out []string
	for _, line := range r.notifications() {
		if rest, ok := strings.CutPrefix(line, tag+"|"); ok {
			out = append(out, rest)
		}
	}
	return out
}

func assertEqualNode[T any](t *testing.T, name string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

// ── suite/agent-session-tool-orchestration.test.ts ───────────────────────────

// orchestrator is upstream's `orchestratorExtension` (:14-53) and the tool_call recorder (:69-73), unchanged apart from the recorder reporting through ctx.ui.notify.
const nodeOrchestratorFixture = `
const text = (value) => ({ content: [{ type: "text", text: value }], details: {} });
export default function (pi) {
  pi.registerTool({
    name: "echo", label: "echo", description: "Echo text.",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    execute: async (_id, { text: value }) => text("echo: " + value),
  });
  pi.registerTool({
    name: "helper", label: "helper", description: "Only reachable from other tools.",
    parameters: { type: "object", properties: {} }, exposure: "codemode",
    execute: async () => text("helped"),
  });
  pi.registerTool({
    name: "run_tools", label: "run_tools", description: "Runs tools.",
    parameters: { type: "object", properties: {} }, exposure: "model-only",
    prepareLoadout: (loadout) => ({
      descriptions: {
        run_tools: "Runs tools: " + loadout.callable.map((tool) => tool.name).join(", "),
        echo: "Echo text (also callable from run_tools).",
      },
      hiddenDeclarations: ["echo"],
    }),
    execute: async (_id, _params, _signal, _onUpdate, ctx) => {
      const helper = await ctx.executeTool("helper", {});
      const echo = await ctx.executeTool("echo", { text: "hi" });
      const self = await ctx.executeTool("run_tools", {});
      return text([helper, echo, self].map((outcome) => outcome.result.content[0].text).join(" | "));
    },
  });
  pi.on("tool_call", (event, ctx) => { ctx.ui.notify("tool_call|" + event.toolName + ":" + (event.parentToolCallId ?? "top"), "info"); });
}
`

// upstream agent-session-tool-orchestration.test.ts:62-113
func TestNodeSessionToolOrchestrationSupportsToolsThatCallOtherToolsUnderAnyNameThroughTheExtensionAPI(t *testing.T) {
	for _, isolation := range nodeAPIIsolations {
		t.Run(isolation, func(t *testing.T) {
			rig := newNodeSessionRig(t, isolation, "", []ai.FauxModelDefinition{{ID: "faux-1"}}, nodeAPIFixture{"nodeapi-orchestrator", nodeOrchestratorFixture})
			session := rig.session
			assertEqualNode(t, "active tools", session.ActiveToolNames(), []string{"echo", "run_tools"})
			assertEqualNode(t, "callable tools", session.CallableToolNames(), []string{"echo", "helper"})
			description := func(name string) string {
				for _, tool := range session.Agent().Tools() {
					if tool.Name() == name {
						return tool.Schema().Description
					}
				}
				return ""
			}
			if got := description("run_tools"); got != "Runs tools: echo, helper" {
				t.Fatalf("run_tools description = %q", got)
			}
			if got := description("echo"); got != "Echo text (also callable from run_tools)." {
				t.Fatalf("echo description = %q", got)
			}

			var requestTools [][]string
			rig.faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
					var names []string
					for _, tool := range ai.GetCurrentTools(request.Messages()) {
						names = append(names, tool.Name)
					}
					requestTools = append(requestTools, names)
					return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("run_tools", map[string]any{}, "")}, StopReason: "toolUse"}, nil
				}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			if _, err := session.Prompt(t.Context(), "go"); err != nil {
				t.Fatal(err)
			}

			// echo stays active, but its declaration is left out of requests.
			assertEqualNode(t, "request tools", requestTools, [][]string{{"run_tools"}})
			var result *agent.ToolResultMessage
			for _, message := range session.Messages() {
				if message.ToolResult != nil {
					result = message.ToolResult
					break
				}
			}
			if result == nil {
				t.Fatal("no tool result")
			}
			if len(result.Content) != 1 || result.Content[0] != (ai.TextContent{Text: "helped | echo: hi | Tool run_tools not found"}) {
				t.Fatalf("content = %+v", result.Content)
			}
			parent := result.ToolCallID
			assertEqualNode(t, "tool_call events", rig.tagged("tool_call"), []string{"run_tools:top", "helper:" + parent, "echo:" + parent})
			if result.NestedCalls == nil {
				t.Fatal("the tool result has no nestedCalls")
			}
			type row struct {
				id, name string
				status   ai.NestedToolCallStatus
			}
			var rows []row
			for _, call := range result.NestedCalls.Calls {
				rows = append(rows, row{call.ID, call.Name, call.Status})
			}
			assertEqualNode(t, "nested calls", rows, []row{{parent + "/1", "helper", ai.NestedToolCallOK}, {parent + "/2", "echo", ai.NestedToolCallOK}, {parent + "/3", "run_tools", ai.NestedToolCallError}})
			// The record is persisted with the session.
			for _, entry := range session.Inner().GetBranch() {
				if message, ok := entry.AsMessage(); ok && message.Message.ToolResult != nil {
					if !reflect.DeepEqual(message.Message.ToolResult.NestedCalls, result.NestedCalls) {
						t.Fatalf("persisted nestedCalls = %+v, want %+v", message.Message.ToolResult.NestedCalls, result.NestedCalls)
					}
					return
				}
			}
			t.Fatal("no persisted tool result")
		})
	}
}

// upstream agent-session-tool-orchestration.test.ts:133-144
func TestNodeSessionToolOrchestrationLeavesResultsWithoutNestedCallsUnchanged(t *testing.T) {
	rig := newNodeSessionRig(t, "strict", "", []ai.FauxModelDefinition{{ID: "faux-1"}}, nodeAPIFixture{"nodeapi-orchestrator", nodeOrchestratorFixture})
	rig.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("echo", map[string]any{"text": "x"}, "")}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	if _, err := rig.session.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	for _, message := range rig.session.Messages() {
		if message.ToolResult == nil {
			continue
		}
		raw, err := json.Marshal(message.ToolResult)
		if err != nil {
			t.Fatal(err)
		}
		if message.ToolResult.NestedCalls != nil || strings.Contains(string(raw), "nestedCalls") {
			t.Fatalf("a result without nested calls carries nestedCalls: %s", raw)
		}
		return
	}
	t.Fatal("no tool result")
}

// ── suite/virtual-models.test.ts ─────────────────────────────────────────────

// nodeRoutedFixture is upstream's `createRoutedHarness` extension (:39-78) with `defaultRoute` (:20-30) and the echo tool of the harness (:10-16) as Node code. Each request is reported as `route|<reason>|<failed error>|<failed model>|<previous model>|<state>` so the assertions read what the router received. body is the JavaScript of `route`, which may use `defaultRoute(request, ctx)`.
func nodeRoutedFixture(routeBody string) nodeAPIFixture {
	return nodeAPIFixture{"nodeapi-router", `
const defaultRoute = (request, ctx) => {
  const find = (id) => ctx.modelRegistry.find("faux", id);
  if (request.reason === "direct") return { model: find("large"), thinkingLevel: "low" };
  const sticky = request.failed ?? request.previous;
  if (request.reason !== "user" && sticky) return { model: sticky.model, thinkingLevel: sticky.thinkingLevel ?? "high" };
  return request.thinkingLevel === "high" ? { model: find("large"), thinkingLevel: "high" } : { model: find("small"), thinkingLevel: "off" };
};
const report = (request, ctx) => ctx.ui.notify(["route", request.reason, request.failed?.message.errorMessage ?? "-", request.failed?.model.id ?? "-", request.previous?.model.id ?? "-", String(JSON.stringify(request.state))].join("|"), "info");
export default function (pi) {
  pi.registerTool({
    name: "echo", label: "Echo", description: "Echo text back",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    execute: async () => ({ content: [{ type: "text", text: "echoed" }], details: {} }),
  });
  pi.registerVirtualModel({
    provider: "router", id: "auto", name: "Auto", thinkingLevels: ["low", "high"], contextWindow: 1000,
    route(request, ctx) {
      report(request, ctx);
      ` + routeBody + `
    },
  });
}
`}
}

var nodeRoutedModels = []ai.FauxModelDefinition{
	{ID: "small", ContextWindow: 1000},
	{ID: "large", ContextWindow: 50_000, MaxTokens: 4000, Reasoning: true},
}

const nodeRetrySettings = `"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}`

func newNodeRoutedRig(t *testing.T, settings, routeBody string) *nodeSessionRig {
	t.Helper()
	rig := newNodeSessionRig(t, "strict", settings, nodeRoutedModels, nodeRoutedFixture(routeBody))
	runtime := rig.session.ModelRuntime()
	if err := rig.session.SetModel(runtime.GetModel("router", "auto")); err != nil {
		t.Fatal(err)
	}
	if err := rig.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	return rig
}

func (r *nodeSessionRig) reasons() []string {
	var out []string
	for _, line := range r.tagged("route") {
		out = append(out, strings.SplitN(line, "|", 2)[0])
	}
	return out
}

// dispatched is the physical model and thinking level recorded on each response.
func (r *nodeSessionRig) dispatched() []string {
	out := []string{}
	for _, message := range r.session.Messages() {
		if a := message.Assistant; a != nil {
			out = append(out, a.Provider+"/"+a.ModelID+":"+string(a.ThinkingLevel))
		}
	}
	return out
}

func fauxTextStep(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"})
}

func fauxErrorStepNode(message string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: message})
}

// upstream suite/virtual-models.test.ts:80-100
func TestNodeVirtualSuiteRoutesEachRequestIncludingRetriesWhileTheSelectionStaysVirtual(t *testing.T) {
	rig := newNodeRoutedRig(t, nodeRetrySettings, `return defaultRoute(request, ctx);`)
	rig.faux.SetResponses([]ai.FauxResponseStep{
		fauxErrorStepNode("overloaded_error"),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("echo", map[string]any{"text": "hi"}, "")}, StopReason: "toolUse"}),
		fauxTextStep("done"),
	})

	if _, err := rig.session.Prompt(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}

	assertEqualNode(t, "reasons", rig.reasons(), []string{"user", "retry", "continuation"})
	routes := rig.tagged("route")
	if !strings.HasPrefix(routes[1], "retry|overloaded_error|large|") {
		t.Fatalf("retry request = %q, want the failed request on large with overloaded_error", routes[1])
	}
	if !strings.HasPrefix(routes[2], "continuation|-|-|large|") {
		t.Fatalf("continuation request = %q, want previous large", routes[2])
	}
	assertEqualNode(t, "dispatched", rig.dispatched(), []string{"faux/large:high", "faux/large:high"})
	if model := rig.session.Model(); model.ProviderMeta.ProviderID != "router" || model.ID != "auto" {
		t.Fatalf("selection = %s/%s", model.ProviderMeta.ProviderID, model.ID)
	}
	if rig.session.ThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("thinking level = %q", rig.session.ThinkingLevel())
	}
	// Limits come from the physical model that produced the latest response, not the virtual model.
	if usage := rig.session.ContextUsage(); usage == nil || usage.ContextWindow != 50_000 {
		t.Fatalf("context usage = %+v", usage)
	}
}

// upstream suite/virtual-models.test.ts:102-122
func TestNodeVirtualSuiteRetriesTheFirstRequestOfATurnOnTheModelRoutedForThatTurn(t *testing.T) {
	rig := newNodeRoutedRig(t, nodeRetrySettings, `return defaultRoute(request, ctx);`)
	if err := rig.session.SetThinkingLevel(ai.ThinkingLow); err != nil {
		t.Fatal(err)
	}
	rig.faux.SetResponses([]ai.FauxResponseStep{fauxTextStep("easy answer"), fauxErrorStepNode("overloaded_error"), fauxTextStep("hard answer")})
	if _, err := rig.session.Prompt(t.Context(), "easy"); err != nil {
		t.Fatal(err)
	}
	if err := rig.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}

	if _, err := rig.session.Prompt(t.Context(), "hard"); err != nil {
		t.Fatal(err)
	}

	assertEqualNode(t, "reasons", rig.reasons(), []string{"user", "user", "retry"})
	// The retry reports the failed request on large next to the small response of the previous turn.
	routes := rig.tagged("route")
	if !strings.HasPrefix(routes[2], "retry|overloaded_error|large|small|") {
		t.Fatalf("retry request = %q, want failed large and previous small", routes[2])
	}
	assertEqualNode(t, "dispatched", rig.dispatched(), []string{"faux/small:off", "faux/large:high"})
}

// upstream suite/virtual-models.test.ts:305-344
func TestNodeVirtualSuiteStoresRouterStateOnTheBranchAndPassesItToLaterRequests(t *testing.T) {
	rig := newNodeRoutedRig(t, `"compaction":{"keepRecentTokens":1}`, `
      const turns = request.state?.turns ?? 0;
      const route = defaultRoute(request, ctx);
      // Returning request.state keeps it without storing it again. Direct requests neither get nor store state.
      if (request.reason === "continuation") return { ...route, state: request.state };
      return { ...route, state: { turns: turns + 1 } };`)
	rig.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("echo", map[string]any{"text": "hi"}, "")}, StopReason: "toolUse"}),
		fauxTextStep("first"), fauxTextStep("second"), fauxTextStep("summary"), fauxTextStep("summary"),
	})

	for _, text := range []string{"one", "two"} {
		if _, err := rig.session.Prompt(t.Context(), text); err != nil {
			t.Fatal(err)
		}
	}
	stored := func() []string {
		var out []string
		for _, entry := range rig.session.Inner().GetBranch() {
			if entry.Base.Type != "custom" {
				continue
			}
			var custom struct {
				CustomType string          `json:"customType"`
				Data       json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(entry.Raw(), &custom); err == nil && custom.CustomType == coding.VirtualModelStateEntry {
				out = append(out, string(custom.Data))
			}
		}
		return out
	}
	assertEqualNode(t, "stored", stored(), []string{`{"provider":"router","modelId":"auto","state":{"turns":1}}`, `{"provider":"router","modelId":"auto","state":{"turns":2}}`})

	if err := rig.session.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}

	assertEqualNode(t, "reasons", rig.reasons(), []string{"user", "continuation", "user", "direct"})
	var states []string
	for _, line := range rig.tagged("route") {
		parts := strings.Split(line, "|")
		states = append(states, parts[len(parts)-1])
	}
	assertEqualNode(t, "states", states, []string{"undefined", `{"turns":1}`, `{"turns":1}`, "undefined"})
	if n := len(stored()); n != 2 {
		t.Fatalf("stored states = %d, want 2", n)
	}
}
