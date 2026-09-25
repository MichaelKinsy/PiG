import { AsyncLocalStorage } from "node:async_hooks";

// An isolated extension is the only thing running in its Node process, so a
// module-level singleton was enough to answer getRuntime(). A Node cell (N8)
// loads several extensions in one process, and the pi-ai/pi-coding-agent
// shims call getRuntime() from code that runs long after install() returns
// (a tool call, a model stream), so the singleton would let one extension's
// shim call resolve to whichever extension constructed a Runtime last.
//
// runWithRuntime scopes getRuntime() to the AsyncLocalStorage context for the
// async chain it starts: every await, .then, and timer that chain schedules
// keeps seeing the same runtime, however long the extension's own connection
// loop keeps running. setRuntime keeps the old singleton behavior as the
// fallback for code that runs with no such context, which is exactly the
// single-extension-per-process (isolated) case cli.mjs still uses.
const perExtension = new AsyncLocalStorage();
let singleton = null;

export function setRuntime(next) {
  singleton = next;
}

export function getRuntime() {
  const scoped = perExtension.getStore();
  if (scoped) return scoped;
  if (!singleton) {
    throw new Error("pig TS runtime not initialized");
  }
  return singleton;
}

export function runWithRuntime(runtime, fn) {
  return perExtension.run(runtime, fn);
}
