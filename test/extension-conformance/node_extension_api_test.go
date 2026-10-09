package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The Pi 0.99.1 extension API additions through the Node runtime and the real Host (.upstream/v0.99.1/packages/coding-agent/src/core/extensions/{types,loader,runner}.ts). These are the Node twins of the Python rows in python_extension_api_test.go: each binds its assertion to a value only the Host or the Node runtime's answer can produce (a host-assigned extension path, a router's computed state, a description built from the loadout the Host sent, the id the Host gave a nested call), so a runtime that never makes the call, or answers with a default, cannot match.

var nodeAPIIsolations = []string{"strict", "shared-ok"}

// nodeAPIFixture is one Node extension of a row. Names are file-safe.
type nodeAPIFixture struct{ name, code string }

type nodeAPIRig struct {
	t      *testing.T
	host   *subprocess.Host
	bridge *subprocess.UIBridge
	ui     *recordingUI
	exts   map[string]extension.Extension
	runner *inproc.Runner
}

// newNodeAPIRig loads the fixtures under one isolation: strict runs each in its own process, shared-ok packs them into one cell (upstream behavior must not depend on it).
func newNodeAPIRig(t *testing.T, isolation string, actions *subprocess.HostCallbacks, fixtures ...nodeAPIFixture) *nodeAPIRig {
	t.Helper()
	notify, status := &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	if actions != nil {
		bridge.SetActions(actions)
	}
	h, loaded := loadNodeFixtures(t, isolation, bridge, fixtures)
	rig := &nodeAPIRig{t: t, host: h, bridge: bridge, ui: ui, exts: map[string]extension.Extension{}}
	for _, ext := range loaded {
		rig.exts[ext.Name] = ext
	}
	if len(rig.exts) != len(fixtures) {
		t.Fatalf("loaded %d of %d extensions", len(rig.exts), len(fixtures))
	}
	rig.runner = inproc.NewRunner(loaded, t.TempDir(), h.Runtime())
	bridge.SetUIPromptScope(rig.runner)
	return rig
}

// loadNodeFixtures starts a Host on bridge and loads the fixtures as Node extensions under one isolation.
func loadNodeFixtures(t *testing.T, isolation string, bridge *subprocess.UIBridge, fixtures []nodeAPIFixture) (*subprocess.Host, []extension.Extension) {
	t.Helper()
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetUIBridge(bridge)
	t.Cleanup(func() { h.Shutdown("test complete") })
	var configs []subprocess.ExtConfig
	for _, f := range fixtures {
		path := filepath.Join(t.TempDir(), f.name+".mjs")
		if err := os.WriteFile(path, []byte(f.code), 0o600); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, subprocess.ExtConfig{Name: f.name, Source: path, Enabled: true, Isolation: isolation})
	}
	var loaded []extension.Extension
	if isolation == "shared-ok" {
		h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
		var err error
		loaded, err = h.Reload(t.Context())
		if err != nil {
			t.Fatalf("reload: %v (%+v)", err, h.LastReloadReport())
		}
		if report := h.LastReloadReport(); report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-node") {
			t.Fatalf("expected one packed node cell: %+v", report)
		}
	} else {
		var failures []error
		loaded, failures = h.LoadAll(t.Context(), configs)
		if len(failures) != 0 {
			t.Fatalf("load: %v", failures)
		}
	}
	return h, loaded
}

func eachNodeAPIIsolation(t *testing.T, body func(t *testing.T, isolation string)) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts Node subprocesses)")
	}
	for _, isolation := range nodeAPIIsolations {
		t.Run(isolation, func(t *testing.T) { body(t, isolation) })
	}
}

func (r *nodeAPIRig) command(ext, name, args string) {
	r.t.Helper()
	command, ok := r.exts[ext].Commands[name]
	if !ok {
		r.t.Fatalf("%s has no command %s", ext, name)
	}
	if err := command.Handler(r.t.Context(), args); err != nil {
		r.t.Fatalf("%s /%s: %v", ext, name, err)
	}
}

// notifications returns what the extensions reported through ctx.ui.notify, with its level stripped.
func (r *nodeAPIRig) notifications() []string {
	var out []string
	for _, line := range r.ui.Recorded() {
		out = append(out, strings.TrimSuffix(line, ":info"))
	}
	return out
}

func (r *nodeAPIRig) waitNotification(want string) {
	r.t.Helper()
	waitFor(r.t, func() bool { return slices.Contains(r.notifications(), want) })
}

func (r *nodeAPIRig) execute(ext, tool, id string) (agent.AgentToolResult, error) {
	r.t.Helper()
	def, ok := r.exts[ext].Tools[tool]
	if !ok {
		r.t.Fatalf("%s has no tool %s", ext, tool)
	}
	result, err := def.Definition.Execute(r.t.Context(), id, json.RawMessage(`{}`), nil)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	typed, ok := result, true
	if !ok {
		r.t.Fatalf("%s result type = %T", tool, result)
	}
	return typed, nil
}

