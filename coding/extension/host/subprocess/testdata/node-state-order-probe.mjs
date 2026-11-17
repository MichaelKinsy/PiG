// A real Runtime (runtime-node/runtime.mjs) over a fake transport, driven the way the host drives it: the host answers each synchronous call with the session's
// state push and then the call's result, and a synchronous wait (ProviderSocket.waitUntil) delivers every frame available. The frames of the synchronous
// call a handler makes after an await reach the receive loop's waiter or its queue, and the loop reads them only when the handler's run ends. The probe prints
// one JSON object with what each scenario's handler read. Run: node node-state-order-probe.mjs <path of runtime.mjs>.
import { EventEmitter } from "node:events";
import { pathToFileURL } from "node:url";

const { Runtime } = await import(pathToFileURL(process.argv[2]));
const timer = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const macrotask = () => new Promise((resolve) => setImmediate(resolve));

// The order this probe asserts comes from what each step observed, and no timer's length decides it: a step that must follow another waits on a gate the earlier
// step opens, and a scenario ends when the effect it checks happened. A process the host starves of CPU runs every timer and every turn of its event loop late;
// an order that a timer's length decides (a timer a handler sets after another one ran late, against a timer the probe set before it) is then inverted, and the
// probe reported a handler that had not run yet. The one timer left is where a scenario has a handler resume from a timer, which no other timer races.
// HANG_MS only names a gate that never opens (a handler that did not run), so a broken runtime is a named failure and not a hang.
const HANG_MS = 30000;

// gate returns a promise and the function that opens it.
function gate() {
  let open;
  const opened = new Promise((resolve) => { open = resolve; });
  return { opened, open };
}

// reached waits for a promise and fails with its name when it does not settle.
async function reached(name, promise) {
  let hang;
  const hung = new Promise((_, reject) => { hang = setTimeout(() => reject(new Error(`never reached: ${name}`)), HANG_MS); });
  try {
    await Promise.race([promise, hung]);
  } finally {
    clearTimeout(hang);
  }
}

// settled resolves once the serve loop has read every frame the host sent and waits for the next one: a frame the host delivers takes the loop's waiter, and the loop
// registers a new one only when it has read the frame and its handling returned, so a registered waiter is the loop parked with nothing left. The loop reads a frame
// in the microtasks that follow its delivery, so a turn of the event loop is all it takes, however long the turn lasts. It does not read handedOff, which a scenario
// here checks.
function settled(host, runtime) {
  const conn = runtime.conn;
  const idle = () => host.frames.length === 0 && conn.waiters.length > 0;
  return reached("the serve loop reads every frame the host sent", (async () => { while (!idle()) await macrotask(); })());
}

// FakeHost holds the session's log and answers a runtime's calls. Frames it sends wait in frames until a synchronous wait or deliver() takes them.
class FakeHost {
  constructor() {
    this.entries = [];
    this.name = "";
    this.frames = [];
    this.afterLeafRead = undefined;
    this.eventAfterWrite = undefined;
    this.beforePush = undefined;
    // order records the frames the runtime sent that a scenario compares with what its handlers did.
    this.order = [];
    this.socket = new EventEmitter();
    this.socket.write = () => {};
    this.socket.destroy = () => {};
    // A synchronous wait delivers every frame the host has sent, then the caller checks its condition (ProviderSocket.waitUntil).
    this.socket.waitUntil = (done) => {
      for (const frame of this.frames.splice(0)) this.socket.emit("envelope", frame);
      if (!done()) throw new Error("a synchronous wait found no answer");
    };
  }

  append(type, fields) {
    const entry = { id: `e${this.entries.length + 1}`, type, ...fields };
    this.entries.push(entry);
    return entry;
  }

  push(entry) {
    if (entry.type === "session_info") this.name = entry.name;
    return { type: "notify", notify: { method: "state_update", args: { session: { sessionId: "s", sessionName: this.name, leafId: entry.id, entriesAppended: [entry], entryCount: this.entries.length } } } };
  }

  result(id, result) {
    return { type: "call_result", id, call_result: { result } };
  }

  // handle answers one frame the runtime sent.
  handle(env) {
    if (env.type === "request_state" && env.request_state.state === "started") this.order.push(`started ${env.request_state.request_id}`);
    if (env.type !== "call") return;
    const { method, args } = env.call;
    if (method === "setSessionName") {
      const before = this.beforePush;
      this.beforePush = undefined;
      if (before) this.frames.push(...before());
      this.frames.push(this.push(this.append("session_info", { name: args.name })));
      if (this.eventAfterWrite) this.frames.push(this.eventAfterWrite());
      this.frames.push(this.result(env.id, {}));
      return;
    }
    if (method === "sessionRead" && args.method === "getLeafEntry") {
      this.frames.push(this.result(env.id, this.entries[this.entries.length - 1]));
      const hook = this.afterLeafRead;
      this.afterLeafRead = undefined;
      if (hook) this.frames.push(...hook());
      return;
    }
    this.frames.push(this.result(env.id, {}));
  }

