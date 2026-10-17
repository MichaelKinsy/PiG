// Imported by the conformance gate before the scenario program (durable/contract impls.toml `setup`): it loads the core module
// the run names in DCORE_WASM, so every Harness the scenario opens runs on that build.
import { coreFromEnvironment } from "./core-env.ts"
import { useCore, useStorageProfile } from "./harness.ts"

const module = await coreFromEnvironment()
if (!module) throw new Error("contract-setup: DCORE_WASM or DCORE_NATIVE names the core to run")
useCore(module)
// The reference runs MemoryStorage as its capsql facade behind a lazy proxy and the node adapters on the facade directly (impls/storage.mjs); the core models that adapter's hops (ABI.md 5.3, S7).
// CONTRACT_STORAGE_PROFILE=node runs the reference's own Node SQLite adapter (a statement is one tick; hopcost.Node) for the node adapters.
const adapter = process.env.CONTRACT_STORAGE_PROFILE === "node" ? "node" : "gate-capsql"
useStorageProfile({ memory: "gate", "node-sqlite": adapter, jsonl: adapter })

// CONTRACT_RECYCLE_AFTER=<seq> (lib/robust.mjs recycle): the capture arms one trap after that commit, and the next event any session sends to a
// Wasm core throws as a trap would, before the core runs it. The host must recycle the instance and the program must finish as if nothing happened.
if (process.env.CONTRACT_RECYCLE_AFTER !== undefined) {
  const { CoreTrap, WasmSession } = await import("../wasm.ts")
  const armed = Symbol.for("pig.contract.trapNext")
  const proto = WasmSession.prototype as unknown as Record<"step" | "rows", (...args: unknown[]) => unknown>
  for (const name of ["step", "rows"] as const) {
    const real = proto[name]
    proto[name] = function (this: unknown, ...args: unknown[]) {
      const state = globalThis as Record<symbol, unknown>
      if (state[armed]) {
        state[armed] = false
        process.stderr.write(`[contract] recycle injected after commit ${process.env.CONTRACT_RECYCLE_AFTER}\n`)
        throw new CoreTrap(`injected trap after commit ${process.env.CONTRACT_RECYCLE_AFTER}`)
      }
      return real.apply(this, args)
    }
  }
}