const nodeAPIPrelude = `const lines = (ctx, ...values) => ctx.ui.notify(values.map(String).join("|"), "info");
`

// ── MCP servers ──────────────────────────────────────────────────────────────

const nodeMcpFixture = nodeAPIPrelude + `
export default function (pi) {
  pi.registerMcpServer("node-load", { url: "https://mcp.example/node-load", description: "Node docs", auth: { provider: "node-provider" } });
  pi.registerMcpServer("node-dropped", { url: "https://mcp.example/dropped" });
  pi.unregisterMcpServer("node-dropped");
  const listing = () => pi.getMcpServers().map((s) => s.name + "@" + s.extensionPath).sort().join(",");
  pi.registerCommand("add", { handler: async (_args, ctx) => {
    pi.registerMcpServer("node-late", { command: "node-mcp", args: ["--stdio"] });
    lines(ctx, "after-add", listing());
  } });
  pi.registerCommand("drop", { handler: async (_args, ctx) => {
    pi.unregisterMcpServer("node-late");
    lines(ctx, "after-drop", listing());
  } });
  pi.registerCommand("steal", { handler: async (_args, ctx) => {
    try {
      pi.registerMcpServer("owned-by-sibling", { url: "https://mcp.example/stolen" });
      lines(ctx, "steal", "no error");
    } catch (error) {
      lines(ctx, "steal", error.message);
    }
  } });
  pi.registerCommand("invalid", { handler: async (_args, ctx) => {
    try {
      pi.registerMcpServer("node-invalid", {});
      lines(ctx, "invalid", "no error");
    } catch (error) {
      lines(ctx, "invalid", error.message);
    }
  } });
  pi.registerCommand("show", { handler: async (_args, ctx) => lines(ctx, "show", listing()) });
  // loader.ts:473-476 and mcp-servers.ts:227-229: getMcpServers returns copies.
  pi.registerCommand("copy", { handler: async (_args, ctx) => {
    const first = pi.getMcpServers();
    first.find((s) => s.name === "node-load").config.url = "https://mutated.example";
    first.length = 0;
    // The host answers with the config in its own member order, not the registration order, so the row compares sorted members.
    const config = pi.getMcpServers().find((s) => s.name === "node-load").config;
    lines(ctx, "copy", JSON.stringify(Object.fromEntries(Object.entries(config).sort(([a], [b]) => (a < b ? -1 : 1)))));
  } });
}
`

const nodeMcpSiblingFixture = `export default function (pi) {
  pi.registerMcpServer("owned-by-sibling", { url: "https://mcp.example/sibling" });
}
`

// loader.ts:456-478 and 259-262: servers registered while the factory ran reach the runtime owned by the extension's path, a queued unregister removes its server, and after load the calls apply at once. getMcpServers lists the registry, including the path the Host assigned.
func TestNodeMcpServerRegistration(t *testing.T) {
	t.Parallel()
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-mcp", nodeMcpFixture}, nodeAPIFixture{"nodeapi-sibling", nodeMcpSiblingFixture})
		mcpPath, siblingPath := rig.exts["nodeapi-mcp"].Path, rig.exts["nodeapi-sibling"].Path
		registered := func() []string {
			var out []string
			for _, s := range rig.host.Runtime().McpServers().List() {
				out = append(out, s.Name+"@"+s.ExtensionPath)
			}
			slices.Sort(out)
			return out
		}
		want := []string{"node-load@" + mcpPath, "owned-by-sibling@" + siblingPath}
		if got := registered(); !slices.Equal(got, want) {
			t.Fatalf("after load: %v, want %v", got, want)
		}
		rig.command("nodeapi-mcp", "show", "")
		rig.waitNotification("show|" + strings.Join(want, ","))

		rig.command("nodeapi-mcp", "add", "")
		want = []string{"node-late@" + mcpPath, "node-load@" + mcpPath, "owned-by-sibling@" + siblingPath}
		rig.waitNotification("after-add|" + strings.Join(want, ","))
		if got := registered(); !slices.Equal(got, want) {
			t.Fatalf("after add: %v, want %v", got, want)
		}
		var late extension.RegisteredMcpServer
		for _, s := range rig.host.Runtime().McpServers().List() {
			if s.Name == "node-late" {
				late = s
			}
		}
		if late.Config.Command != "node-mcp" || !slices.Equal(late.Config.Args, []string{"--stdio"}) {
			t.Fatalf("node-late config = %+v", late.Config)
		}

		rig.command("nodeapi-mcp", "drop", "")
		want = []string{"node-load@" + mcpPath, "owned-by-sibling@" + siblingPath}
		rig.waitNotification("after-drop|" + strings.Join(want, ","))

		// loader.ts:463-470: a name another extension owns and an invalid config throw to the registering extension.
		rig.command("nodeapi-mcp", "steal", "")
		rig.waitNotification(`steal|MCP server "owned-by-sibling" is already registered by extension "` + siblingPath + `"`)
		rig.command("nodeapi-mcp", "invalid", "")
		waitFor(t, func() bool {
			for _, n := range rig.notifications() {
				if strings.HasPrefix(n, "invalid|Invalid MCP server registered by extension") {
					return true
				}
			}
			return false
		})
		if got := registered(); !slices.Equal(got, want) {
			t.Fatalf("after rejected registrations: %v, want %v", got, want)
		}

		rig.command("nodeapi-mcp", "copy", "")
		rig.waitNotification(`copy|{"auth":{"provider":"node-provider"},"description":"Node docs","url":"https://mcp.example/node-load"}`)
	})
}

