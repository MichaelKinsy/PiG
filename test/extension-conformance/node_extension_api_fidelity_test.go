package extensionconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Where the Node runtime departs from Pi 0.99.1 in how the extension API behaves while the factory runs and while a handler runs (review of port-99-f6f-node, items B-G). Each row binds its assertion to a value only the Host can produce or that Pi's own ordering produces, so a runtime that defers the work, or answers from state it replicated earlier, cannot match.

// ── B and C: registerMcpServer and getMcpServers inside the factory ──────────

const nodeMcpSiblingOwnerFixture = `export default function (pi) {
  pi.registerMcpServer("owned-by-sibling", { url: "https://mcp.example/sibling" });
}
`

const nodeMcpFactoryCatchFixture = nodeAPIPrelude + `
export default function (pi) {
  const outcomes = [];
  const attempt = (name, config) => {
    try { pi.registerMcpServer(name, config); outcomes.push(name + ": no throw"); } catch (error) { outcomes.push(name + ": " + error.message); }
  };
  attempt("bad", {});
  attempt("owned-by-sibling", { url: "https://mcp.example/stolen" });
  attempt("after-catch", { url: "https://mcp.example/after-catch" });
  pi.registerCommand("outcomes", { handler: async (_args, ctx) => lines(ctx, "outcomes", ...outcomes) });
}
`

// loader.ts:456-468: registerMcpServer validates the config and checks ownership when it is called and throws to the factory, so a factory that catches the throw still loads, keeps its later registrations, and the rejected server is never registered. The messages are the ones the registering extension sees, with its own path.
func TestNodeFactoryMcpRegistrationThrowsInsideTheFactory(t *testing.T) {
	t.Parallel()
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-owner", nodeMcpSiblingOwnerFixture}, nodeAPIFixture{"nodeapi-catch", nodeMcpFactoryCatchFixture})
		ownerPath, catchPath := rig.exts["nodeapi-owner"].Path, rig.exts["nodeapi-catch"].Path
		var registered []string
		for _, server := range rig.host.Runtime().McpServers().List() {
			registered = append(registered, server.Name+"@"+server.ExtensionPath)
		}
		slices.Sort(registered)
		if want := []string{"after-catch@" + catchPath, "owned-by-sibling@" + ownerPath}; !slices.Equal(registered, want) {
			t.Fatalf("registered %v, want %v", registered, want)
		}
		_, message := extension.ValidateMcpServerConfig("bad", json.RawMessage(`{}`))
		if message == "" {
			t.Fatal("the validator accepted an empty config")
		}
		rig.command("nodeapi-catch", "outcomes", "")
		rig.waitNotification(strings.Join([]string{
			"outcomes",
			`bad: Invalid MCP server registered by extension "` + catchPath + `": ` + message,
			`owned-by-sibling: MCP server "owned-by-sibling" is already registered by extension "` + ownerPath + `"`,
			"after-catch: no throw",
		}, "|"))
	})
}

const nodeMcpFactoryListFixture = nodeAPIPrelude + `
export default function (pi) {
  pi.registerMcpServer("own-pending", { url: "https://mcp.example/own-pending" });
  const during = pi.getMcpServers().map((s) => s.name + "@" + s.extensionPath).join(",");
  pi.registerCommand("during", { handler: async (_args, ctx) => lines(ctx, "during", during) });
}
`

// loader.ts:475-478: getMcpServers returns the live registry, which holds the servers earlier-loaded extensions committed and not the ones this factory has only queued (applyRuntimeChange, loader.ts:259-262). The list is the registry's at the time of the call, with paths only the Host assigns.
func TestNodeGetMcpServersInsideTheFactoryListsTheLiveRegistry(t *testing.T) {
	t.Parallel()
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-owner", nodeMcpSiblingOwnerFixture}, nodeAPIFixture{"nodeapi-list", nodeMcpFactoryListFixture})
		rig.command("nodeapi-list", "during", "")
		rig.waitNotification("during|owned-by-sibling@" + rig.exts["nodeapi-owner"].Path)
	})
}

// ── D: unregisterVirtualModel before the runner binds ────────────────────────

const nodeVirtualModelOwnerFixture = `const route = async () => ({ model: { provider: "anthropic", id: "x" }, thinkingLevel: "off" });
export default function (pi) {
  pi.registerVirtualModel({ provider: "noderouter", id: "victim", name: "Victim", route });
  pi.registerVirtualModel({ provider: "noderouter", id: "kept", name: "Kept", route });
}
`

const nodeVirtualModelRemoverFixture = `export default function (pi) {
  pi.unregisterVirtualModel("noderouter", "victim");
}
`

