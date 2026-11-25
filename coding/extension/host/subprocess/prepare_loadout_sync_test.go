package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// A registerTool made after loading waits synchronously for the host, and the host refreshes the registry before it replies:
// that refresh asks the prepareLoadout hook of every extension (tool_prepare_loadout), the registering one included, and waits
// for each answer. Left to the serve loops, which cannot run while the registerTool holds the thread, no hook ran and the
// extension and the host waited on each other (a tool with prepareLoadout, and another tool registered in session_start, as
// pi-extensible-workflows does). setActiveTools refreshes the same way, and a synchronous call made after it runs behind it in the
// host. While a registerTool of any runtime of the process waits, or the process waits on a synchronous call while a setActiveTools
// is unfinished, the request now starts nested in that wait: the notifications sent before it apply first, those already handed to
// the serve loop's waiter included (once), inside the hook's own request, so a host call made there or in the hook is filed under
// that request; it does not wait for onLoaded handlers, and it answers without waiting for its fire-and-forget calls. A request
// that waits unstarted ahead of it (queued, or handed to the serve loop's waiter) or frames held for a turn do not hold it back, as
// Pi's hook runs inside the call whatever else waits: they stay for the serve loop, in order. Outside those waits the serve loop's
// order holds. The host's call is a synchronous stub inside the real wait, so no microtask runs before the answer must be out.
func TestNodeRuntimeStartsPrepareLoadoutNestedInARefreshItWaitsOn(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { Runtime } from %[1]q;
import { ProviderSocket } from %[2]q;

// The host's reply comes inside the real synchronous wait (the main thread's, isWaiting), where no microtask runs.
const inWait = (fn) => {
  let out;
  ProviderSocket.prototype.waitUntil.call({ closed: false }, () => { out = fn(); return true; });
  return out;
};

function attach(name) {
  const runtime = new Runtime("/ext/" + name + ".mjs");
  runtime.conn = { closed: false, callSync() { return null; }, call: async () => null, requestState() {}, notify() {}, send() {} };
  runtime.hostReady = true;
  runtime.commitLoad();
  const frames = [];
  const socket = new EventEmitter();
  socket.write = (data) => {
    for (let at = 0; at < data.length; ) {
      const size = data.readUInt32BE(at);
      frames.push(JSON.parse(data.subarray(at + 4, at + 4 + size).toString()));
      at += 4 + size;
    }
    return true;
  };
  const conn = runtime.attachConnection(socket);
  // The serve loop's connection, as in production: answers leave through it.
  runtime.conn = conn;
  const response = (id) => frames.find((env) => env.type === "response" && env.id === id)?.response;
  const ask = (id) => socket.emit("envelope", { type: "request", id, request: { method: "tool_prepare_loadout", tool: "hooked", args: { exposures: { grep: "deferred" } } } });
  return { runtime, conn, socket, response, ask };
}

const a = attach("a");
const b = attach("b");
const seen = [];
const parents = [];
// The host's registerTool, and a synchronous call made after a setActiveTools it has not finished: calls during which it asks
// both runtimes' hooks and must have their answers.
const answeredDuringTheCall = [];
let onRegister = () => {};
let onActive = () => {};
for (const side of [a, b]) {
  side.conn.callSync = (method, args, parent) => inWait(() => {
    parents.push([method, parent]);
    if (method === "registerTool") onRegister();
    if (method === "getActiveTools" && !parent) onActive();
    if (method === "getActiveTools") return { tools: ["hooked"] };
    return null;
  });
}
for (const [name, side] of [["a", a], ["b", b]]) {
  side.runtime.api.registerTool({
    name: "hooked", label: "H", description: "d", parameters: { type: "object" },
    prepareLoadout(loadout) {
      const tools = side.runtime.api.getActiveTools();
      side.runtime.fireAndForget("ui.setStatus", { key: name, text: "x" });
      seen.push({ name, model: side.runtime.state.model?.id, tools, exposure: loadout.getExposure("grep") });
      return { descriptions: { hooked: name } };
    },
    async execute() { return { content: [] }; },
  });
  // A fire-and-forget call's result never arrives while the registerTool waits.
  side.conn.call = () => new Promise(() => {});
}

const registerLate = () => a.runtime.api.registerTool({ name: "late", label: "L", description: "d", parameters: { type: "object" }, async execute() { return { content: [] }; } });

// A state push waits in b's queue ahead of its request: it applies first.
b.socket.emit("envelope", { type: "notify", notify: { method: "state_update", args: { model: { id: "after", provider: "p" } } } });
// a's onLoaded handlers are still running: the nested hook does not wait for them.
a.runtime.api.onLoaded(() => new Promise(() => {}));
a.runtime.loadedPromise = a.runtime.runLoadedHandlers();
onRegister = () => {
  a.ask("a1");
  b.ask("b1");
  answeredDuringTheCall.push(a.response("a1"), b.response("b1"));
};
registerLate();
assert.deepEqual(answeredDuringTheCall, [{ result: { descriptions: { hooked: "a" } } }, { result: { descriptions: { hooked: "b" } } }]);
assert.deepEqual(seen.map((s) => [s.name, s.tools, s.exposure]), [["a", ["hooked"], "deferred"], ["b", ["hooked"], "deferred"]]);
assert.equal(seen[1].model, "after", "the state push queued ahead applied first");
assert.deepEqual(parents.filter(([method]) => method === "getActiveTools"), [["getActiveTools", "a1"], ["getActiveTools", "b1"]], "each hook's host call is filed under its own request");
assert.deepEqual(b.conn.queue, []);

// A request waits unstarted ahead of the hook, a notification behind it, and frames are held for a turn: the hook still runs inside
// the call, as Pi's does, and what waits stays for the serve loop, in order.
const ran = seen.length;
const earlier = { type: "request", id: "earlier", request: { method: "event", event: "x" } };
const behind = { type: "notify", notify: { method: "state_update", args: { model: { id: "behind", provider: "p" } } } };
b.conn.queue.push(earlier, behind);
const held = { type: "notify", notify: { method: "state_update", args: { model: { id: "held", provider: "p" } } } };
a.conn.held.push(held);
onRegister = () => {
  b.ask("b2");
  a.ask("a2");
  answeredDuringTheCall.push(b.response("b2"), a.response("a2"));
};
registerLate();
assert.deepEqual(answeredDuringTheCall.slice(-2), [{ result: { descriptions: { hooked: "b" } } }, { result: { descriptions: { hooked: "a" } } }]);
assert.equal(seen.length, ran + 2, "both hooks ran");
assert.equal(seen.at(-2).model, "after", "a notification behind a waiting request does not apply before it");
assert.deepEqual(b.conn.queue, [earlier, behind]);
assert.deepEqual(a.conn.held, [held]);
b.conn.queue.length = 0;
a.conn.held.length = 0;

// A setActiveTools the host has not finished, then a synchronous call, which the host runs behind it: the hooks start nested in
// that call, as in Pi they run inside setActiveTools.
let finishSetActiveTools;
a.conn.call = (method) => new Promise((resolve) => { if (method === "setActiveTools") finishSetActiveTools = resolve; });
onActive = () => {
  a.ask("a4");
  b.ask("b4");
  answeredDuringTheCall.push(a.response("a4"), b.response("b4"));
};
a.runtime.api.setActiveTools(["hooked"]);
a.runtime.api.getActiveTools();
assert.deepEqual(answeredDuringTheCall.slice(-2), [{ result: { descriptions: { hooked: "a" } } }, { result: { descriptions: { hooked: "b" } } }]);
// Outside a synchronous wait the serve loop runs, and keeps its order.
b.ask("b6");
assert.equal(b.response("b6"), undefined);
assert.equal(b.conn.queue.at(-1)?.id, "b6");
b.conn.queue.length = 0;
// Once the host has finished it, a hook asked during a synchronous call keeps the serve loop's order again.
finishSetActiveTools(null);
await new Promise((resolve) => setImmediate(resolve));
onActive = () => {
  b.ask("b5");
  answeredDuringTheCall.push(b.response("b5"));
};
a.runtime.api.getActiveTools();
assert.equal(answeredDuringTheCall.at(-1), undefined);
assert.equal(b.conn.queue.at(-1)?.id, "b5");
b.conn.queue.length = 0;
onActive = () => {};

// Outside a registerTool or in-flight setActiveTools, in another synchronous call or idle, the request keeps the serve loop's order.
inWait(() => {
  b.ask("b3");
  assert.equal(b.response("b3"), undefined);
  assert.equal(b.conn.queue.at(-1)?.id, "b3");
});

// c's serve loop runs and waits for its next frame. A state push that arrives during the registerTool is handed to that waiter,
// which cannot resume before the wait ends, and a notification after it is queued: the nested hook applies both first, the
// handed one once, and a host call a notification makes is filed under the hook's request.
const c = attach("c");
const cSeen = [];
c.conn.callSync = (method, args, parent) => inWait(() => (parents.push([method, parent]), null));
c.runtime.api.registerTool({
  name: "hooked", label: "H", description: "d", parameters: { type: "object" },
  prepareLoadout() {
    cSeen.push(c.runtime.state.model?.id);
    return { descriptions: { hooked: "c" } };
  },
  async execute() { return { content: [] }; },
});
const applied = [];
const handleNotify = c.runtime.handleNotify.bind(c.runtime);
c.runtime.handleNotify = (notify) => {
  if (notify.method === "probe") return void parents.push(["probe", c.runtime.activeRequest()?.id ?? ""]);
  applied.push(notify.args?.model?.id);
  return handleNotify(notify);
};
c.runtime.connect = async () => {};
void c.runtime.serve();
await new Promise((resolve) => setImmediate(resolve));
onRegister = () => {
  c.socket.emit("envelope", { type: "notify", notify: { method: "state_update", args: { model: { id: "handed", provider: "p" } } } });
  c.socket.emit("envelope", { type: "notify", notify: { method: "probe" } });
  c.ask("c1");
  answeredDuringTheCall.push(c.response("c1"));
};
registerLate();
assert.deepEqual(answeredDuringTheCall.at(-1), { result: { descriptions: { hooked: "c" } } });
assert.deepEqual(cSeen, ["handed"], "the frame handed to the serve loop applied before the hook");
assert.deepEqual(parents.at(-1), ["probe", "c1"], "a notification applied first runs under the hook's request");
await new Promise((resolve) => setImmediate(resolve));
assert.deepEqual(applied, ["handed"], "the serve loop does not apply it again");
// A request handed to the waiter is work the loop has not started: the hook still runs, any notification queued behind
// that request stays queued until the loop runs the request first, and the loop applies both in order afterward.
onRegister = () => {
  c.socket.emit("envelope", { type: "request", id: "c-earlier", request: { method: "event", event: "x" } });
  c.socket.emit("envelope", { type: "notify", notify: { method: "state_update", args: { model: { id: "after-earlier", provider: "p" } } } });
  c.ask("c2");
  answeredDuringTheCall.push(c.response("c2"));
};
registerLate();
assert.deepEqual(answeredDuringTheCall.at(-1), { result: { descriptions: { hooked: "c" } } });
assert.deepEqual(cSeen, ["handed", "handed"], "a notification queued behind a handed-off request must not apply before the hook");
for (let i = 0; i < 5 && (!c.response("c-earlier") || c.runtime.state.model?.id !== "after-earlier"); i++) {
  await new Promise((resolve) => setImmediate(resolve));
}
assert.ok(c.response("c-earlier"), "the loop ran the request it was handed");
assert.equal(c.runtime.state.model?.id, "after-earlier", "the loop applied the queued state update after the handed-off request");
assert.deepEqual(applied, ["handed", "after-earlier"], "each state update applied once in frame order");
`, fileURL(path), fileURL(filepath.Join(filepath.Dir(path), "provider-socket.mjs")))
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