// loader.ts:456-465: a server the factory registers with an invalid config, or with a name another extension owns, fails the load with the runtime's message instead of leaving a half-registered extension.
func TestNodeMcpServerRegisteredByTheFactoryIsCheckedAtLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts Node subprocesses)")
	}
	notify, status := &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetUIBridge(bridge)
	t.Cleanup(func() { h.Shutdown("test complete") })
	path := filepath.Join(t.TempDir(), "nodeapi-bad.mjs")
	if err := os.WriteFile(path, []byte(`export default function (pi) { pi.registerMcpServer("bad", {}); }`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, failures := h.LoadAll(t.Context(), []subprocess.ExtConfig{{Name: "nodeapi-bad", Source: path, Enabled: true}})
	if len(loaded) != 0 || len(failures) != 1 || !strings.Contains(failures[0].Error(), "Invalid MCP server registered by extension") {
		t.Fatalf("loaded %d, failures %v", len(loaded), failures)
	}
	if servers := h.Runtime().McpServers().List(); len(servers) != 0 {
		t.Fatalf("a failed load left %v registered", servers)
	}
}

// ── Virtual models ───────────────────────────────────────────────────────────

const nodeVirtualModelFixture = nodeAPIPrelude + `
const route = async (request, ctx) => {
  const n = request.state?.n ?? 0;
  return {
    model: { provider: "anthropic", id: "node-picked-" + request.reason },
    thinkingLevel: request.thinkingLevel,
    state: { n: n + 5, saw: request.messages.length, cancelled: request.signal.aborted, ctxCwd: typeof ctx.cwd },
  };
};

export default function (pi) {
  // The object may carry properties Pi ignores (virtual-models.ts:157-174); the load must not fail on them.
  pi.registerVirtualModel({ provider: "noderouter", id: "auto", name: "Node Auto", route, thinkingLevels: ["off", "low"], contextWindow: 123456, maxTokens: 4321, input: ["text"], description: "ignored", cost: { input: 1 } });
  pi.registerVirtualModel({ provider: "noderouter", id: "dropped", name: "Dropped", route });
  pi.unregisterVirtualModel("noderouter", "dropped");
  pi.registerCommand("add", { handler: async () => pi.registerVirtualModel({ provider: "noderouter", id: "late", name: "Late", route }) });
  pi.registerCommand("drop", { handler: async () => pi.unregisterVirtualModel("noderouter", "auto") });
}
`

// pi.registerVirtualModel (packages/coding-agent/src/core/extensions/types.ts:1872, loader.ts:500-509) and unregisterVirtualModel (packages/coding-agent/src/core/extensions/types.ts:1875, loader.ts:511-514), with virtual-models.ts:87-101: the declaration reaches the runtime queue without its route, the route runs in the extension against the request the Host sends and receives the extension context, and the physical model the router names is resolved by the Host. The value in the state is computed by the router from the state it was sent.
func TestNodeVirtualModelRegistrationAndRouting(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, &subprocess.HostCallbacks{}, nodeAPIFixture{"nodeapi-vm", nodeVirtualModelFixture})
		pending := func() map[string]extension.PendingVirtualModelRegistration {
			out := map[string]extension.PendingVirtualModelRegistration{}
			for _, p := range rig.host.Runtime().PendingVirtualModelRegistrations() {
				out[p.Definition.Provider+"/"+p.Definition.ID] = p
			}
			return out
		}
		got := pending()
		if len(got) != 1 {
			t.Fatalf("registerVirtualModel/unregisterVirtualModel: registered %v, want only noderouter/auto (dropped was unregistered while the factory ran)", got)
		}
		p := got["noderouter/auto"]
		def := p.Definition
		if def.Name != "Node Auto" || def.ContextWindow != 123456 || def.MaxTokens != 4321 || len(def.ThinkingLevels) != 2 || def.ThinkingLevels[1] != ai.ThinkingLow || !slices.Equal(def.Input, []string{"text"}) || def.Route == nil {
			t.Fatalf("definition = %+v", def)
		}
		if p.ExtensionPath != rig.exts["nodeapi-vm"].Path {
			t.Fatalf("extension path = %q, want %q", p.ExtensionPath, rig.exts["nodeapi-vm"].Path)
		}
		route, err := def.Route(t.Context(), extension.ModelRouteRequest{
			Model: &ai.Model{ID: "auto"}, ThinkingLevel: ai.ThinkingLow, Reason: extension.ModelRouteReasonUser,
			State:    json.RawMessage(`{"n":4}`),
			Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}, ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "again"}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if route.Model == nil || route.Model.ProviderMeta.ProviderID != "anthropic" || route.Model.ID != "node-picked-user" || route.ThinkingLevel != ai.ThinkingLow || string(route.State) != `{"n":9,"saw":2,"cancelled":false,"ctxCwd":"string"}` {
			t.Fatalf("route = model %v thinking %q state %s", route.Model, route.ThinkingLevel, route.State)
		}
		// A router that names a model the registry does not have comes back naming only the provider and id it returned; the model runtime rejects it with `<name> routed to <provider>/<id>, which is not a physical model.` (model-runtime.ts:1006-1009, virtual-models.ts:100; host_extension_api.go makeVirtualModelRoute since dcd2035fb). The red row copied the Python row, which still expects the Host itself to fail.
		unknown, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "auto"}, Reason: extension.ModelRouteReasonRetry})
		if err != nil || unknown.Model == nil || unknown.Model.ID != "node-picked-retry" || unknown.Model.ProviderMeta.ProviderID != "anthropic" {
			t.Fatalf("unknown physical model = %+v, %v", unknown.Model, err)
		}

		rig.command("nodeapi-vm", "add", "")
		if got := pending(); len(got) != 2 || got["noderouter/late"].Definition.Name != "Late" {
			t.Fatalf("after add: %v", got)
		}
		rig.command("nodeapi-vm", "drop", "")
		if got := pending(); len(got) != 1 || got["noderouter/late"].Definition.Name != "Late" {
			t.Fatalf("after drop: %v", got)
		}
	})
}

