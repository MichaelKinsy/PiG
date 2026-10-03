package subprocess

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Node runtime's side of the Pi 0.99.1 extension API, without a Host: what the register frame carries, what each call sends and how the replies and requests are handled. test/extension-conformance/node_extension_api_test.go proves the same capabilities against the real Host.
//
// upstream: .upstream/v0.99.1/packages/coding-agent/src/core/extensions/loader.ts:411-414, 456-497; runner.ts:952-985; types.ts:534-611
func TestNodeRuntimeExtensionAPI(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import net from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Runtime, requestSignal } from %q;

// The register frame: queued MCP servers keep a replaced name's position and lose an unregistered one; virtual models travel without their route; tool fields travel under the wire names.
const sock = process.platform === "win32"
  ? "\\\\.\\pipe\\pig-extension-api-" + process.pid
  : join(tmpdir(), "pig-extension-api-" + process.pid + ".sock");
const frame = new Promise((resolve, reject) => {
  const server = net.createServer((socket) => {
    let buffer = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      if (buffer.length < 4) return;
      const size = buffer.readUInt32BE(0);
      if (buffer.length < 4 + size) return;
      resolve(JSON.parse(buffer.subarray(4, 4 + size).toString("utf8")));
      socket.destroy();
      server.close();
    });
  });
  server.on("error", reject);
  server.listen(sock);
});
process.env.PIG_EXT_SOCKET = sock;
const loading = new Runtime("/ext/api.mjs");
// The factory's registration calls reach the Host before the register frame (loader.ts:456-468 throws when the call is made); the Host is not part of this test.
const factoryCalls = [];
loading.factoryCall = (method, args) => { factoryCalls.push([method, args]); return {}; };
const route = () => ({});
loading.api.registerMcpServer("a", { url: "https://a.example" });
loading.api.registerMcpServer("b", { url: "https://b.example" });
loading.api.registerMcpServer("a", { url: "https://a2.example" });
loading.api.registerMcpServer("c", { url: "https://c.example" });
loading.api.unregisterMcpServer("c");
// Pi reads only the VirtualModelDefinition fields (virtual-models.ts:84-102, 157-174); other properties of the object stay behind.
loading.api.registerVirtualModel({ provider: "p", id: "one", name: "One", thinkingLevels: ["low"], contextWindow: 10, maxTokens: 5, input: ["text"], route, api: "openai-completions", description: "extra" });
loading.api.registerVirtualModel({ provider: "p", id: "gone", name: "Gone", route });
loading.api.unregisterVirtualModel("p", "gone");
loading.api.registerTool({
  name: "t", label: "T", description: "d", parameters: { type: "object", properties: {} },
  outputSchema: { type: "object" }, exposure: "deferred", namespace: { name: "ns" }, annotations: { readOnlyHint: true }, defaultActive: false,
  prepareLoadout: () => undefined, execute: async () => ({ content: [] }),
});
loading.api.registerTool({ name: "plain", label: "P", description: "d", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [] }) });
await loading.connect();
const register = (await frame).register;
assert.deepEqual(factoryCalls.map(([method, args]) => [method, args.name]), [["mcpServers.check", "a"], ["mcpServers.check", "b"], ["mcpServers.check", "a"], ["mcpServers.check", "c"]]);
// The unregistration travels too: Pi filters the runtime-wide queue of models registered before the runner binds (loader.ts:228-232).
assert.deepEqual(register.unregister_virtual_models, [{ provider: "p", id: "gone" }]);
assert.deepEqual(register.mcp_servers, [{ name: "a", config: { url: "https://a2.example" } }, { name: "b", config: { url: "https://b.example" } }]);
assert.deepEqual(register.virtual_models, [{ provider: "p", id: "one", name: "One", thinkingLevels: ["low"], contextWindow: 10, maxTokens: 5, input: ["text"] }]);
const [tool, plain] = register.tools;
assert.deepEqual([tool.output_schema, tool.exposure, tool.namespace, tool.annotations, tool.default_active, tool.prepares_loadout], [{ type: "object" }, "deferred", { name: "ns" }, { readOnlyHint: true }, false, true]);
assert.deepEqual([plain.output_schema, plain.exposure, plain.namespace, plain.default_active, plain.prepares_loadout], [undefined, undefined, undefined, undefined, undefined]);
loading.conn.socket.destroy();

