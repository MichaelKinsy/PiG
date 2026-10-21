import { AsyncLocalStorage } from "node:async_hooks";

// pig additive (D189): Pi runs an extension's timer and immediate callbacks on the thread that runs the agent, so such a callback runs only when the agent's run awaits real work or the loop is idle (agent-loop.ts, rpc-mode.ts). This runtime is another process, whose loop runs the callback at once and writes the host calls it makes. The host holds those calls until Pi's loop would have run the callback, so each call says it came from a timer or an immediate: CallPayload.Timer.
//
// A callback runs in a store that every promise continuation and later callback it starts inherits, so the work a callback causes is marked as well. A request the host sends is served outside the store: it starts on the host's step, not on the timer's. The store says which tool call's handler queued the callback, because the host runs the calls of a callback after that call's tool_execution_end (CallPayload.OriginToolCallID).
const origin = new AsyncLocalStorage();

// The tool call whose handler is running the current code, undefined outside one.
let currentToolCallId = () => undefined;
export function setToolCallIdSource(fn) {
  currentToolCallId = fn;
}

// toolOfQueuer is the tool call whose handler queues the callback now: the one a callback that queues this one belongs to, else the one whose handler runs.
function toolOfQueuer() {
  return originToolCallId() ?? currentToolCallId();
}

let installed = false;

// While the host says a stdin command runs (setTimerDefer), a timer or immediate callback waits: Pi's loop runs the command's microtasks and its response before any timer. The waiting callbacks run, in order, in a later iteration once the host lifts the hold.
let deferring = false;
let waiting = [];
const rawSetImmediate = setImmediate;

// A handler that is still running awaits its own timer or I/O, which is a turn of Pi's loop, so a callback runs. The runtime says whether one is: only a runtime whose handlers all returned holds its callbacks.
let handlersRunning = () => false;
export function setHandlersRunning(fn) {
  handlersRunning = fn;
}

export function setTimerDefer(on) {
  deferring = on;
  if (on || waiting.length === 0) return;
  rawSetImmediate(() => {
    if (deferring) return;
    const batch = waiting;
    waiting = [];
    for (const run of batch) run();
  });
}

// installTimerOrigin wraps the global timer functions once. The wrapper hands the callback to the original, so the timer object, its ref and unref, and clearTimeout and clearImmediate are the original's.
export function installTimerOrigin() {
  if (installed) return;
  installed = true;
  for (const name of ["setTimeout", "setInterval", "setImmediate"]) {
    const original = globalThis[name];
    const wrapped = function (callback, ...rest) {
      if (typeof callback !== "function") return Reflect.apply(original, this, [callback, ...rest]);
      const tool = toolOfQueuer();
      const fromTimer = (...args) => {
        const run = () => origin.run({ kind: "timer", tool }, callback, ...args);
        if (deferring && !handlersRunning()) waiting.push(run);
        else return run();
      };
      return Reflect.apply(original, this, [fromTimer, ...rest]);
    };
    // setTimeout and setImmediate carry util.promisify.custom, which promisify reads.
    for (const key of Reflect.ownKeys(original)) {
      const descriptor = Object.getOwnPropertyDescriptor(original, key);
      if (descriptor) Object.defineProperty(wrapped, key, descriptor);
    }
    Object.defineProperty(wrapped, "name", { value: name, configurable: true });
    globalThis[name] = wrapped;
  }
  // Pi runs process.nextTick callbacks between two steps of the agent's chain: after the tool_execution_end of the tool that queued them (agent-loop.ts), so the host releases the calls of a tick callback there.
  const nextTick = process.nextTick;
  process.nextTick = function (callback, ...rest) {
    if (typeof callback !== "function") return Reflect.apply(nextTick, this, [callback, ...rest]);
    const tool = toolOfQueuer();
    return Reflect.apply(nextTick, this, [(...args) => origin.run({ kind: "tick", tool }, callback, ...args), ...rest]);
  };
}

// originKind is "timer" for a timer or immediate callback, or work one started, "tick" for a process.nextTick callback or work one started, and undefined elsewhere. Work started inside a callback of the other kind takes that callback's kind.
export function originKind() {
  const kind = origin.getStore()?.kind;
  return kind === "timer" || kind === "tick" ? kind : undefined;
}

// originToolCallId is the tool call whose handler queued the callback the current code runs in, or work such a callback started: undefined when the callback was queued outside a tool call's handler, and elsewhere.
export function originToolCallId() {
  return origin.getStore()?.tool;
}

// outsideTimer returns fn running outside any timer callback's store.
export function outsideTimer(fn) {
  return (...args) => origin.run(false, fn, ...args);
}
