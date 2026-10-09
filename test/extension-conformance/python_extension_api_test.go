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

// The Pi 0.99.1 extension API additions through the Python SDK and the real Host (.upstream/v0.99.1/packages/coding-agent/src/core/extensions/{types,loader,runner}.ts). Each row binds its assertion to a value only the Host or the Python SDK's answer can produce: a host-assigned extension path, a router's computed state, a description built from the loadout the Host sent, the id the host gave a nested call. A Python SDK that never makes the call, or that returns a default, cannot match.

var pyAPIIsolations = []string{"strict", "shared-ok"}

// pyAPIFixture is a Python extension of the API rows. Names are file-safe.
type pyAPIFixture struct{ name, code string }

type pyAPIRig struct {
	t      *testing.T
	host   *subprocess.Host
	bridge *subprocess.UIBridge
	ui     *recordingUI
	exts   map[string]extension.Extension
	runner *inproc.Runner
}

// newPyAPIRig loads the fixtures under one isolation: strict runs each in its own process, shared-ok packs them in one cell (upstream behavior must not depend on it).
func newPyAPIRig(t *testing.T, isolation string, actions *subprocess.HostCallbacks, fixtures ...pyAPIFixture) *pyAPIRig {
	t.Helper()
	root := findModuleRoot(t)
	notify, status := &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	if actions != nil {
		bridge.SetActions(actions)
	}
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetUIBridge(bridge)
	t.Cleanup(func() { h.Shutdown("test complete") })
	var configs []subprocess.ExtConfig
	for _, f := range fixtures {
		cfg := packedFlagFactory(t, root, "python", f.name, false)
		cfg.Isolation = isolation
		cfg.ContentHash = "python-extension-api-" + f.name + "-" + fmt.Sprint(len(f.code))
		if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(f.code), 0o600); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, cfg)
	}
	var loaded []extension.Extension
	if isolation == "shared-ok" {
		h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
		var err error
		loaded, err = h.Reload(t.Context())
		if err != nil {
			t.Fatalf("reload: %v (%+v)", err, h.LastReloadReport())
		}
		if report := h.LastReloadReport(); report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-python") {
			t.Fatalf("expected one packed python cell: %+v", report)
		}
	} else {
		var failures []error
		loaded, failures = h.LoadAll(t.Context(), configs)
		if len(failures) != 0 {
			t.Fatalf("load: %v", failures)
		}
	}
	rig := &pyAPIRig{t: t, host: h, bridge: bridge, ui: ui, exts: map[string]extension.Extension{}}
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

func eachPyAPIIsolation(t *testing.T, body func(t *testing.T, isolation string)) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts Python subprocesses)")
	}
	for _, isolation := range pyAPIIsolations {
		t.Run(isolation, func(t *testing.T) { body(t, isolation) })
	}
}

func (r *pyAPIRig) command(ext, name, args string) {
	r.t.Helper()
	command, ok := r.exts[ext].Commands[name]
	if !ok {
		r.t.Fatalf("%s has no command %s", ext, name)
	}
	if err := command.Handler(r.t.Context(), args); err != nil {
		r.t.Fatalf("%s /%s: %v", ext, name, err)
	}
}

// notifications returns what the extensions reported through ctx.notify, with its level stripped.
func (r *pyAPIRig) notifications() []string {
	var out []string
	for _, line := range r.ui.Recorded() {
		out = append(out, strings.TrimSuffix(line, ":info"))
	}
	return out
}

func (r *pyAPIRig) waitNotification(want string) {
	r.t.Helper()
	waitFor(r.t, func() bool { return slices.Contains(r.notifications(), want) })
}

