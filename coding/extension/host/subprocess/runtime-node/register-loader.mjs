// The runtime's preload (Node --import, Bun --preload). Node compiles every module of a graph before it evaluates the first, so the bytecode cache starts here, in a module whose own graph is only the cache switch, and the hooks load behind it: the hooks' modules, the runtime graph and the entry then compile from the cache when an earlier run populated it.
import { cachePopulatedRuntime } from "./compile-cache.mjs";
if (typeof globalThis.Bun === "undefined") cachePopulatedRuntime();
await import("./register-hooks.mjs");