// virtual-models.ts:77-83: a router that returns no state, or the state it was given, keeps the current state; a router that returns a state stores it. Node distinguishes an absent state (undefined) from JSON null, which is a state.
const nodeVirtualStateFixture = `export default function (pi) {
  pi.registerVirtualModel({ provider: "noderouter", id: "states", name: "States", route: async (request) => {
    const model = { provider: "anthropic", id: "node-picked-user" };
    if (request.reason === "user") return { model, thinkingLevel: "off" };
    if (request.reason === "retry") return { model, thinkingLevel: "off", state: null };
    return { model, thinkingLevel: "off", state: request.state };
  } });
}
`

func TestNodeVirtualModelRouteStateIsAbsentOrStored(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, &subprocess.HostCallbacks{}, nodeAPIFixture{"nodeapi-states", nodeVirtualStateFixture})
		def := rig.host.Runtime().PendingVirtualModelRegistrations()[0].Definition
		route := func(reason extension.ModelRouteReason, state string) extension.ModelRoute {
			t.Helper()
			request := extension.ModelRouteRequest{Model: &ai.Model{ID: "states"}, Reason: reason}
			if state != "" {
				request.State = json.RawMessage(state)
			}
			routed, err := def.Route(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			return routed
		}
		if got := route(extension.ModelRouteReasonUser, `{"k":1}`); got.State != nil {
			t.Fatalf("an absent state reads %s, want none", got.State)
		}
		if got := route(extension.ModelRouteReasonRetry, `{"k":1}`); string(got.State) != "null" {
			t.Fatalf("a null state reads %q, want null", got.State)
		}
		if got := route(extension.ModelRouteReasonContinuation, `{"k":2}`); string(got.State) != `{"k":2}` {
			t.Fatalf("the given state reads %q", got.State)
		}
	})
}

// ── Tool definition fields, structured results, settings, provider config ────