func (r *pyAPIRig) execute(ext, tool, id string) (agent.AgentToolResult, error) {
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

const pyAPIPrelude = `import json, threading
import pig_sdk

def lines(ctx, *values):
    ctx.notify("|".join(str(v) for v in values))
`

// ── MCP servers ──────────────────────────────────────────────────────────────

const pyMcpFixture = pyAPIPrelude + `
def new_extension():
    e = pig_sdk.Extension("pyapi-mcp")
    e.register_mcp_server("py-load", {"url": "https://mcp.example/py-load"})
    e.register_mcp_server("py-dropped", {"url": "https://mcp.example/dropped"})
    e.unregister_mcp_server("py-dropped")

    def listing(ctx):
        return ",".join(sorted("%s@%s" % (s["name"], s["extensionPath"]) for s in ctx.get_mcp_servers()))

    def add(ctx, args):
        ctx.register_mcp_server("py-late", {"command": "py-mcp", "args": ["--stdio"]})
        lines(ctx, "after-add", listing(ctx))

    def drop(ctx, args):
        ctx.unregister_mcp_server("py-late")
        lines(ctx, "after-drop", listing(ctx))

    def steal(ctx, args):
        try:
            ctx.register_mcp_server("owned-by-sibling", {"url": "https://mcp.example/stolen"})
            lines(ctx, "steal", "no error")
        except pig_sdk.HostCallError as exc:
            lines(ctx, "steal", exc.message)

    def invalid(ctx, args):
        try:
            ctx.register_mcp_server("py-invalid", {})
            lines(ctx, "invalid", "no error")
        except pig_sdk.HostCallError as exc:
            lines(ctx, "invalid", exc.message)

    def show(ctx, args):
        lines(ctx, "show", listing(ctx))

    e.command("add", "", add)
    e.command("drop", "", drop)
    e.command("steal", "", steal)
    e.command("invalid", "", invalid)
    e.command("show", "", show)
    return e
`

const pyMcpSiblingFixture = `import pig_sdk

def new_extension():
    e = pig_sdk.Extension("pyapi-sibling")
    e.register_mcp_server("owned-by-sibling", {"url": "https://mcp.example/sibling"})
    return e
`

// loader.ts:456-478 and 259-262: servers registered while the factory ran reach the runtime owned by the extension's path, a queued unregister removes its server, and after load the calls apply at once. getMcpServers lists the registry, including the path the Host assigned.
func TestPythonSDKMcpServerRegistration(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil, pyAPIFixture{"pyapi-mcp", pyMcpFixture}, pyAPIFixture{"pyapi-sibling", pyMcpSiblingFixture})
		mcpPath, siblingPath := rig.exts["pyapi-mcp"].Path, rig.exts["pyapi-sibling"].Path
		registered := func() []string {
			var out []string
			for _, s := range rig.host.Runtime().McpServers().List() {
				out = append(out, s.Name+"@"+s.ExtensionPath)
			}
			slices.Sort(out)
			return out
		}
		want := []string{"owned-by-sibling@" + siblingPath, "py-load@" + mcpPath}
		if got := registered(); !slices.Equal(got, want) {
			t.Fatalf("after load: %v, want %v", got, want)
		}
		rig.command("pyapi-mcp", "show", "")
		rig.waitNotification("show|" + strings.Join(want, ","))

		rig.command("pyapi-mcp", "add", "")
		want = []string{"owned-by-sibling@" + siblingPath, "py-late@" + mcpPath, "py-load@" + mcpPath}
		rig.waitNotification("after-add|" + strings.Join(want, ","))
		if got := registered(); !slices.Equal(got, want) {
			t.Fatalf("after add: %v, want %v", got, want)
		}
		var late extension.RegisteredMcpServer
		for _, s := range rig.host.Runtime().McpServers().List() {
			if s.Name == "py-late" {
				late = s
			}
		}
		if late.Config.Command != "py-mcp" || !slices.Equal(late.Config.Args, []string{"--stdio"}) {
			t.Fatalf("py-late config = %+v", late.Config)
		}

		rig.command("pyapi-mcp", "drop", "")
		want = []string{"owned-by-sibling@" + siblingPath, "py-load@" + mcpPath}
		rig.waitNotification("after-drop|" + strings.Join(want, ","))

		// loader.ts:463-470: a name another extension owns and an invalid config throw to the registering extension.
		rig.command("pyapi-mcp", "steal", "")
		rig.waitNotification(`steal|MCP server "owned-by-sibling" is already registered by extension "` + siblingPath + `"`)
		rig.command("pyapi-mcp", "invalid", "")
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
	})
}