// After the register frame the calls go to the host. The registry and the callable tools are read from it when they are read.
const runtime = new Runtime("/ext/api.mjs");
const sync = [];
const asyncCalls = [];
let servers = [];
let callable = [];
let failNext;
runtime.conn = {
  closed: false,
  callSync(method, args) {
    sync.push([method, args]);
    if (failNext) { const error = failNext; failNext = undefined; throw error; }
    if (method === "getMcpServers") return structuredClone({ servers });
    if (method === "getCallableTools") return structuredClone({ tools: callable });
    return null;
  },
  call: async (method, args, parent) => { asyncCalls.push([method, args, parent]); return runtime.nextResult; },
  requestState() {},
  notify() {},
};
runtime.hostReady = true;
runtime.commitLoad();
const api = runtime.api;

servers = [{ name: "x", config: { url: "https://x.example" }, extensionPath: "/ext/api.mjs" }];
api.registerMcpServer("x", { url: "https://x.example" });
assert.deepEqual(sync.at(-1), ["registerMcpServer", { name: "x", config: { url: "https://x.example" } }]);
assert.deepEqual(api.getMcpServers(), servers);
// getMcpServers returns copies (mcp-servers.ts:227-229).
api.getMcpServers()[0].config.url = "mutated";
assert.equal(api.getMcpServers()[0].config.url, "https://x.example");
servers = [];
api.unregisterMcpServer("x");
assert.deepEqual(sync.at(-1), ["unregisterMcpServer", { name: "x" }]);
assert.deepEqual(api.getMcpServers(), []);
// A refused registration throws at the call and leaves the list alone.
failNext = new Error('MCP server "x" is already registered by extension "/other"');
assert.throws(() => api.registerMcpServer("x", { url: "https://x.example" }), /already registered by extension "\/other"/);

// A failed registerVirtualModel restores the route it replaced; a successful one replaces it.
const first = { provider: "p", id: "m", name: "M", route: () => "first" };
api.registerVirtualModel(first);
failNext = new Error("bad model");
assert.throws(() => api.registerVirtualModel({ provider: "p", id: "m", name: "M", route: () => "second" }), /bad model/);
assert.equal(runtime.virtualModels.get(JSON.stringify(["p", "m"])), first);
failNext = new Error("bad new model");
assert.throws(() => api.registerVirtualModel({ provider: "p", id: "n", name: "N", route: () => "n" }), /bad new model/);
assert.equal(runtime.virtualModels.has(JSON.stringify(["p", "n"])), false);
api.unregisterVirtualModel("p", "m");
assert.deepEqual(sync.at(-1), ["unregisterVirtualModel", { provider: "p", id: "m" }]);
assert.equal(runtime.virtualModels.size, 0);

// getSettings answers from the replicated state, as a copy, and reports when the host sent none.
assert.throws(() => api.getSettings(), /settings are not available/);
runtime.applyState({ settings: { a: { b: 1 } } });
const settings = api.getSettings();
settings.a.b = 2;
assert.deepEqual(api.getSettings(), { a: { b: 1 } });

// A tool result's structuredContent and isError travel; absent ones stay absent.
assert.deepEqual(runtime.normalizeToolResult({ content: "x", structuredContent: { rows: [1] }, isError: true }), { content: "x", details: undefined, is_error: true, structured_content: { rows: [1] }, terminate: undefined, usage: undefined });
assert.equal(runtime.normalizeToolResult({ content: "x" }).structured_content, undefined);