const nodeToolsFixture = nodeAPIPrelude + `
export default function (pi) {
  pi.registerTool({
    name: "search", label: "Search", description: "Search docs", parameters: { type: "object" },
    outputSchema: { type: "object", properties: { rows: { type: "array" } } },
    exposure: "codemode", namespace: { name: "mcp__node", description: "Node server", instructions: "Search the node docs." },
    annotations: { readOnlyHint: true, openWorldHint: false }, defaultActive: false,
    prepareLoadout: (loadout) => {
      const grep = loadout.getNamespace("grep") ?? {};
      return {
        descriptions: { read: "node: " + loadout.callable.length + " callable, " + loadout.registered.length + " registered, grep is " + loadout.getExposure("grep") + " in " + grep.name + ", absent is " + loadout.getExposure("absent") + ", grep guidelines " + JSON.stringify(loadout.getPromptGuidelines("grep")) + ", absent guidelines " + JSON.stringify(loadout.getPromptGuidelines("absent")) },
        hiddenDeclarations: loadout.declared.map((t) => t.name).filter((name) => name !== "read"),
      };
    },
    execute: async () => ({ content: [{ type: "text", text: "unused" }], details: {} }),
  });
  pi.registerTool({ name: "plain", label: "Plain", description: "Plain", parameters: { type: "object" }, execute: async () => ({ content: [{ type: "text", text: "plain" }], details: {} }) });
  pi.registerTool({
    name: "structured", label: "Structured", description: "Structured result", parameters: { type: "object" },
    execute: async () => ({ content: [{ type: "text", text: "model-visible text" }], structuredContent: { rows: [3, 1, 4] }, isError: true, details: { k: "v" } }),
  });
  pi.registerTool({
    name: "structured_update", label: "Structured update", description: "Streams a structured partial", parameters: { type: "object" },
    execute: async (_id, _params, _signal, onUpdate) => {
      onUpdate({ content: [{ type: "text", text: "partial" }], structuredContent: { step: 1 }, details: {} });
      return { content: [{ type: "text", text: "final" }], structuredContent: { step: 2 }, details: {} };
    },
  });
  pi.registerCommand("settings", { handler: async (_args, ctx) => {
    const settings = pi.getSettings();
    settings.marker = "mutated";
    lines(ctx, "settings", pi.getSettings().marker, JSON.stringify(pi.getSettings().nested));
  } });
  let early;
  try { pi.getSettings(); early = "no error"; } catch (error) { early = error.message; }
  pi.registerCommand("settings-early", { handler: async (_args, ctx) => lines(ctx, "settings-early", early) });
  pi.registerCommand("all-tools", { handler: async (_args, ctx) => {
    const grep = pi.getAllTools().find((t) => t.name === "grep");
    lines(ctx, "all-tools", grep.exposure, grep.namespace?.name, JSON.stringify(grep.annotations), Object.hasOwn(grep, "source"));
  } });
  pi.registerProvider("nodeapi-multi", {
    baseUrl: "https://api.example/v1",
    models: [
      { id: "chat-1", name: "Chat", api: "openai-completions", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 },
      { type: "image", id: "img-1", name: "Image", api: "openai-images", output: ["image", "text"], input: ["text"] },
      { type: "classifier", id: "cls-1", name: "Classifier", api: "llama-cpp-classify", input: ["text"], contextWindow: 512 },
    ],
  });
}
`

// types.ts:579-607, 601-607 and agent-session.ts:1480-1518: a tool's outputSchema, exposure, namespace, annotations and defaultActive reach the Host's definition; prepareLoadout runs in the extension against the loadout the Host builds, and its answer carries values computed from that loadout.
func TestNodeToolExposureFieldsAndPrepareLoadout(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-tools", nodeToolsFixture})
		search := rig.exts["nodeapi-tools"].Tools["search"].Definition
		if string(search.OutputSchema) != `{"type":"object","properties":{"rows":{"type":"array"}}}` || search.Exposure != extension.ToolExposureCodemode {
			t.Fatalf("outputSchema=%s exposure=%q", search.OutputSchema, search.Exposure)
		}
		if search.Namespace == nil || search.Namespace.Name != "mcp__node" || search.Namespace.Description != "Node server" || search.Namespace.Instructions != "Search the node docs." {
			t.Fatalf("namespace = %+v", search.Namespace)
		}
		if a := search.Annotations; a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint || a.DestructiveHint != nil || a.IdempotentHint != nil {
			t.Fatalf("annotations = %+v", search.Annotations)
		}
		if search.DefaultActive == nil || *search.DefaultActive {
			t.Fatalf("defaultActive = %v, want false", search.DefaultActive)
		}
		plain := rig.exts["nodeapi-tools"].Tools["plain"].Definition
		if plain.PrepareLoadout != nil || plain.Exposure != "" || plain.DefaultActive != nil || plain.OutputSchema != nil || plain.Namespace != nil || plain.Annotations != nil {
			t.Fatalf("an undeclared field was set on %+v", plain)
		}
		if search.PrepareLoadout == nil {
			t.Fatal("a tool that defines prepareLoadout has no PrepareLoadout")
		}
		read := extension.AgentTool{Name: "read", Label: "Read", Description: "Read", Parameters: json.RawMessage(`{"type":"object"}`)}
		grep := extension.AgentTool{Name: "grep", Label: "Grep", Description: "Grep", Parameters: json.RawMessage(`{"type":"object"}`)}
		changes := search.PrepareLoadout(extension.ToolLoadout{
			Declared: []extension.AgentTool{read, grep}, Callable: []extension.AgentTool{read}, Registered: []extension.AgentTool{read, grep},
			GetExposure: func(name string) extension.ToolExposure {
				if name == "grep" {
					return extension.ToolExposureDeferred
				}
				return extension.ToolExposureDirect
			},
			GetNamespace: func(name string) *extension.ToolNamespace {
				if name == "grep" {
					return &extension.ToolNamespace{Name: "search-ns"}
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
		wantDescription := `node: 1 callable, 2 registered, grep is deferred in search-ns, absent is direct, grep guidelines ["Use grep for patterns.","Quote regexes."], absent guidelines []`
		if changes == nil || changes.Descriptions["read"] != wantDescription || !slices.Equal(changes.HiddenDeclarations, []string{"grep"}) {
			t.Fatalf("changes = %+v, want description %q hiding grep", changes, wantDescription)
		}
	})
}

// agent/src/types.ts:424-446: structuredContent and isError survive the runtime, the wire and the Host's tool result, in the final result and in a partial one.
func TestNodeToolResultCarriesStructuredContentAndIsError(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-tools", nodeToolsFixture})
		result, err := rig.execute("nodeapi-tools", "structured", "tc-structured")
		if err != nil {
			t.Fatal(err)
		}
		if string(result.StructuredContent) != `{"rows":[3,1,4]}` || !result.IsError || result.Text() != "model-visible text" {
			t.Fatalf("result = structured %s isError %v text %q", result.StructuredContent, result.IsError, result.Text())
		}

		// A partial result reaches the Host's agent as the AgentToolResult the tool passed to onUpdate (agent-loop.ts:778-786); the row checks that its content and details cross.
		var mu sync.Mutex
		var partials []string
		def := rig.exts["nodeapi-tools"].Tools["structured_update"].Definition
		final, err := def.Execute(t.Context(), "tc-update", json.RawMessage(`{}`), func(partial agent.AgentToolResult) {
			mu.Lock()
			defer mu.Unlock()
			details, _ := json.Marshal(partial.Details)
			partials = append(partials, fmt.Sprint(partial.Text(), "/", string(details)))
		})
		if err != nil {
			t.Fatal(err)
		}
		typed, ok := final, true
		if !ok || string(typed.StructuredContent) != `{"step":2}` {
			t.Fatalf("final = %#v", final)
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(partials, []string{"partial/{}"}) {
			t.Fatalf("partial results = %v", partials)
		}
	})
}

// types.ts:1712, loader.ts:177 and 411-413 and settings-manager.ts:562-564: getSettings returns a copy of the settings object the Host holds, read from the state the Host replicated, and throws until the runtime is bound.
func TestNodeGetSettings(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, &subprocess.HostCallbacks{GetSettings: func() extension.Settings {
			return extension.Settings{"marker": "node-settings-value-42", "nested": map[string]any{"b": []any{1, 2}, "a": true}}
		}}, nodeAPIFixture{"nodeapi-tools", nodeToolsFixture})
		rig.command("nodeapi-tools", "settings", "")
		rig.waitNotification(`settings|node-settings-value-42|{"a":true,"b":[1,2]}`)
		rig.command("nodeapi-tools", "settings-early", "")
		rig.waitNotification("settings-early|Extension runtime not initialized. Action methods cannot be called during extension loading.")
	})
}