  // deliver hands frames to the runtime one macrotask apart, as the message port does.
  async deliver(...frames) {
    for (const frame of frames) {
      this.socket.emit("envelope", frame);
      await macrotask();
    }
  }
}

// start returns a Runtime serving a FakeHost whose log holds e1 and e2.
async function start(register) {
  const host = new FakeHost();
  host.append("custom", { customType: "probe" });
  host.append("custom", { customType: "probe" });
  const runtime = new Runtime("/ext/state-order.mjs");
  register(runtime.api, runtime);
  const conn = runtime.attachConnection(host.socket);
  conn.send = (...envs) => { for (const env of envs) host.handle(env); return true; };
  runtime.conn = conn;
  const serving = runtime.serve();
  await host.deliver({ type: "ready", ready: { cwd: "/", state: { session: { sessionId: "s", sessionName: "", sessionFile: "", leafId: "e2", entriesAppended: [...host.entries], entryCount: 2 } } } });
  const stop = async () => {
    host.socket.emit("close");
    await serving;
  };
  return { host, runtime, stop };
}

const command = (id, tool) => ({ type: "request", id, request: { method: "command", tool, args: "" } });
const ids = (ctx) => ctx.sessionManager.getEntries().map((entry) => entry.id).join(",");
const out = {};

// Handler a resumes from a timer and writes; the pump of its last call delivers command b's request and then a push of another writer (the host's entry e4).
// b renames the session. The host's log is e1..e5 in the order the host appended them, and the name b wrote is the last. b reads once the serve loop has read
// what the host sent (settled), and the probe goes on once b read (finished).
async function limbo(withOnLoaded) {
  const finished = gate();
  const { host, runtime, stop } = await start((pi) => {
    if (withOnLoaded) pi.onLoaded(() => {});
    pi.registerCommand("a", { handler: async () => { await timer(5); pi.setSessionName("a-name"); } });
    pi.registerCommand("b", { handler: async (_args, ctx) => {
      pi.setSessionName("renamed");
      await settled(host, runtime);
      result.name = ctx.sessionManager.getSessionName();
      result.ids = ids(ctx);
      finished.open();
    } });
  });
  const result = {};
  host.afterLeafRead = () => {
    host.append("custom", { customType: "other" });
    host.name = "host-x";
    return [command("rb", "b"), host.push(host.entries[host.entries.length - 1])];
  };
  await host.deliver(command("ra", "a"));
  await reached("limbo: handler b read the state", finished.opened);
  result.host = `${host.name} ${host.entries.map((entry) => entry.id).join(",")}`;
  await stop();
  return result;
}
out.limbo = await limbo(true);
out.limboNoLoaded = await limbo(false);

// A handler that resumes from a timer and writes sees the write in the log, in the state of the host's frame order, and the session_info_changed handler that
// starts nested in the write sees it too.
{
  const result = {};
  const finished = gate();
  const { host, runtime, stop } = await start((pi) => {
    pi.registerCommand("a", { handler: async (_args, ctx) => {
      await timer(5);
      pi.setSessionName("nested-name");
      result.afterWrite = `${ctx.sessionManager.getSessionName()} ${ids(ctx)}`;
      finished.open();
    } });
    pi.on("session_info_changed", (_event, ctx) => { result.inEvent = `${ctx.sessionManager.getSessionName()} ${ids(ctx)}`; });
  });
  host.eventAfterWrite = () => ({ type: "request", id: "ev", request: { method: "event", event: "session_info_changed", handler_id: runtime.handlers.get("session_info_changed")[0].id, args: {} } });
  await host.deliver(command("ra", "a"));
  await reached("nested: handler a wrote", finished.opened);
  await settled(host, runtime);
  await stop();
  out.nested = result;
}