// A tool call's context lists the callable tools and runs a nested call; other contexts have neither.
callable = [{ name: "echo" }];
assert.equal(Object.hasOwn(runtime.ctx, "tools"), false);
assert.equal(runtime.ctx.executeTool, undefined);
const toolCtx = runtime.addToolContext(Object.create(runtime.ctx), "call-1");
assert.deepEqual(toolCtx.tools, [{ name: "echo" }]);
assert.equal(Object.keys(toolCtx).includes("tools"), false);
runtime.nextResult = { toolCall: { id: "call-1/1" }, result: { content: [] }, isError: false };
assert.equal((await toolCtx.executeTool("echo", { v: 1 })).toolCall.id, "call-1/1");
assert.deepEqual(asyncCalls.at(-1).slice(0, 2), ["executeTool", { callerId: "call-1", name: "echo", args: { v: 1 }, executeId: "e1" }]);

// Inside a request the call is tied to it, and an explicit signal makes it independent of the calling request; its abort sends the cancel after the call.
const request = { id: "req-1", connection: runtime.conn, settled: false, responded: false, cancelled: false, controller: new AbortController() };
await runtime.requestContext.run(request, async () => {
  await toolCtx.executeTool("echo", {});
  assert.equal(asyncCalls.at(-1)[2], "req-1");
  const controller = new AbortController();
  const pending = toolCtx.executeTool("echo", {}, { signal: controller.signal });
  assert.equal(asyncCalls.at(-1)[2], "");
  assert.equal(asyncCalls.at(-1)[1].ownSignal, true);
  controller.abort();
  await pending;
  assert.deepEqual(asyncCalls.at(-1).slice(0, 3), ["executeTool.cancel", { executeId: "e3" }, ""]);
  // A signal that is already aborted cancels once the call is sent.
  await toolCtx.executeTool("echo", {}, { signal: AbortSignal.abort() });
  assert.deepEqual(asyncCalls.at(-1).slice(0, 2), ["executeTool.cancel", { executeId: "e4" }]);
  // runner.ts:979-981 ("options.signal ?? signal"): a null signal leaves the call with the calling tool's, tied to the request.
  await toolCtx.executeTool("echo", {}, { signal: null });
  assert.deepEqual(asyncCalls.at(-1), ["executeTool", { callerId: "call-1", name: "echo", args: {}, executeId: "e5" }, "req-1"]);
});

// Partial results reach onUpdate in order before the outcome, each as a request the Host waits on; a throwing callback answers its request with the error, still sees every later partial result, and the call rejects with the error it threw (nested-tool-calls.ts:219-231, agent-loop.ts:833-845).
const responses = [];
runtime.respond = async (id, result, error) => { responses.push([id, result, error]); };
const seen = [];
const partials = (executeId) => ["one", "two"].map((text, i) => ["u" + i, { method: "execute_tool_update", args: { executeId, result: { content: [{ type: "text", text }] } } }]);
runtime.call = async (method, args) => {
  if (method === "executeTool") {
    let rejected;
    for (const [id, request] of partials(args.executeId)) {
      await runtime.handleRequest(id, request, Object.create(runtime.ctx));
      rejected ??= responses.at(-1)[2];
    }
    // The Host rejects the call with the first error its update request answered.
    if (rejected) throw new Error("host: " + rejected.message);
  }
  return { toolCall: { id: "call-1/9" }, result: {}, isError: false };
};
const outcome = await toolCtx.executeTool("echo", {}, { onUpdate: (partial) => seen.push(partial.content[0].text) });
assert.deepEqual(seen, ["one", "two"]);
assert.deepEqual(responses.map((response) => [response[0], response[1], response[2]]), [["u0", null, undefined], ["u1", null, undefined]]);
assert.equal(outcome.toolCall.id, "call-1/9");
let delivered = 0;
await assert.rejects(toolCtx.executeTool("echo", {}, { onUpdate: () => { delivered++; throw new Error("callback failed " + delivered); } }), /callback failed 1$/);
assert.equal(delivered, 2);
assert.equal(responses.at(-2)[2].message, "callback failed 1");
assert.equal(responses.at(-1)[2].message, "callback failed 2");
assert.equal(runtime.nestedCalls.size, 0);