// ── Virtual models ───────────────────────────────────────────────────────────

const pyVirtualModelFixture = pyAPIPrelude + `
def route(ctx, request):
    n = (request.get("state") or {}).get("n", 0)
    return {
        "model": {"provider": "anthropic", "id": "py-picked-" + request["reason"]},
        "thinkingLevel": request["thinkingLevel"],
        "state": {"n": n + 5, "saw": len(request["messages"]), "cancelled": request["signal"].is_set()},
    }

def new_extension():
    e = pig_sdk.Extension("pyapi-vm")
    e.register_virtual_model(pig_sdk.VirtualModel(
        provider="pyrouter", id="auto", name="Py Auto", route=route,
        thinking_levels=["off", "low"], context_window=123456, max_tokens=4321, input=["text"]))
    e.register_virtual_model(pig_sdk.VirtualModel(provider="pyrouter", id="dropped", name="Dropped", route=route))
    e.unregister_virtual_model("pyrouter", "dropped")
    e.command("add", "", lambda ctx, args: ctx.register_virtual_model(pig_sdk.VirtualModel(provider="pyrouter", id="late", name="Late", route=route)))
    e.command("drop", "", lambda ctx, args: ctx.unregister_virtual_model("pyrouter", "auto"))
    return e
`

// pi.registerVirtualModel (packages/coding-agent/src/core/extensions/types.ts:1872, loader.ts:500-509) and unregisterVirtualModel (packages/coding-agent/src/core/extensions/types.ts:1875, loader.ts:511-514), with virtual-models.ts:87-101: the declaration reaches the runtime queue without its route, the route runs in the extension against the request the Host sends, and the route names the physical model by provider and id for the model runtime to resolve (model-runtime.ts:1000-1009). The value in the state is computed by the router from the state it was sent.
func TestPythonSDKVirtualModelRegistrationAndRouting(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, &subprocess.HostCallbacks{}, pyAPIFixture{"pyapi-vm", pyVirtualModelFixture})
		pending := func() map[string]extension.PendingVirtualModelRegistration {
			out := map[string]extension.PendingVirtualModelRegistration{}
			for _, p := range rig.host.Runtime().PendingVirtualModelRegistrations() {
				out[p.Definition.Provider+"/"+p.Definition.ID] = p
			}
			return out
		}
		got := pending()
		if len(got) != 1 {
			t.Fatalf("registerVirtualModel/unregisterVirtualModel: registered %v, want only pyrouter/auto (dropped was unregistered while the factory ran)", got)
		}
		p := got["pyrouter/auto"]
		def := p.Definition
		if def.Name != "Py Auto" || def.ContextWindow != 123456 || def.MaxTokens != 4321 || len(def.ThinkingLevels) != 2 || def.ThinkingLevels[1] != ai.ThinkingLow || !slices.Equal(def.Input, []string{"text"}) || def.Route == nil {
			t.Fatalf("definition = %+v", def)
		}
		if p.ExtensionPath != rig.exts["pyapi-vm"].Path {
			t.Fatalf("extension path = %q, want %q", p.ExtensionPath, rig.exts["pyapi-vm"].Path)
		}
		route, err := def.Route(t.Context(), extension.ModelRouteRequest{
			Model: &ai.Model{ID: "auto"}, ThinkingLevel: ai.ThinkingLow, Reason: extension.ModelRouteReasonUser,
			State:    json.RawMessage(`{"n":4}`),
			Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}, ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "again"}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if route.Model == nil || route.Model.ProviderMeta.ProviderID != "anthropic" || route.Model.ID != "py-picked-user" || route.ThinkingLevel != ai.ThinkingLow || string(route.State) != `{"n":9,"saw":2,"cancelled":false}` {
			t.Fatalf("route = model %v thinking %q state %s", route.Model, route.ThinkingLevel, route.State)
		}
		// model-runtime.ts:1000-1009: a router that names a model the registry does not have still returns it as answered; the model runtime, not the router call, rejects it with the pair it named.
		unknown, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "auto"}, Reason: extension.ModelRouteReasonRetry})
		if err != nil || unknown.Model == nil || unknown.Model.ProviderMeta.ProviderID != "anthropic" || unknown.Model.ID != "py-picked-retry" {
			t.Fatalf("unknown physical model route = %+v, %v", unknown, err)
		}

		rig.command("pyapi-vm", "add", "")
		if got := pending(); len(got) != 2 || got["pyrouter/late"].Definition.Name != "Late" {
			t.Fatalf("after add: %v", got)
		}
		rig.command("pyapi-vm", "drop", "")
		if got := pending(); len(got) != 1 || got["pyrouter/late"].Definition.Name != "Late" {
			t.Fatalf("after drop: %v", got)
		}
	})
}