// types.ts:2063 and agent-session.ts:1452-1461: getAllTools carries each tool's exposure, namespace and annotations, and none of the PiG-only legacy source.
func TestNodeGetAllToolsCarriesExposureNamespaceAndAnnotations(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		readOnly := true
		rig := newNodeAPIRig(t, isolation, &subprocess.HostCallbacks{GetAllTools: func() []subprocess.ToolInfo {
			return []subprocess.ToolInfo{{
				Name: "grep", Description: "Grep", Parameters: json.RawMessage(`{"type":"object"}`),
				Exposure: extension.ToolExposureDeferred, Namespace: &extension.ToolNamespace{Name: "search-ns"}, Annotations: &extension.ToolAnnotations{ReadOnlyHint: &readOnly},
				SourceInfo: extension.SourceInfo{Path: "/x", Source: "builtin", Scope: "temporary", Origin: "top-level"}, Source: "builtin",
			}}
		}}, nodeAPIFixture{"nodeapi-tools", nodeToolsFixture})
		rig.command("nodeapi-tools", "all-tools", "")
		rig.waitNotification(`all-tools|deferred|search-ns|{"readOnlyHint":true}|false`)
	})
}

// types.ts:1929-1991: a provider registered with chat, image and classifier model entries keeps each entry's type and its variant's fields.
func TestNodeProviderConfigWithImageAndClassifierModels(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-tools", nodeToolsFixture})
		var config *extension.ProviderConfig
		for _, p := range rig.host.Runtime().PendingProviderRegistrations() {
			if p.Name == "nodeapi-multi" {
				config = &p.Config
			}
		}
		if config == nil || len(config.Models) != 3 {
			t.Fatalf("provider registration = %+v", config)
		}
		chat, image, classifier := config.Models[0], config.Models[1], config.Models[2]
		if chat.ID != "chat-1" || (chat.Type != "" && chat.Type != ai.ModelTypeChat) || chat.ContextWindow != 1000 {
			t.Fatalf("chat entry = %+v", chat)
		}
		if image.ID != "img-1" || image.Type != ai.ModelTypeImage || string(image.API) != "openai-images" || !slices.Equal(image.Output, []string{"image", "text"}) {
			t.Fatalf("image entry = %+v", image)
		}
		if classifier.ID != "cls-1" || classifier.Type != ai.ModelTypeClassifier || string(classifier.API) != "llama-cpp-classify" || classifier.ContextWindow != 512 {
			t.Fatalf("classifier entry = %+v", classifier)
		}
	})
}

// ── ctx.tools, ctx.executeTool and parentToolCallId ──────────────────────────

