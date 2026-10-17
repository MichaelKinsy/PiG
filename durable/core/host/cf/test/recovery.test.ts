// A core that traps mid-session loses nothing Pi would keep (ABI section 4.6, ADR D16): the handle is discarded, the host reopens from
// SQLite with a fresh instance, and the Session continues with exactly the committed state. Every event of a small workload is made
// to trap in turn, once before the core runs it and once after (the step's statements are lost, as in a real trap).
import assert from "node:assert/strict"
import { existsSync, readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createRegistry, defineDoc } from "../src/facade/index.ts"
import { Harness } from "../src/facade/harness.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { CoreTrap, WasmCore, type WasmSession } from "../src/wasm.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const wasm = join(root, "dist", `core-${toolchain}.wasm`)
const context = BACKGROUND_CONTEXT
const COMMITS = 4
const Live = defineDoc<{ items: number[] }>({ kind: "recovery.live", version: 1, scope: "conversation", history: "latest", fork: "initial", initial: () => ({ items: [] }) })
const noModels = { getModel: () => undefined, streamSimple: () => { throw new Error("a Session has no models") } }
const registry = createRegistry()

/** The real core with a trap armed for the nth event (counting every event of every session opened through it). */
function trapping(module: WebAssembly.Module, plan: { at: number; when: "before" | "after" }) {
  const real = new WasmCore(module)
  const counter = { events: 0, trapped: false }
  const wrap = (session: WasmSession) => new Proxy(session, {
    get(target, name) {
      const value = (target as never as Record<string | symbol, unknown>)[name]
      if (name !== "step" && name !== "rows") return typeof value === "function" ? value.bind(target) : value
      return (...args: unknown[]) => {
        const n = counter.events++
        if (n !== plan.at) return (value as (...a: unknown[]) => unknown).apply(target, args)
        counter.trapped = true
        if (plan.when === "before") throw new CoreTrap("injected trap before the step")
        ;(value as (...a: unknown[]) => unknown).apply(target, args)
        throw new CoreTrap("injected trap after the step")
      }
    },
  })
  return { counter, core: { newSession: () => wrap(real.newSession()), sqlTable: real.sqlTable } }
}

const open = (path: string, core: unknown): Promise<any> =>
  (Harness as any).open(openNodeSqliteStorage(path), { models: noModels, registry: registry as never, mode: "session", core } as never, context)

/** Run the workload from commit `from` (the conversation is created first when `convId` is unset); returns how many pushes were acknowledged. */
async function run(h: any, convId: { id?: number }, from: number, progress: { acked: number } = { acked: from }): Promise<number> {
  if (convId.id === undefined) convId.id = await h.commit(async (tx: any) => (await tx.createConversation({ ownership: { kind: "ownerless" } })).id, context)
  progress.acked = from
  for (let i = from; i < COMMITS; i++) {
    await h.commit(async (tx: any) => { (await tx.doc(Live, convId.id)).items.push(i) }, context)
    progress.acked = i + 1
  }
  return progress.acked
}

test("a trap at any event reopens from SQLite and the Session continues with exactly the committed state", { skip: !existsSync(wasm) }, async () => {
  const module = new (WebAssembly.Module as any)(readFileSync(wasm)) as WebAssembly.Module
  const dir = tempDir("recovery")
  try {
    // Uninterrupted run: how many events the workload takes.
    const base = trapping(module, { at: -1, when: "before" })
    const baseline = await open(join(dir, "base.sqlite"), base.core)
    const baseConv: { id?: number } = {}
    await run(baseline, baseConv, 0)
    await baseline.close(context)
    const events = base.counter.events
    assert.ok(events > COMMITS, `the workload sends ${events} events`)

    let trapsSeen = 0
    for (const when of ["before", "after"] as const) {
      for (let at = 0; at < events; at++) {
        const path = join(dir, `${when}-${at}.sqlite`)
        const trap = trapping(module, { at, when })
        const conv: { id?: number } = {}
        const progress = { acked: 0 }
        try {
          const h = await open(path, trap.core)
          await run(h, conv, 0, progress)
        } catch (error) {
          assert.ok(trap.counter.trapped, `${when} ${at}: only the injected trap fails the run: ${(error as Error).message}`)
        }
        if (!trap.counter.trapped) continue
        trapsSeen++
        // Reopen with a fresh instance over the same store: the committed prefix is exactly there, nothing partial.
        const fresh = new WasmCore(module)
        const again = await open(path, fresh)
        let id = conv.id
        if (id === undefined) {
          // The trap came before the conversation's creating commit was acknowledged: it either committed or it did not.
          const found = await (again as any).snapshot(Live, 1, context).catch(() => undefined)
          if (found !== undefined) id = 1
        }
        const items = id === undefined ? undefined : (await (again as any).snapshot(Live, id, context))?.items
        const have: number[] = items ?? []
        assert.deepEqual(have, Array.from({ length: have.length }, (_, i) => i), `${when} ${at}: the state is a prefix with no gap or duplicate`)
        assert.ok(have.length === progress.acked || have.length === progress.acked + 1, `${when} ${at}: acknowledged ${progress.acked}, found ${have.length}`)
        // Continue from the prefix: the final state is the uninterrupted run's.
        const resume: { id?: number } = { id }
        await run(again, resume, have.length)
        const final = (await (again as any).snapshot(Live, resume.id, context))?.items
        assert.deepEqual(final, Array.from({ length: COMMITS }, (_, i) => i), `${when} ${at}: the final state equals the uninterrupted run`)
        await again.close(context)
      }
    }
    assert.ok(trapsSeen >= events, `every event trapped at least once (${trapsSeen})`)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})