// ── Tool definition fields, structured results, settings, provider config ────

const pyToolsFixture = pyAPIPrelude + `
def prepare(loadout):
    grep = loadout.get_namespace("grep") or {}
    return {
        "descriptions": {"read": "py: %d callable, %d registered, grep is %s in %s, absent is %s, grep guidelines %s, absent guidelines %s" % (
            len(loadout.callable), len(loadout.registered), loadout.get_exposure("grep"), grep.get("name"), loadout.get_exposure("absent"),
            json.dumps(loadout.get_prompt_guidelines("grep")), json.dumps(loadout.get_prompt_guidelines("absent")))},
        "hiddenDeclarations": [t["name"] for t in loadout.declared if t["name"] != "read"],
    }

def new_extension():
    e = pig_sdk.Extension("pyapi-tools")
    e.register_tool(pig_sdk.ToolDefinition(
        name="search", label="Search", description="Search docs", parameters={"type": "object"},
        output_schema={"type": "object", "properties": {"rows": {"type": "array"}}},
        exposure="codemode", namespace={"name": "mcp__py", "description": "Py server", "instructions": "Search the py docs."},
        annotations={"readOnlyHint": True, "openWorldHint": False}, default_active=False,
        prepare_loadout=prepare, execute=lambda ctx, args: "unused"))
    e.register_tool(pig_sdk.ToolDefinition(
        name="plain", label="Plain", description="Plain", parameters={"type": "object"}, execute=lambda ctx, args: "plain"))
    e.tool("structured", "Structured result", {"type": "object"}, lambda ctx, args: {
        "content": "model-visible text", "structured_content": {"rows": [3, 1, 4]}, "is_error": True, "details": {"k": "v"}})
    e.command("settings", "", lambda ctx, args: lines(ctx, "settings", ctx.get_settings()["marker"], json.dumps(ctx.get_settings()["nested"], sort_keys=True)))
    e.register_provider("pyapi-multi", {
        "baseUrl": "https://api.example/v1",
        "models": [
            {"id": "chat-1", "name": "Chat", "api": "openai-completions", "reasoning": False, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100},
            {"type": "image", "id": "img-1", "name": "Image", "api": "openai-images", "output": ["image", "text"], "input": ["text"]},
            {"type": "classifier", "id": "cls-1", "name": "Classifier", "api": "llama-cpp-classify", "input": ["text"], "contextWindow": 512},
        ]})
    return e
`

