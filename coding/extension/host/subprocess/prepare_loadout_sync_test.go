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
// is unfinished, the request now starts nested in that wait: the notifications sent before it apply first, the hook runs as its own
// request (a host call it makes is filed under that request), it does not wait for onLoaded handlers, and it answers without waiting
// for its fire-and-forget calls. Where the order cannot be kept (a request queued ahead, frames held for a turn) it is refused at once.
// Otherwise the serve loop's order holds. The host's call is a synchronous stub inside the real wait, so no microtask runs before the
// answer must be out.
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

// A request queued ahead, or frames held for a turn: refused at once, the hook does not run, what waits stays.
const ran = seen.length;
const earlier = { type: "request", id: "earlier", request: { method: "event", event: "x" } };
b.conn.queue.push(earlier);
const held = { type: "notify", notify: { method: "state_update", args: { model: { id: "held", provider: "p" } } } };
a.conn.held.push(held);
onRegister = () => {
  b.ask("b2");
  a.ask("a2");
  answeredDuringTheCall.push(b.response("b2"), a.response("a2"));
};
registerLate();
for (const answer of answeredDuringTheCall.slice(-2)) assert.match(answer?.error?.message ?? "", /refusing it rather than answering it out of order/);
assert.equal(seen.length, ran, "a refused hook does not run");
assert.deepEqual(b.conn.queue, [earlier]);
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

// Outside a registerTool, in another synchronous call or idle, the request keeps the serve loop's order.
b.conn.syncWaits = 1;
b.ask("b3");
assert.equal(b.response("b3"), undefined);
assert.equal(b.conn.queue.at(-1)?.id, "b3");
`, fileURL(path), fileURL(filepath.Join(filepath.Dir(path), "provider-socket.mjs")))
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
