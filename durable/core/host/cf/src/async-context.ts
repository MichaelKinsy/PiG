// The host's hold on AsyncLocalStorage (ABI section 5.4). A call a client makes into the host runs in the async context of its caller,
// and Pi runs the work that call starts in that context (a model call in the context `submit` was made in, a tool in its turn's),
// so the host takes a snapshot of the context at each call, tags the event with it, and starts the effects and runs the listeners
// the core stamps with that tag inside the snapshot again. A runtime without `node:async_hooks` (a Worker without nodejs_compat) has
// no stores for a program to set, so it has no contexts to carry.

/** Runs a function in the async context a snapshot captured, as `AsyncLocalStorage.snapshot()`'s function does. */
export type ContextRun = <R>(fn: () => R) => R

const hooks = await import("node:async_hooks").catch(() => undefined)

/** A snapshot of the async context of the caller, or undefined where the runtime has none. */
export const captureContext: () => ContextRun | undefined = hooks === undefined ? () => undefined : () => hooks.AsyncLocalStorage.snapshot() as ContextRun