// pi.unregisterVirtualModel (packages/coding-agent/src/core/extensions/types.ts:1875, loader.ts:511-514) reaches loader.ts:229-233: before the runner binds, unregisterVirtualModel filters the runtime-wide pending list, so a virtual model an earlier-loaded extension queued is removed too.
func TestNodeUnregisterVirtualModelBeforeBindFiltersTheRuntimeWidePendingList(t *testing.T) {
	t.Parallel()
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-vm-owner", nodeVirtualModelOwnerFixture}, nodeAPIFixture{"nodeapi-vm-remover", nodeVirtualModelRemoverFixture})
		var pending []string
		for _, p := range rig.host.Runtime().PendingVirtualModelRegistrations() {
			pending = append(pending, p.Definition.Provider+"/"+p.Definition.ID)
		}
		if want := []string{"noderouter/kept"}; !slices.Equal(pending, want) {
			t.Fatalf("unregisterVirtualModel: pending virtual models %v, want %v", pending, want)
		}
	})
}

// ── E: router state identity ────────────────────────────────────────────────

const nodeVirtualStateIdentityFixture = `export default function (pi) {
  pi.registerVirtualModel({ provider: "noderouter", id: "identity", name: "Identity", route: async (request) => ({
    model: { provider: "anthropic", id: "node-picked-user" }, thinkingLevel: "off", state: request.state,
  }) });
}
`

// agent-session.ts:788: the session stores a router's state unless `route.state === request.state`. A router that returns the state it was given keeps it, whatever the bytes that crossed the process boundary look like: the Host sent the state HTML-escaped (Go's encoding of a RawMessage) and Node answers with JSON.stringify's bytes, which differ for <, > and &.
func TestNodeVirtualModelRouterReturningItsRequestStateKeepsTheRequestStateBytes(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, &subprocess.HostCallbacks{}, nodeAPIFixture{"nodeapi-identity", nodeVirtualStateIdentityFixture})
		def := rig.host.Runtime().PendingVirtualModelRegistrations()[0].Definition
		for _, state := range []string{`{"a":"\u003cb\u003e\u0026","n":1}`, `{"a":"<b>&","n":1}`, `"\u003c"`, `7`} {
			route, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "identity"}, Reason: extension.ModelRouteReasonContinuation, State: json.RawMessage(state)})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(route.State, json.RawMessage(state)) {
				t.Fatalf("a router that returned its request state %s produced %s, which the session would store as a new state", state, route.State)
			}
		}
	})
}

const nodeVirtualStateCopyFixture = `export default function (pi) {
  pi.registerVirtualModel({ provider: "noderouter", id: "copy", name: "Copy", route: async (request) => ({
    model: { provider: "anthropic", id: "node-picked-user" }, thinkingLevel: "off", state: { ...request.state },
  }) });
}
`

