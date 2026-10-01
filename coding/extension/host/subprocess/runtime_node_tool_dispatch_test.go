package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// The Node runtime's side of how a model-issued call reaches a tool, without a Host. agent-loop.ts:707-716 (prepareToolCall) calls
// tool.prepareArguments before validateToolArguments; the host validates, so the runtime declares the hook (prepares_arguments), answers a
// tool_prepare_arguments request with it, and does not prepare the arguments again in tool_call. agent-loop.ts:619-647: the host writes a
// parallel batch in source order, so serve() must start the handlers in the order the frames arrive.
func TestNodeRuntimeToolDispatch(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
const runtime = new Runtime("/ext/dispatch.mjs");
const prepared = [];
const executed = [];
runtime.conn = { closed: false, callSync() { return null; }, call: async () => null, requestState() {}, notify() {}, send() {} };
runtime.hostReady = true;
runtime.commitLoad();
runtime.api.registerTool({
  name: "legacy", label: "L", description: "d", parameters: { type: "object", required: ["text"], properties: { text: { type: "string" } } },
  prepareArguments(args) { prepared.push(args); if (args.fail) throw new Error("prepare exploded"); return { text: args.legacy }; },
  async execute(_id, params) { executed.push(params); return { content: [{ type: "text", text: "ok" }] }; },
});
runtime.api.registerTool({ name: "plain", label: "P", description: "d", parameters: { type: "object" }, async execute() { return { content: [] }; } });
const declared = runtime.toolDeclaration(runtime.tools.get("legacy"));
assert.equal(declared.prepares_arguments, true);
assert.equal(runtime.toolDeclaration(runtime.tools.get("plain")).prepares_arguments, undefined);

const responses = [];
runtime.respond = async (id, result, error) => { responses.push([id, result, error]); };
const ctx = () => Object.create(runtime.ctx);
await runtime.handleRequest("p1", { method: "tool_prepare_arguments", tool: "legacy", args: { legacy: "hello" } }, ctx());
assert.deepEqual(responses.at(-1), ["p1", { text: "hello" }, undefined]);
await runtime.handleRequest("p2", { method: "tool_prepare_arguments", tool: "legacy", args: { fail: true } }, ctx());
assert.equal(responses.at(-1)[2].message, "prepare exploded");
await runtime.handleRequest("p3", { method: "tool_prepare_arguments", tool: "missing", args: {} }, ctx());
assert.ok(responses.at(-1)[2]);
await runtime.handleRequest("p4", { method: "tool_prepare_arguments", tool: "plain", args: {} }, ctx());
assert.ok(responses.at(-1)[2]);
assert.deepEqual(prepared, [{ legacy: "hello" }, { fail: true }]);

// The host sent the prepared arguments: tool_call must run the tool with them and not run the hook again.
prepared.length = 0;
await runtime.handleRequest("c1", { method: "tool_call", tool: "legacy", tool_call_id: "c", args: { text: "prepared", legacy: "raw" } }, ctx());
assert.deepEqual(executed, [{ text: "prepared", legacy: "raw" }]);
assert.deepEqual(prepared, []);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
