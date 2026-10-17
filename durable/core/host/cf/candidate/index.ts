// The candidate JS API of the gate (durable/contract README, "Writing a candidate"): this module replaces
// packages/durable/src/index.ts, so every upstream example and upstream test runs unmodified against the Durable core.
// The core module is the one this build produced next to `src/` (../core.wasm).
import { readFileSync } from "node:fs"
import { useCore } from "@@FACADE@@/harness.ts"

export * from "@@FACADE@@/index.ts"
export { MemoryStorage } from "./storage/memory.ts"

useCore(new WebAssembly.Module(readFileSync(new URL("../core.wasm", import.meta.url))))
