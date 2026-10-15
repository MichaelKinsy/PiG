// Fault injection for a real Wasm core: every event a session sends to the core (step or rows) is counted across all sessions of
// the process, and a plan says which ones trap. The trap is a CoreTrap thrown where the Wasm trap would surface, before the core
// ran the event ("before": the step never happened) or after it ran ("after": the step happened and its output was lost, so
// nothing it carried reached the store).
import { EVENT } from "../src/abi.ts"
import { CoreTrap, WasmSession } from "../src/wasm.ts"

export interface Plan { (n: number, kind: number): "before" | "after" | undefined }

export function inject(plan: Plan = () => undefined) {
  const proto = WasmSession.prototype as unknown as Record<"step" | "rows", (...args: unknown[]) => unknown>
  const real = { step: proto.step, rows: proto.rows }
  const state = { events: 0, traps: 0, plan }
  const wrap = (name: "step" | "rows") => function (this: unknown, ...args: unknown[]) {
    const n = state.events++
    const kind = name === "rows" ? EVENT.rows : args[0] as number
    const when = state.plan(n, kind)
    if (when === undefined) return real[name].apply(this, args)
    state.traps++
    if (when === "before") throw new CoreTrap(`injected trap before event ${n}`)
    real[name].apply(this, args)
    throw new CoreTrap(`injected trap after event ${n}`)
  }
  proto.step = wrap("step")
  proto.rows = wrap("rows")
  return { state, restore() { proto.step = real.step; proto.rows = real.rows } }
}