// types.ts:579-607, 601-607 and agent-session.ts:1480-1518: a tool's outputSchema, exposure, namespace, annotations and defaultActive reach the Host's definition; prepareLoadout runs in the extension against the loadout the Host builds, and its answer carries values computed from that loadout.
func TestPythonSDKToolExposureFieldsAndPrepareLoadout(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil, pyAPIFixture{"pyapi-tools", pyToolsFixture})
		search := rig.exts["pyapi-tools"].Tools["search"].Definition
		if string(search.OutputSchema) != `{"type":"object","properties":{"rows":{"type":"array"}}}` || search.Exposure != extension.ToolExposureCodemode {
			t.Fatalf("outputSchema=%s exposure=%q", search.OutputSchema, search.Exposure)
		}
		if search.Namespace == nil || search.Namespace.Name != "mcp__py" || search.Namespace.Description != "Py server" || search.Namespace.Instructions != "Search the py docs." {
			t.Fatalf("namespace = %+v", search.Namespace)
		}
		if a := search.Annotations; a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint || a.DestructiveHint != nil || a.IdempotentHint != nil {
			t.Fatalf("annotations = %+v", search.Annotations)
		}
		if search.DefaultActive == nil || *search.DefaultActive {
			t.Fatalf("defaultActive = %v, want false", search.DefaultActive)
		}
		plain := rig.exts["pyapi-tools"].Tools["plain"].Definition
		if plain.PrepareLoadout != nil || plain.Exposure != "" || plain.DefaultActive != nil || plain.OutputSchema != nil || plain.Namespace != nil || plain.Annotations != nil {
			t.Fatalf("an undeclared field was set on %+v", plain)
		}
		if search.PrepareLoadout == nil {
			t.Fatal("a tool that defines prepare_loadout has no PrepareLoadout")
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
		wantDescription := `py: 1 callable, 2 registered, grep is deferred in search-ns, absent is direct, grep guidelines ["Use grep for patterns.", "Quote regexes."], absent guidelines []`
		if changes == nil || changes.Descriptions["read"] != wantDescription || !slices.Equal(changes.HiddenDeclarations, []string{"grep"}) {
			t.Fatalf("changes = %+v, want description %q hiding grep", changes, wantDescription)
		}
	})
}

// agent/src/types.ts:424-446: structuredContent and isError survive the SDK, the wire and the Host's tool result.
func TestPythonSDKToolResultCarriesStructuredContentAndIsError(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil, pyAPIFixture{"pyapi-tools", pyToolsFixture})
		result, err := rig.execute("pyapi-tools", "structured", "tc-structured")
		if err != nil {
			t.Fatal(err)
		}
		if string(result.StructuredContent) != `{"rows":[3,1,4]}` || !result.IsError || result.Text() != "model-visible text" {
			t.Fatalf("result = structured %s isError %v text %q", result.StructuredContent, result.IsError, result.Text())
		}
	})
}

// types.ts:1712 and loader.ts:411-413: getSettings returns the settings object the Host holds, read from the state the Host replicated.
func TestPythonSDKGetSettings(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, &subprocess.HostCallbacks{GetSettings: func() extension.Settings {
			return extension.Settings{"marker": "py-settings-value-42", "nested": map[string]any{"b": []any{1, 2}, "a": true}}
		}}, pyAPIFixture{"pyapi-tools", pyToolsFixture})
		rig.command("pyapi-tools", "settings", "")
		rig.waitNotification(`settings|py-settings-value-42|{"a": true, "b": [1, 2]}`)
	})
}