const nodeNestFixture = nodeAPIPrelude + `
export default function (pi) {
  pi.registerTool({
    name: "nest", label: "nest", description: "Runs echo", parameters: { type: "object" },
    execute: async (_id, _params, _signal, _onUpdate, ctx) => {
      const seen = [];
      const out = await ctx.executeTool("echo", { v: 7 }, { onUpdate: (partial) => seen.push(partial.content[0].text) });
      const names = ctx.tools.map((t) => t.name).join(",");
      return { content: [{ type: "text", text: [names, out.toolCall.id, out.result.content[0].text, out.isError, seen.join(",")].join("|") }], details: {} };
    },
  });
  pi.registerTool({
    name: "cancel_nest", label: "cancel_nest", description: "Cancels wait", parameters: { type: "object" },
    execute: async (_id, _params, _signal, _onUpdate, ctx) => {
      const controller = new AbortController();
      setTimeout(() => controller.abort(), 200);
      const out = await ctx.executeTool("wait", {}, { signal: controller.signal });
      return { content: [{ type: "text", text: "isError=" + out.isError + " text=" + out.result.content[0].text }], details: {} };
    },
  });
  pi.registerCommand("tools-outside", { handler: async (_args, ctx) => lines(ctx, "outside", String(ctx.tools), typeof ctx.executeTool) });
  pi.on("tool_call", (event, ctx) => { lines(ctx, "tool_call", event.toolCallId, event.parentToolCallId); });
  pi.on("tool_result", (event, ctx) => { lines(ctx, "tool_result", event.toolCallId, event.parentToolCallId, JSON.stringify(event.structuredContent)); });
  pi.on("provider_stream_event", (event, ctx) => { lines(ctx, "provider_stream_event", event.provider, event.api, event.model, JSON.stringify(event.data)); });
  pi.on("mcp_servers_change", (event, ctx) => { lines(ctx, "mcp_servers_change", event.servers.map((s) => s.name).join(",")); });
}
`

// types.ts:367-395 and runner.ts:952-985: a tool's context lists the tools the Host reports as callable and runs another tool through the Host, which names the nested call after the calling one, streams partial results in order before the outcome, and returns the outcome untouched.
func TestNodeExecuteToolAndCallableTools(t *testing.T) {
	t.Parallel()
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		var mu sync.Mutex
		var seen []string
		actions := &subprocess.HostCallbacks{
			GetCallableTools: func() []extension.AgentTool {
				return []extension.AgentTool{{Name: "echo", Label: "Echo", Parameters: json.RawMessage(`{"type":"object"}`)}, {Name: "grep", Label: "Grep", Parameters: json.RawMessage(`{"type":"object"}`)}}
			},
			ExecuteTool: func(ctx context.Context, callerID, name string, args json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}}
				switch name {
				case "echo":
					if update := options.OnUpdate; update != nil {
						// The session rejects the call with the first sink error (agent-loop.ts:820-849); these rows' callbacks do not throw.
						var first error
						for _, step := range []string{"partial-one", "partial-two"} {
							if err := update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: step}}}); err != nil && first == nil {
								first = err
							}
						}
						if first != nil {
							return extension.AgentToolCallOutcome{}, first
						}
					}
					outcome.Result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "host ran echo " + string(args)}}}
				case "wait":
					<-ctx.Done()
					mu.Lock()
					seen = append(seen, "wait cancelled")
					mu.Unlock()
					outcome.IsError = true
					outcome.Result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "aborted"}}, IsError: true}
				}
				return outcome, nil
			},
		}
		rig := newNodeAPIRig(t, isolation, actions, nodeAPIFixture{"nodeapi-nest", nodeNestFixture})
		result, err := rig.execute("nodeapi-nest", "nest", "call-7")
		if err != nil || result.IsError {
			t.Fatalf("nest = %+v, %v", result, err)
		}
		if want := `echo,grep|call-7/1|host ran echo {"v":7}|false|partial-one,partial-two`; result.Text() != want {
			t.Fatalf("nest text = %q, want %q", result.Text(), want)
		}

		// The option signal cancels the nested call only; the calling tool continues and reads the aborted outcome.
		result, err = rig.execute("nodeapi-nest", "cancel_nest", "call-8")
		if err != nil {
			t.Fatal(err)
		}
		if result.Text() != "isError=true text=aborted" {
			t.Fatalf("cancel_nest text = %q", result.Text())
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(seen, []string{"wait cancelled"}) {
			t.Fatalf("host saw %v", seen)
		}
	})
}

