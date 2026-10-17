// Several Durable Objects in one isolate (plan 2: one core instance per Durable Object). The facade a Worker uses (Harness.open with
// useCore(module)) instantiates a core per Harness, so a trap, an out-of-memory or a recycle takes one object and V8 shares the
// compiled code of the module. ADR-0001 D10's one-instance-per-isolate pool was measured by durable-bench (robust-durable
// bench/isolate.ts): four 3,500-turn objects in one TinyGo instance trapped at its memory cap.
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { EVENT } from "../src/abi.ts"
import { createRegistry, Harness, MemoryStorage } from "../src/facade/index.ts"
import { probeModule } from "./helpers.ts"

const models = () => { const m = createModels(); m.setProvider(fauxProvider().provider); return m as any }

test("every Harness one isolate opens with one core module has its own Wasm instance", async () => {
  const module = probeModule()
  const Real = WebAssembly.Instance
  let instances = 0
  const Counting = function (this: unknown, m: WebAssembly.Module, imports?: WebAssembly.Imports) { instances++; return new Real(m, imports) } as unknown as typeof WebAssembly.Instance
  Object.defineProperty(WebAssembly, "Instance", { value: Counting, configurable: true, writable: true })
  try {
    const opened = []
    for (let i = 0; i < 4; i++) opened.push(await Harness.open(new MemoryStorage(), { models: models(), registry: createRegistry(), core: module } as any, BACKGROUND_CONTEXT))
    assert.equal(instances, 4, `four Durable Objects in one isolate instantiated the core ${instances} times`)
    for (const h of opened) await h.close(BACKGROUND_CONTEXT)
  } finally {
    Object.defineProperty(WebAssembly, "Instance", { value: Real, configurable: true, writable: true })
  }
})

test("a trap on one Harness's instance is recovered on a new instance and leaves the other Harnesses running", async () => {
  const module = probeModule()
  const { CoreTrap } = await import("../src/wasm.ts")
  const open = () => Harness.open(new MemoryStorage(), { models: models(), registry: createRegistry(), core: module, onReport: () => {} } as any, BACKGROUND_CONTEXT)
  const a = await open(), b = await open()
  ;((a as any).host.core as any).step = () => { throw new CoreTrap("unreachable") }
  // The host drops the trapped instance, reopens on a new one and runs the event again (ADR D16). The probe refuses an input whose
  // content is "reject": the rejected step proves the event ran on the new core.
  const step = (a as any).host.sendJSON(EVENT.submit, { type: "input", content: "reject", requestId: "r1" })
  assert.equal(step?.status, 1, "the submit was run again on a new core and answered")
  assert.equal((a as any).host.counters.traps, 1)
  assert.equal((a as any).host.counters.recycles, 1)
  assert.equal((a as any).host.discarded, undefined, "the Harness that trapped is alive on its new instance")
  assert.equal((b as any).host.discarded, undefined, "the other Harness keeps its instance")
  assert.equal((b as any).host.counters.traps, 0)
  await b.close(BACKGROUND_CONTEXT)
})

test("a Harness whose open fails returns its instance to the pool", async () => {
  const { CorePool } = await import("../src/pool.ts")
  const module = probeModule()
  const pool = new CorePool(module)
  // The open payload lists the catalogue; a models object that throws there fails Harness.open after the lease is taken.
  const failing = { ...models(), getModels: () => { throw new Error("catalogue unavailable") } }
  for (let i = 0; i < 3; i++) await assert.rejects(Harness.open(new MemoryStorage(), { models: failing, registry: createRegistry(), core: module, pool } as any, BACKGROUND_CONTEXT), /catalogue unavailable/)
  assert.equal(pool.instances, 0, `${pool.instances} instances of failed opens stay in the pool`)
})