// types.ts:1929-1991: a provider registered with chat, image and classifier model entries keeps each entry's type and its variant's fields.
func TestPythonSDKProviderConfigWithImageAndClassifierModels(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil, pyAPIFixture{"pyapi-tools", pyToolsFixture})
		var config *extension.ProviderConfig
		for _, p := range rig.host.Runtime().PendingProviderRegistrations() {
			if p.Name == "pyapi-multi" {
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

// ── ctx.tools, ctx.execute_tool and parentToolCallId ─────────────────────────

const pyNestFixture = pyAPIPrelude + `
def new_extension():
    e = pig_sdk.Extension("pyapi-nest")

    def nest(ctx, args):
        seen = []
        out = ctx.execute_tool("echo", {"v": 7}, pig_sdk.ExecuteToolOptions(on_update=lambda p: seen.append(p["content"][0]["text"])))
        names = ",".join(t["name"] for t in ctx.tools)
        return "%s|%s|%s|%s|%s" % (names, out["toolCall"]["id"], out["result"]["content"][0]["text"], out["isError"], ",".join(seen))

    def cancel_nest(ctx, args):
        signal = pig_sdk.ProviderSignal()
        timer = threading.Timer(0.2, signal.set)
        timer.start()
        out = ctx.execute_tool("wait", {}, pig_sdk.ExecuteToolOptions(signal=signal))
        return "isError=%s text=%s" % (out["isError"], out["result"]["content"][0]["text"])

    e.tool("nest", "Runs echo", {"type": "object"}, nest)
    e.tool("cancel_nest", "Cancels wait", {"type": "object"}, cancel_nest)
    e.command("tools-outside", "", lambda ctx, args: lines(ctx, "outside", _outside(ctx)))

    def _outside(ctx):
        try:
            ctx.tools
            return "tools available"
        except RuntimeError as exc:
            return str(exc)

    e.on_event("tool_call", lambda ctx, event: lines(ctx, "tool_call", event["toolCallId"], event.get("parentToolCallId")))
    e.on_event("tool_result", lambda ctx, event: lines(ctx, "tool_result", event["toolCallId"], event.get("parentToolCallId"), json.dumps(event.get("structuredContent"), sort_keys=True)))
    e.on_event("provider_stream_event", lambda ctx, event: lines(ctx, "provider_stream_event", event["provider"], event["api"], event["model"], json.dumps(event["data"], sort_keys=True)))
    e.on_event("mcp_servers_change", lambda ctx, event: lines(ctx, "mcp_servers_change", ",".join(s["name"] for s in event["servers"])))
    return e
`

// types.ts:367-395 and runner.ts:952-985: a tool's context lists the tools the Host reports as callable and runs another tool through the Host, which names the nested call after the calling one, streams partial results in order before the outcome, and returns the outcome untouched.
func TestPythonSDKExecuteToolAndCallableTools(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
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
		rig := newPyAPIRig(t, isolation, actions, pyAPIFixture{"pyapi-nest", pyNestFixture})
		result, err := rig.execute("pyapi-nest", "nest", "call-7")
		if err != nil || result.IsError {
			t.Fatalf("nest = %+v, %v", result, err)
		}
		// mis-port fix (red expected `false`): the fixture formats isError with Python's %s, which prints False.
		if want := `echo,grep|call-7/1|host ran echo {"v":7}|False|partial-one,partial-two`; result.Text() != want {
			t.Fatalf("nest text = %q, want %q", result.Text(), want)
		}

		// The option signal cancels the nested call only; the calling tool continues and reads the aborted outcome.
		result, err = rig.execute("pyapi-nest", "cancel_nest", "call-8")
		if err != nil {
			t.Fatal(err)
		}
		if result.Text() != "isError=True text=aborted" {
			t.Fatalf("cancel_nest text = %q", result.Text())
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(seen, []string{"wait cancelled"}) {
			t.Fatalf("host saw %v", seen)
		}
	})
}

// types.ts:385-395 (ExtensionToolContext only) and 1044-1219 (parentToolCallId, structuredContent), 699-709 and 884-890 (the new events): outside a tool call the context has no `tools`, tool events carry the parent id and structured content, and the new events reach the handler with the fields the Host set.
func TestPythonSDKToolEventsAndNewEvents(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil, pyAPIFixture{"pyapi-nest", pyNestFixture})
		rig.command("pyapi-nest", "tools-outside", "")
		rig.waitNotification("outside|tools is only available while a tool runs")

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
		rig.waitNotification(`tool_result|outer-1/3|outer-1|{"rows": [9, 8]}`)

		if _, err := rig.runner.Emit(t.Context(), extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "prov-x", API: "api-y", Model: "model-z", Data: map[string]any{"delta": []any{1, "two"}}}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification(`provider_stream_event|prov-x|api-y|model-z|{"delta": [1, "two"]}`)

		servers := []extension.RegisteredMcpServer{{Name: "one", ExtensionPath: "/x"}, {Name: "two", ExtensionPath: "/y"}}
		if _, err := rig.runner.Emit(t.Context(), extension.McpServersChangeEvent{Type: "mcp_servers_change", Servers: servers}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification("mcp_servers_change|one,two")
	})
}