// types.ts:385-395 (ExtensionToolContext only) and 1044-1219 (parentToolCallId, structuredContent), 699-709 and 884-890 (the new events): outside a tool call the context has no `tools` or `executeTool`, tool events carry the parent id and structured content, and the new events reach the handler with the fields the Host set.
func TestNodeToolEventsAndNewEvents(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-nest", nodeNestFixture})
		rig.command("nodeapi-nest", "tools-outside", "")
		rig.waitNotification("outside|undefined|undefined")

		base := extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "outer-1/2", ParentToolCallID: "outer-1"}
		if _, err := rig.runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{ToolCallEventBase: base, ToolName: "bash", Input: map[string]any{"command": "ls"}}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification("tool_call|outer-1/2|outer-1")

		if _, err := rig.runner.EmitToolResult(t.Context(), extension.CustomToolResultEvent{
			ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "outer-1/3", ParentToolCallID: "outer-1", Input: map[string]any{}, Content: []any{}, StructuredContent: json.RawMessage(`{"rows":[9,8]}`)},
			ToolName:            "custom",
		}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification(`tool_result|outer-1/3|outer-1|{"rows":[9,8]}`)

		if _, err := rig.runner.Emit(t.Context(), extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "prov-x", API: "api-y", Model: "model-z", Data: map[string]any{"delta": []any{1, "two"}}}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification(`provider_stream_event|prov-x|api-y|model-z|{"delta":[1,"two"]}`)

		servers := []extension.RegisteredMcpServer{{Name: "one", ExtensionPath: "/x"}, {Name: "two", ExtensionPath: "/y"}}
		if _, err := rig.runner.Emit(t.Context(), extension.McpServersChangeEvent{Type: "mcp_servers_change", Servers: servers}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification("mcp_servers_change|one,two")
	})
}

const nodeOwnSignalFixture = `export default function (pi) {
  pi.registerTool({
    name: "own_signal", label: "own_signal", description: "Runs wait_release under its own signal", parameters: { type: "object" },
    execute: async (_id, _params, _signal, _onUpdate, ctx) => {
      await ctx.executeTool("wait_release", {}, { signal: new AbortController().signal });
      return { content: [{ type: "text", text: "done" }], details: {} };
    },
  });
}
`

// runner.ts:980 (`signal: options.signal ?? signal`): a nested call given its own signal is not cancelled with the calling tool's request; it runs until it ends on its own. TestPythonSDKExecuteToolOwnSignalOutlivesTheCallingRequest is the Python twin.
func TestNodeExecuteToolOwnSignalOutlivesTheCallingRequest(t *testing.T) {
	t.Parallel()
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		started, ended, release, owned := make(chan struct{}, 1), make(chan string, 1), make(chan struct{}), make(chan bool, 1)
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(ctx context.Context, callerID, name string, _ json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}}
				owned <- extension.OwnsSignal(ctx)
				started <- struct{}{}
				select {
				case <-ctx.Done():
					ended <- "Aborted"
					outcome.IsError = true
				case <-release:
					ended <- "released"
				}
				return outcome, nil
			},
		}
		rig := newNodeAPIRig(t, isolation, actions, nodeAPIFixture{"nodeapi-own-signal", nodeOwnSignalFixture})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		def := rig.exts["nodeapi-own-signal"].Tools["own_signal"]
		go func() { _, _ = def.Definition.Execute(ctx, "call-o", json.RawMessage(`{}`), nil) }()
		select {
		case <-started:
		case <-time.After(20 * time.Second):
			t.Fatal("the nested call did not start")
		}
		// runner.ts:979-981: the nested tool runs with the signal its caller passed, which the Host marks for the tool's runtime.
		if !<-owned {
			t.Error("the nested call with an explicit signal was not marked as running with it")
		}
		cancel()
		// The Host cancels the calls tied to a cancelled request at once; this one must still be running.
		select {
		case got := <-ended:
			t.Fatalf("the nested call ended with the calling request: %s", got)
		case <-time.After(500 * time.Millisecond):
		}
		close(release)
		select {
		case got := <-ended:
			if got != "released" {
				t.Fatalf("the nested call ended with %q, want it released", got)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the nested call did not end")
		}
	})
}

// runner.ts:980: without an explicit signal the nested call takes the calling tool's signal, so cancelling the calling request cancels it.
func TestNodeExecuteToolDefaultsToTheCallingToolSignal(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		started, ended := make(chan struct{}, 1), make(chan string, 1)
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(ctx context.Context, callerID, name string, _ json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				started <- struct{}{}
				<-ctx.Done()
				ended <- "Aborted"
				return extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}, IsError: true}, nil
			},
		}
		const fixture = `export default function (pi) {
  pi.registerTool({
    name: "inherit", label: "inherit", description: "Runs wait under the tool's signal", parameters: { type: "object" },
    execute: async (_id, _params, _signal, _onUpdate, ctx) => {
      await ctx.executeTool("wait", {});
      return { content: [{ type: "text", text: "done" }], details: {} };
    },
  });
}
`
		rig := newNodeAPIRig(t, isolation, actions, nodeAPIFixture{"nodeapi-inherit", fixture})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		def := rig.exts["nodeapi-inherit"].Tools["inherit"]
		go func() { _, _ = def.Definition.Execute(ctx, "call-i", json.RawMessage(`{}`), nil) }()
		select {
		case <-started:
		case <-time.After(20 * time.Second):
			t.Fatal("the nested call did not start")
		}
		cancel()
		select {
		case got := <-ended:
			if got != "Aborted" {
				t.Fatalf("ended with %q", got)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("cancelling the calling request did not cancel the nested call")
		}
	})
}
