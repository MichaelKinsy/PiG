// A real Runtime (runtime-node/runtime.mjs) over a fake transport, driven the way the host drives it: the host answers each synchronous call with the session's
// state push and then the call's result, and a synchronous wait (ProviderSocket.waitUntil) delivers every frame available. The frames of the synchronous
// call a handler makes after an await reach the receive loop's waiter or its queue, and the loop reads them only when the handler's run ends. The probe prints
// one JSON object with what each scenario's handler read. Run: node node-state-order-probe.mjs <path of runtime.mjs>.
import { EventEmitter } from "node:events";
import { pathToFileURL } from "node:url";

const { Runtime } = await import(pathToFileURL(process.argv[2]));
const timer = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const macrotask = () => new Promise((resolve) => setImmediate(resolve));

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
// b renames the session. The host's log is e1..e5 in the order the host appended them, and the name b wrote is the last.
async function limbo(withOnLoaded) {
  const { host, stop } = await start((pi) => {
    if (withOnLoaded) pi.onLoaded(() => {});
    pi.registerCommand("a", { handler: async () => { await timer(5); pi.setSessionName("a-name"); } });
    pi.registerCommand("b", { handler: async (_args, ctx) => {
      pi.setSessionName("renamed");
      await timer(20);
      result.name = ctx.sessionManager.getSessionName();
      result.ids = ids(ctx);
    } });
  });
  const result = {};
  host.afterLeafRead = () => {
    host.append("custom", { customType: "other" });
    host.name = "host-x";
    return [command("rb", "b"), host.push(host.entries[host.entries.length - 1])];
  };
  await host.deliver(command("ra", "a"));
  await timer(60);
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
  const { host, runtime, stop } = await start((pi) => {
    pi.registerCommand("a", { handler: async (_args, ctx) => {
      await timer(5);
      pi.setSessionName("nested-name");
      result.afterWrite = `${ctx.sessionManager.getSessionName()} ${ids(ctx)}`;
    } });
    pi.on("session_info_changed", (_event, ctx) => { result.inEvent = `${ctx.sessionManager.getSessionName()} ${ids(ctx)}`; });
  });
  host.eventAfterWrite = () => ({ type: "request", id: "ev", request: { method: "event", event: "session_info_changed", handler_id: runtime.handlers.get("session_info_changed")[0].id, args: {} } });
  await host.deliver(command("ra", "a"));
  await timer(40);
  await stop();
  out.nested = result;
}

// A setter's session_info_changed handler starts inside the setter (Pi runs it in the call, and the host waits for it to start before it answers the setter),
// also when the serve loop holds a request of the extension: a request handed to the waiting loop by the pump of the setter's own call ("handed"), and
// a request the loop took from its queue and has not started, while a handler that began first writes ("queued"). Refusing the nested start there leaves the
// event to the loop, which cannot read it until the setter returns, and the host's wait for the event deadlocks the setter.
async function nestedWithRequest(shape) {
  const order = [];
  const state = await start((pi) => {
    pi.onLoaded(() => {});
    pi.on("session_info_changed", () => {});
    pi.registerCommand("a", { handler: async () => {
      await timer(5);
      pi.setSessionName(shape === "handed" ? "x" : "a-name");
      if (shape === "handed") order.push("setter returned");
    } });
    pi.registerCommand("b", { handler: async () => { pi.setSessionName("b-name"); order.push("setter returned"); } });
    pi.registerCommand("c", { handler: async () => { order.push("c ran"); } });
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
  await timer(60);
  await state.stop();
  return order.filter((item) => item === "started ev" || item === "setter returned").join(" | ");
}
out.nestedHanded = await nestedWithRequest("handed");
out.nestedQueued = await nestedWithRequest("queued");

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

console.log(JSON.stringify(out));
process.exit(0);