const pyOwnSignalFixture = `import pig_sdk

def new_extension():
    e = pig_sdk.Extension("pyapi-own-signal")

    def own(ctx, args):
        ctx.execute_tool("wait_release", {}, pig_sdk.ExecuteToolOptions(signal=pig_sdk.ProviderSignal()))
        return "done"

    e.tool("own_signal", "Runs wait_release under its own signal", {"type": "object"}, own)
    return e
`

// runner.ts:980 (`signal: options.signal ?? signal`): a nested call given its own signal is not cancelled with the calling tool's request; it runs until it ends on its own. TestExtensionAPIExecuteToolOwnSignalOutlivesTheCallingRequestGo is the Go SDK's row.
func TestPythonSDKExecuteToolOwnSignalOutlivesTheCallingRequest(t *testing.T) {
	t.Parallel()
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
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
		rig := newPyAPIRig(t, isolation, actions, pyAPIFixture{"pyapi-own-signal", pyOwnSignalFixture})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		def := rig.exts["pyapi-own-signal"].Tools["own_signal"]
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

// pyBusSiblingFixture is one of two Python extensions that share a packed cell and so one realm.
const pyBusSiblingFixture = pyAPIPrelude + `
NAME = %q

def new_extension():
    e = pig_sdk.Extension(NAME)
    subs = []
    e.command("sub", "", lambda ctx, args: subs.append(ctx.events.on("ch", lambda c, data: lines(c, NAME, data))))
    e.command("unsub", "", lambda ctx, args: subs.pop()())
    e.command("emit", "", lambda ctx, args: ctx.events.emit("ch", args))
    return e
`

// The Host keys a pi.events listener by realm (one OS process) and handler ID, and a packed cell runs its extensions in one process. Each extension's unsubscribe removes only its own listener (event-bus.ts:27, EventEmitter.off), wherever the extensions run.
func TestPythonSDKEventBusSiblingsInOneCellKeepTheirOwnListeners(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil,
			pyAPIFixture{"pyapi-bus-a", fmt.Sprintf(pyBusSiblingFixture, "pyapi-bus-a")},
			pyAPIFixture{"pyapi-bus-b", fmt.Sprintf(pyBusSiblingFixture, "pyapi-bus-b")},
		)
		rig.command("pyapi-bus-a", "sub", "")
		rig.command("pyapi-bus-b", "sub", "")
		rig.command("pyapi-bus-a", "emit", "one")
		if got, want := rig.notifications(), []string{"pyapi-bus-a|one", "pyapi-bus-b|one"}; !slices.Equal(got, want) {
			t.Fatalf("after the first emit: %q, want %q", got, want)
		}
		rig.command("pyapi-bus-a", "unsub", "")
		rig.command("pyapi-bus-b", "emit", "two")
		if got, want := rig.notifications(), []string{"pyapi-bus-a|one", "pyapi-bus-b|one", "pyapi-bus-b|two"}; !slices.Equal(got, want) {
			t.Fatalf("after pyapi-bus-a unsubscribed: %q, want %q", got, want)
		}
	})
}