// A setter's session_info_changed handler starts inside the setter (Pi runs it in the call, and the host waits for it to start before it answers the setter),
// also when the serve loop holds a request of the extension: a request handed to the waiting loop by the pump of the setter's own call ("handed"), and
// a request the loop took from its queue and has not started, while a handler that began first writes ("queued"). Refusing the nested start there leaves the
// event to the loop, which cannot read it until the setter returns, and the host's wait for the event deadlocks the setter.
async function nestedWithRequest(shape) {
  const order = [];
  const finished = gate();
  const state = await start((pi) => {
    pi.onLoaded(() => {});
    pi.on("session_info_changed", () => {});
    pi.registerCommand("a", { handler: async () => {
      await timer(5);
      pi.setSessionName(shape === "handed" ? "x" : "a-name");
      if (shape === "handed") order.push("setter returned");
    } });
    pi.registerCommand("b", { handler: async () => { pi.setSessionName("b-name"); order.push("setter returned"); } });
    pi.registerCommand("c", { handler: async () => { order.push("c ran"); finished.open(); } });
  });
  const { host, runtime } = state;
  host.order = order;
  const event = () => ({ type: "request", id: "ev", request: { method: "event", event: "session_info_changed", handler_id: runtime.handlers.get("session_info_changed")[0].id, args: {} } });
  if (shape === "handed") {
    host.beforePush = () => [command("rc", "c")];
    host.eventAfterWrite = event;
  } else {
    host.afterLeafRead = () => { host.eventAfterWrite = event; return [command("rb", "b"), command("rc", "c")]; };
  }
  await host.deliver(command("ra", "a"));
  await reached(`nestedWithRequest ${shape}: command c ran`, finished.opened);
  await settled(host, runtime);
  await state.stop();
  return order.filter((item) => item === "started ev" || item === "setter returned" || item === "c ran").join(" | ");
}
out.nestedHanded = await nestedWithRequest("handed");
out.nestedQueued = await nestedWithRequest("queued");

// The frame the loop was handed is read by the loop, which clears it from handedOff: a synchronous call a handler makes later, one that pushes no state
// (ctx.getContextUsage), finds nothing to apply, and the push the loop applied is not applied a second time.
{
  const result = {};
  const pushed = gate();
  const finished = gate();
  const { host, runtime, stop } = await start((pi) => {
    pi.registerCommand("a", { handler: async (_args, ctx) => { await pushed.opened; ctx.getContextUsage(); result.ids = ids(ctx); finished.open(); } });
  });
  await host.deliver(command("ra", "a"));
  host.append("custom", { customType: "other" });
  await host.deliver(host.push(host.entries[host.entries.length - 1]));
  pushed.open();
  await reached("staleHandedOff: handler a read the log", finished.opened);
  await settled(host, runtime);
  await stop();
  out.staleHandedOff = result.ids;
}

// A replicated log is replaced by a page with a lower count that holds the whole log: the page the host sends from 0 when its cursor is out of range
// (Session.RawEntriesPage restarts at 0 and the push's count is the cursor after the page), and the pages after it extend it.
{
  const { runtime, stop } = await start(() => {});
  const page = (entries, count) => ({ session: { sessionId: "s", entriesAppended: entries, entryCount: count } });
  const log = () => runtime.state.session.entries.map((entry) => entry.id).join(",");
  runtime.applyState(page([{ id: "e3" }], 3));
  const grown = log();
  runtime.applyState(page([{ id: "n1" }, { id: "n2" }], 2));
  const afterReset = log();
  runtime.applyState(page([{ id: "n3" }], 3));
  out.replaced = `${grown} ${afterReset} ${log()}`;
  await stop();
}

// A state the host sends before the ready is applied in frame order, not dropped, and the load goes on: the serve loop reads every frame the same way before
// and after the ready. The ready here carries no state, so the name the handler reads is the early state's. The host writes no state before a connection's
// ready; the Go, Python, Rust and Lisp SDKs apply one that arrives all the same (extensions/sdk/state_before_ready_test.go).
{
  const result = {};
  const finished = gate();
  const host = new FakeHost();
  const runtime = new Runtime("/ext/state-before-ready.mjs");
  runtime.api.registerCommand("a", { handler: async (_args, ctx) => { result.name = ctx.sessionManager.getSessionName(); finished.open(); } });
  const conn = runtime.attachConnection(host.socket);
  conn.send = (...envs) => { for (const env of envs) host.handle(env); return true; };
  runtime.conn = conn;
  const serving = runtime.serve();
  const named = host.append("session_info", { name: "named before ready" });
  await host.deliver(
    { type: "notify", notify: { method: "state_update", args: { state: { session: { sessionId: "s", sessionName: "named before ready", sessionFile: "", leafId: named.id, entriesAppended: [named], entryCount: 1 } } } } },
    { type: "ready", ready: { cwd: "/" } },
    command("ra", "a"),
  );
  await reached("beforeReady: handler a read the name", finished.opened);
  await settled(host, runtime);
  host.socket.emit("close");
  await serving;
  out.beforeReady = result.name;
}

console.log(JSON.stringify(out));
process.exit(0);