// tool_prepare_loadout answers from the loadout the host sent; a tool without a definition is direct.
let received;
runtime.tools.set("orchestrator", { name: "orchestrator", prepareLoadout: (loadout) => {
  received = [loadout.declared.length, loadout.callable.length, loadout.registered.length, loadout.getExposure("grep"), loadout.getExposure("constructor"), loadout.getNamespace("grep"), loadout.getNamespace("constructor")];
  return { descriptions: { orchestrator: "changed" } };
} });
await runtime.handleRequest("r1", { method: "tool_prepare_loadout", tool: "orchestrator", args: { declared: [{}, {}], callable: [{}], registered: [{}, {}, {}], exposures: { grep: "deferred" }, namespaces: { grep: { name: "ns" } } } }, Object.create(runtime.ctx));
assert.deepEqual(received, [2, 1, 3, "deferred", "direct", { name: "ns" }, undefined]);
assert.deepEqual(responses.at(-1).slice(0, 2), ["r1", { descriptions: { orchestrator: "changed" } }]);
runtime.tools.set("silent", { name: "silent", prepareLoadout: () => undefined });
await runtime.handleRequest("r2", { method: "tool_prepare_loadout", tool: "silent", args: {} }, Object.create(runtime.ctx));
assert.deepEqual(responses.at(-1).slice(0, 2), ["r2", null]);

