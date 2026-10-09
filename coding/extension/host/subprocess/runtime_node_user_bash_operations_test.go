package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// The Node runtime's side of a user_bash handler's `{ operations }` (types.ts UserBashEventResult; runner.ts isUserBashEventResult), without a
// Host: the reply names the object by handle, user_bash_exec runs its exec with onData, signal, timeout and env, each onData chunk leaves as a
// tool_update before the answer, an operations value without a function exec is left for the host to reject, and the host's release drops the
// object.
func TestNodeRuntimeUserBashOperations(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime, requestSignal } from %q;
const runtime = new Runtime("/ext/user-bash.mjs");
const sent = [];
runtime.conn = { closed: false, callSync() { return null; }, call: async () => null, requestState() {}, notify(method, args) { sent.push([method, args]); }, send() {} };
runtime.hostReady = true;
runtime.commitLoad();
let seen;
let reply = { operations: { exec: async (command, cwd, options) => {
  seen = { command, cwd, timeout: options.timeout, env: options.env, signal: options.signal };
  options.onData(Buffer.from("one"));
  options.onData(Buffer.from([0xff, 0x00]));
  if (command === "reject") throw new Error("rejected: " + cwd);
  return { exitCode: command === "none" ? null : 5 };
} } };
runtime.api.on("user_bash", async () => reply);
const handler = runtime.handlers.get("user_bash")[0];
const responses = [];
runtime.respond = async (id, result, error) => { responses.push([id, result, error]); };
const ctx = () => Object.assign(Object.create(runtime.ctx), { [requestSignal]: new AbortController().signal });
const run = async (id, request) => { await runtime.handleRequest(id, request, ctx()); return responses.at(-1); };

assert.deepEqual(await run("u1", { method: "event", event: "user_bash", handler_id: handler.id, args: { type: "user_bash", command: "x", cwd: "/w" } }), ["u1", { operations: { handle: "bash-1" } }, undefined]);
sent.length = 0;
assert.deepEqual(await run("e1", { method: "user_bash_exec", tool: "bash-1", args: { command: "run", cwd: "/w", timeout: 1.5, env: { K: "V" } } }), ["e1", { exitCode: 5 }, undefined]);
assert.deepEqual(sent, [["tool_update", { request_id: "e1", result: { data: "b25l" } }], ["tool_update", { request_id: "e1", result: { data: "/wA=" } }]]);
assert.deepEqual({ ...seen, signal: undefined }, { command: "run", cwd: "/w", timeout: 1.5, env: { K: "V" }, signal: undefined });
assert.ok(seen.signal instanceof AbortSignal);
assert.deepEqual((await run("e2", { method: "user_bash_exec", tool: "bash-1", args: { command: "none", cwd: "/w" } }))[1], { exitCode: null });
assert.equal(seen.timeout, undefined);
assert.equal(seen.env, undefined);
assert.equal((await run("e3", { method: "user_bash_exec", tool: "bash-1", args: { command: "reject", cwd: "/w" } }))[2].message, "rejected: /w");

// Without a function exec, or alongside a result, the reply names no handle.
for (const invalid of [{ operations: {} }, { operations: null }, { operations: { exec: 1 } }, { operations: { exec() {} }, result: { output: "x", exitCode: 0, cancelled: false, truncated: false } }]) {
  reply = invalid;
  const answered = await run("i", { method: "event", event: "user_bash", handler_id: handler.id, args: { type: "user_bash", command: "x", cwd: "/w" } });
  assert.ok(answered[2] || answered[1]?.operations?.handle === undefined, JSON.stringify(answered));
}
assert.equal(runtime.bashOperations.size, 1);

runtime.handleNotify({ method: "bash_operations_release", args: { handle: "bash-1" } });
assert.equal(runtime.bashOperations.size, 0);
const stale = await run("e4", { method: "user_bash_exec", tool: "bash-1", args: { command: "run", cwd: "/w" } });
assert.equal(stale[2].message, "unknown bash operations: bash-1");
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
