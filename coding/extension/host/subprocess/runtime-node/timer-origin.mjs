import { AsyncLocalStorage } from "node:async_hooks";

// pig additive (D5): Pi runs an extension's timer and immediate callbacks on the thread that runs the agent, so such a callback runs only when the agent's run awaits real work or the loop is idle (agent-loop.ts, rpc-mode.ts). This runtime is another process, whose loop runs the callback at once and writes the host calls it makes. The host holds those calls until Pi's loop would have run the callback, so each call says it came from a timer or an immediate: CallPayload.Timer.
//
// A callback runs in a store that every promise continuation and later callback it starts inherits, so the work a callback causes is marked as well. A request the host sends is served outside the store: it starts on the host's step, not on the timer's.
const origin = new AsyncLocalStorage();

let installed = false;

// installTimerOrigin wraps the global timer functions once. The wrapper hands the callback to the original, so the timer object, its ref and unref, and clearTimeout and clearImmediate are the original's.
export function installTimerOrigin() {
  if (installed) return;
  installed = true;
  for (const name of ["setTimeout", "setInterval", "setImmediate"]) {
    const original = globalThis[name];
    const wrapped = function (callback, ...rest) {
      if (typeof callback !== "function") return Reflect.apply(original, this, [callback, ...rest]);
      const fromTimer = (...args) => origin.run(true, callback, ...args);
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
}

// fromTimer reports whether the running code is a timer or immediate callback, or work one started.
export function fromTimer() {
  return origin.getStore() === true;
}

// outsideTimer returns fn running outside any timer callback's store.
export function outsideTimer(fn) {
  return (...args) => origin.run(false, fn, ...args);
}