// virtual_model_route gives the router the request with model objects, the request's signal and the context, and answers with the route.
let routed;
api.registerVirtualModel({ provider: "p", id: "r", name: "R", async route(request, ctx) {
  routed = { thisIsModel: this.id, provider: request.model.provider, id: request.model.id, previous: request.previous.model.provider, failed: request.failed.model.id, signal: request.signal, ctx };
  return { model: { provider: "q", id: "phys" }, thinkingLevel: request.thinkingLevel, state: request.state === undefined ? { n: 1 } : { n: request.state.n + 1 } };
} });
const controllerRoute = new AbortController();
const requestCtx = Object.create(runtime.ctx);
// Pi's ctx.signal is the run's signal (runner.ts:917-920), so the request's own signal travels under the runtime's key.
Object.defineProperty(requestCtx, requestSignal, { value: controllerRoute.signal });
const routeArgs = { provider: "p", id: "r", request: { model: { id: "r", provider: "p" }, thinkingLevel: "low", reason: "retry", previous: { model: { id: "a", provider: "pa" } }, failed: { model: { id: "b", provider: "pb" }, message: {} }, state: { n: 1 }, messages: [] } };
await runtime.handleRequest("r3", { method: "virtual_model_route", args: routeArgs }, requestCtx);
assert.deepEqual(responses.at(-1).slice(0, 2), ["r3", { model: { provider: "q", id: "phys" }, thinkingLevel: "low", state: { n: 2 } }]);
// A router that returns the state object it was given says so: Pi keeps the state then (agent-session.ts:788).
api.registerVirtualModel({ provider: "p", id: "same", name: "S", route: async (request) => ({ model: { provider: "q", id: "phys" }, thinkingLevel: "low", state: request.state }) });
await runtime.handleRequest("r3b", { method: "virtual_model_route", args: { provider: "p", id: "same", request: { ...routeArgs.request, state: { n: 1 } } } }, requestCtx);
assert.equal(responses.at(-1)[1].stateUnchanged, true);
api.registerVirtualModel({ provider: "p", id: "fresh", name: "F", route: async (request) => ({ model: { provider: "q", id: "phys" }, thinkingLevel: "low", state: { ...request.state } }) });
await runtime.handleRequest("r3c", { method: "virtual_model_route", args: { provider: "p", id: "fresh", request: { ...routeArgs.request, state: { n: 1 } } } }, requestCtx);
assert.equal(responses.at(-1)[1].stateUnchanged, undefined);
assert.equal(routed.thisIsModel, "r");
assert.deepEqual([routed.provider, routed.id, routed.previous, routed.failed], ["p", "r", "pa", "b"]);
assert.equal(routed.signal, controllerRoute.signal);
assert.equal(routed.ctx, requestCtx);
await runtime.handleRequest("r4", { method: "virtual_model_route", args: { provider: "p", id: "nope", request: routeArgs.request } }, requestCtx);
assert.match(responses.at(-1)[2].message, /unknown virtual model p\/nope/);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// examples/sdk/14-codemode-mcp.ts is the unchanged Pi 0.99.1 SDK example: it builds a session with the codemode, tool-search and MCP built-in extension factories, activates the first two through the defaultTools setting and prints the active tools before it prompts. The SDK and its extension runtime are Pi's own vendored code running under PiG's module loader, so this proves the example loads and binds every 0.99.1 API it uses; it stops at the prompt, which needs an API key.
//
// upstream: .upstream/v0.99.1/packages/coding-agent/examples/sdk/14-codemode-mcp.ts:14-55
func TestNodeRuntimeRunsTheCodemodeMCPSDKExample(t *testing.T) {
	root := findModuleRoot(t)
	runtimeDir := filepath.Join(root, "coding", "extension", "host", "subprocess", "runtime-node")
	work := t.TempDir()
	example, err := os.ReadFile(filepath.Join(root, ".upstream", "current", "packages", "coding-agent", "examples", "sdk", "14-codemode-mcp.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "14-codemode-mcp.ts"), example, 0o600); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(work, "agent")
	command := exec.CommandContext(t.Context(), "node", "--import", (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(runtimeDir, "register-loader.mjs"))}).String(), "14-codemode-mcp.ts")
	command.Dir = work
	command.Env = append(os.Environ(), "HOME="+work, "PIG_HOME="+filepath.Join(work, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_OFFLINE=1")
	output, err := command.CombinedOutput()
	text := string(output)
	if !strings.HasPrefix(text, "Active tools: read, bash, edit, write, codemode, tool_search\n") {
		t.Fatalf("output = %q, want the active tools with codemode and tool_search first", text)
	}
	// The example ends at the prompt, which fails for want of credentials.
	if err == nil || !strings.Contains(text, "No API key found for the selected model.") {
		t.Fatalf("err = %v; output %q, want it to stop at the prompt without an API key", err, text)
	}
}

// Pi starts compaction in an unawaited async block and lets later events through (agent-session.ts:3369-3378). A handler that waits for ctx.compact's callbacks is therefore blocked on the host, not running: the runtime reports the request blocked while the call is pending (the Session releases the handler's acknowledgment on that report, conn.go request_state "blocked") and progress after. The call itself stays unparented, so the handler ending does not cancel compaction.
func TestNodeRuntimeCompactReportsItsHandlerBlockedAndStaysUnparented(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
const runtime = new Runtime("/ext/compact.mjs");
const frames = [];
let finish;
runtime.conn = {
  closed: false,
  callSync() { return null; },
  call: (method, args, parent) => new Promise((resolve) => { frames.push(["call", method, args, parent]); finish = resolve; }),
  requestState(id, state, reason) { frames.push(["state", id, state, reason]); },
  notify() {},
};
runtime.hostReady = true;
runtime.commitLoad();
const request = { id: "req-1", connection: runtime.conn, settled: false, responded: false, cancelled: false, controller: new AbortController() };
const outcome = await runtime.requestContext.run(request, async () => {
  const pending = new Promise((resolve) => runtime.ctx.compact({ customInstructions: "x", onComplete: resolve, onError: resolve }));
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(frames, [["state", "req-1", "blocked", "host_call"], ["call", "compact", { customInstructions: "x", awaitCompletion: true }, ""]]);
  finish({ summary: "s" });
  return await pending;
});
assert.deepEqual(outcome, { summary: "s" });
assert.deepEqual(frames.at(-1), ["state", "req-1", "progress", undefined]);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