// agent-session.ts:788 compares with `===`, so a router that returns a fresh object stores it even when it holds the request state's values. Node observes that identity, so its route reports no unchanged state and the Host keeps the router's bytes: the session sees JSON.stringify's `<b>&`, which differs from the HTML-escaped state it sent, and stores it as Pi does.
func TestNodeVirtualModelRouterReturningAFreshEqualStateIsStored(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, &subprocess.HostCallbacks{}, nodeAPIFixture{"nodeapi-copy", nodeVirtualStateCopyFixture})
		def := rig.host.Runtime().PendingVirtualModelRegistrations()[0].Definition
		sent := json.RawMessage(`{"a":"\u003cb\u003e\u0026","n":1}`)
		route, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "copy"}, Reason: extension.ModelRouteReasonContinuation, State: sent})
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"a":"<b>&","n":1}`; string(route.State) != want {
			t.Fatalf("a router that returned a fresh copy of its request state produced %s, want its own bytes %s so the session stores it", route.State, want)
		}
	})
}

// ── F: ctx.tools is live ─────────────────────────────────────────────────────

const nodeLiveToolsFixture = nodeAPIPrelude + `
export default function (pi) {
  pi.registerTool({
    name: "live_tools", label: "live_tools", description: "Reads ctx.tools around setActiveTools", parameters: { type: "object" },
    execute: async (_id, _params, _signal, _onUpdate, ctx) => {
      const names = () => ctx.tools.map((tool) => tool.name).join(",");
      const before = names();
      pi.setActiveTools(["only"]);
      const after = names();
      return { content: [{ type: "text", text: before + "|" + after + "|" + names() }], details: {} };
    },
  });
}
`

// runner.ts:958-961: the ctx.tools getter reads the callable tools when it is read, so a read after setActiveTools inside the same handler sees the change. The host keeps the active set; the extension can only learn it by asking.
func TestNodeCtxToolsIsLiveInsideOneHandler(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		var mu sync.Mutex
		active := []string{"a", "b", "c"}
		actions := &subprocess.HostCallbacks{
			SetActiveTools: func(names []string) {
				mu.Lock()
				active = slices.Clone(names)
				mu.Unlock()
			},
			GetCallableTools: func() []extension.AgentTool {
				mu.Lock()
				defer mu.Unlock()
				var tools []extension.AgentTool
				for _, name := range active {
					tools = append(tools, extension.AgentTool{Name: name, Label: name, Parameters: json.RawMessage(`{"type":"object"}`)})
				}
				return tools
			},
		}
		rig := newNodeAPIRig(t, isolation, actions, nodeAPIFixture{"nodeapi-live", nodeLiveToolsFixture})
		result, err := rig.execute("nodeapi-live", "live_tools", "call-l")
		if err != nil || result.IsError {
			t.Fatalf("live_tools = %+v, %v", result, err)
		}
		if want := "a,b,c|only|only"; result.Text() != want {
			t.Fatalf("ctx.tools read %q, want %q", result.Text(), want)
		}
	})
}

// ── G: a throwing executeTool onUpdate rejects the call ─────────────────────

const nodeNestedThrowFixture = nodeAPIPrelude + `
export default function (pi) {
  pi.registerTool({
    name: "nest_throw", label: "nest_throw", description: "Throws from onUpdate", parameters: { type: "object" },
    execute: async (_id, _params, _signal, _onUpdate, ctx) => {
      const seen = [];
      try {
        await ctx.executeTool("slow", {}, { onUpdate: (partial) => { seen.push(partial.content[0].text); if (seen.length === 1) throw new Error("callback boom"); } });
        return { content: [{ type: "text", text: "no rejection after " + seen.join(",") }], details: {} };
      } catch (error) {
        return { content: [{ type: "text", text: "rejected: " + error.message + " after " + seen.join(",") }], details: {} };
      }
    },
  });
  pi.registerTool({
    name: "nest_duration", label: "nest_duration", description: "Reports the durationMs of a nested outcome", parameters: { type: "object" },
    execute: async (_id, _params, _signal, _onUpdate, ctx) => {
      const outcome = await ctx.executeTool("timed", {});
      return { content: [{ type: "text", text: String(outcome.durationMs) }], details: {} };
    },
  });
}
`

// types.ts:454 and nested-tool-calls.ts:246: executeTool's outcome carries the nested call's durationMs.
func TestNodeExecuteToolOutcomeCarriesDuration(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(context.Context, string, string, json.RawMessage, extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				return extension.AgentToolCallOutcome{DurationMs: new(int64(4321))}, nil
			},
		}
		rig := newNodeAPIRig(t, isolation, actions, nodeAPIFixture{"nodeapi-throw", nodeNestedThrowFixture})
		result, err := rig.execute("nodeapi-throw", "nest_duration", "call-d")
		if err != nil || result.IsError || result.Text() != "4321" {
			t.Fatalf("nest_duration = %+v, %v, want 4321", result, err)
		}
	})
}

// nested-tool-calls.ts:219-248 and agent-loop.ts:820-849: a throw from the caller's onUpdate rejects the nested call after the tool returned. The call never reaches afterToolCall or tool_execution_end, every partial result still reaches onUpdate, and the extension sees the rejection. The Host's ExecuteTool action stands in for the session: it keeps Pi's rule for the error its update sink returns, so what the row proves is that the Host hands the sink the extension's throw before the next update.
func TestNodeExecuteToolOnUpdateThrowRejectsTheNestedCall(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		var mu sync.Mutex
		var sinkResults []string
		var completed []string
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(_ context.Context, callerID, name string, _ json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				sink := options.OnUpdate
				if sink == nil {
					return extension.AgentToolCallOutcome{}, nil
				}
				var first error
				for _, step := range []string{"one", "two"} {
					err := sink(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: step}}, Details: map[string]any{"step": step}})
					mu.Lock()
					if err != nil {
						sinkResults = append(sinkResults, step+": "+err.Error())
					} else {
						sinkResults = append(sinkResults, step+": ok")
					}
					mu.Unlock()
					if err != nil && first == nil {
						first = err
					}
				}
				if first != nil {
					return extension.AgentToolCallOutcome{}, first
				}
				mu.Lock()
				completed = append(completed, name)
				mu.Unlock()
				return extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}, Result: agent.AgentToolResult{}}, nil
			},
		}
		rig := newNodeAPIRig(t, isolation, actions, nodeAPIFixture{"nodeapi-throw", nodeNestedThrowFixture})
		result, err := rig.execute("nodeapi-throw", "nest_throw", "call-t")
		if err != nil || result.IsError {
			t.Fatalf("nest_throw = %+v, %v", result, err)
		}
		if want := "rejected: callback boom after one,two"; result.Text() != want {
			t.Fatalf("the extension saw %q, want %q", result.Text(), want)
		}
		mu.Lock()
		defer mu.Unlock()
		if want := []string{"one: callback boom", "two: ok"}; !slices.Equal(sinkResults, want) {
			t.Fatalf("the update sink returned %v, want %v", sinkResults, want)
		}
		if len(completed) != 0 {
			t.Fatalf("the call completed %v although its update callback threw", completed)
		}
	})
}
