// Instance recycling (Durable plan 2 item 3, ADR D16): a core that traps is dropped, a fresh instance is cold-opened from SQLite, and
// the work the trapped step carried runs again once; a second trap on the same event fails the conversation. The same path recycles
// an instance at a quiet point. Every test runs a real core (the toolchain's build) under a real pool.
import assert from "node:assert/strict"
import { existsSync, readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineDoc, defineExtension, defineTool } from "../src/facade/index.ts"
import { Harness } from "../src/facade/harness.ts"
import { CorePool } from "../src/pool.ts"
import { EVENT } from "../src/abi.ts"
import { FatalError, RecycledError, RecycledUncertainError } from "../src/host.ts"
import { CoreTrap } from "../src/wasm.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { inject } from "./inject.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const wasm = join(root, "dist", `core-${toolchain}.wasm`)
const skip = !existsSync(wasm)
const module = () => new (WebAssembly.Module as any)(readFileSync(wasm)) as WebAssembly.Module

const blocks = (m: any): any[] => (typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content)
const textOf = (m: any) => (m ? blocks(m).filter((b: any) => b.type === "text").map((b: any) => b.text).join("") : "")

/** A deterministic model: the answer is a function of the transcript, so a request the host repeats gets the same reply. */
function rig(lookups = 2) {
  /** While set, the first lookup waits for it (or for the host to cancel it): a turn the test can hold in flight. */
  const hold: { gate?: Promise<void> } = {}
  const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
  const models = createModels()
  models.setProvider(faux.provider)
  const respond = async (transcript: any) => {
    faux.appendResponses([respond])
    const messages = transcript.messages
    const last = messages.findLastIndex((m: any) => m.role === "user")
    const done = messages.slice(last + 1).filter((m: any) => m.role === "toolResult").length
    if (done < lookups) return fauxAssistantMessage(fauxToolCall("lookup", { n: done + 1 }, { id: `call-${done}` }), { stopReason: "toolUse" })
    return fauxAssistantMessage(`answered after ${done} lookups: ${textOf(messages[last])}`)
  }
  faux.setResponses([respond])
  const executed: number[] = []
  const registry = createRegistry()
  registry.install(defineExtension({
    name: "lookups",
    tools: [defineTool({ name: "lookup", description: "Look up record n", parameters: Type.Object({ n: Type.Number() }), replay: "safe", execute: async ({ n }: { n: number }, _api: unknown, call: any) => {
      executed.push(n)
      if (hold.gate) await Promise.race([hold.gate, new Promise<void>(resolve => call?.abortSignal?.addEventListener("abort", () => resolve()))])
      return { output: [{ type: "text", text: `record ${n}` }] }
    } })],
  }))
  const model = { provider: faux.getModel().provider, modelId: faux.getModel().id }
  return { models, registry, executed, model, hold }
}

type Rig = ReturnType<typeof rig>
const reports: unknown[] = []

async function open(path: string, r: Rig, core: unknown = module(), extra: Record<string, unknown> = {}) {
  return Harness.open(await openNodeSqliteStorage(path) as never, { models: r.models, registry: r.registry, core, onReport: (e: unknown) => { reports.push(e) }, ...extra } as never, context)
}

/** What a conversation left, without ids and clocks: the transcript's entry kinds and the text of its messages. */
async function transcript(h: Harness, id: number) {
  const entries = (await (await h.conversation(id, context))!.entries({}, 500, undefined, context) as any).items.reverse()
  return entries.map((e: any): [string, string] => [e.kind, (e.model ?? []).map((m: any) => `${m.role}:${textOf(m) || JSON.stringify(blocks(m).map((b: any) => b.name ?? b.type))}`).join("|")])
}

/** A caller's own transaction that a trap interrupted before its commit fails with a RecycledError; nothing of it was committed, and the caller makes the call again. */
async function again<T>(call: () => Promise<T>): Promise<T> {
  for (let attempt = 0; ; attempt++) {
    try { return await call() } catch (error) { if (!(error instanceof RecycledError) || attempt >= 3) throw error }
  }
}

/** One turn on a fresh store. Returns what the conversation left and the host's counters. */
async function turn(r: Rig, path: string, text = "turn a") {
  const h = await open(path, r)
  const conversation = await again(() => h.root(context, { agent: { model: r.model } } as never))
  const submission = await conversation.submit({ type: "input", content: text, requestId: "q1" }, context)
  const settled = await submission.wait(context)
  const entries = await transcript(h, conversation.id)
  await h.close(context)
  const counters = { ...h.host.counters }
  return { status: settled.status as string, entries, counters, executed: [...r.executed] }
}

test("a turn with tools on the real core: the baseline the trap sweeps compare against", { skip }, async () => {
  const dir = tempDir("recycle-base")
  try {
    const r = rig()
    const base = await turn(r, join(dir, "a.sqlite"))
    assert.equal(base.status, "done")
    assert.deepEqual(base.entries.map((e: any) => e[0]), ["pi.user", "pi.system", "pi.assistant", "pi.tool-result", "pi.assistant", "pi.tool-result", "pi.assistant"])
    assert.deepEqual(base.executed, [1, 2])
    assert.equal(base.counters.recycles, 0)
    assert.equal(base.counters.traps, 0)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

test("a trap at any event of a tool turn: the host reopens from SQLite and the conversation finishes exactly as the uninterrupted one", { skip, timeout: 280_000 }, async () => {
  const dir = tempDir("recycle-sweep")
  try {
    const counting = inject()
    const base = await turn(rig(1), join(dir, "base.sqlite"))
    const events = counting.state.events
    counting.restore()
    assert.ok(events > 30, `the turn sends ${events} events to the core`)
    let recovered = 0
    for (const when of ["before", "after"] as const) {
      for (let at = 0; at < events; at++) {
        const fault = inject(n => n === at ? when : undefined)
        try {
          const r = rig(1)
          const got = await turn(r, join(dir, `${when}-${at}.sqlite`))
          assert.equal(fault.state.traps, 1, `${when} ${at}: the trap fired`)
          assert.equal(got.status, "done", `${when} ${at}: the submission settles`)
          assert.deepEqual(got.entries, base.entries, `${when} ${at}: the transcript is the uninterrupted one, no entry lost or repeated`)
          assert.ok(got.counters.recycles >= 1 && got.counters.traps === 1, `${when} ${at}: one trap, ${got.counters.recycles} recycles`)
          // A tool whose result was lost with the trap runs again (replay: "safe"); none runs that the turn did not call for.
          assert.deepEqual([...new Set(got.executed)], [1], `${when} ${at}: the lookups that ran`)
          assert.ok(got.executed.length <= 2, `${when} ${at}: at most one lookup repeated, ran ${got.executed}`)
          recovered++
        } finally { fault.restore() }
      }
    }
    assert.equal(recovered, events * 2)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

test("a submission without a request ID: at any trap point it is admitted once, and a caller the host cannot answer for certain is told so", { skip, timeout: 280_000 }, async () => {
  const dir = tempDir("recycle-unkeyed")
  // Without a request ID nothing in the store names the submission, so a submission the old core admitted and had not yet answered
  // (the answer follows the commit's publication hops) must not be sent to the new core again: that would admit it twice.
  const unkeyed = async (r: Rig, path: string) => {
    const h = await open(path, r)
    const conversation = await again(() => h.root(context, { agent: { model: r.model } } as never))
    let outcome: string
    try {
      outcome = (await (await conversation.submit({ type: "input", content: "turn a" }, context)).wait(context)).status as string
    } catch (error) {
      if (!(error instanceof RecycledUncertainError)) throw error
      outcome = "uncertain"
    }
    await h.waitForIdle(context)
    const entries = await transcript(h, conversation.id)
    await h.close(context)
    return { outcome, entries }
  }
  try {
    const counting = inject()
    const base = await unkeyed(rig(1), join(dir, "base.sqlite"))
    const events = counting.state.events
    counting.restore()
    assert.equal(base.outcome, "done")
    const seen: Record<string, number> = {}
    for (const when of ["before", "after"] as const) {
      for (let at = 0; at < events; at++) {
        const fault = inject(n => n === at ? when : undefined)
        try {
          const got = await unkeyed(rig(1), join(dir, `${when}-${at}.sqlite`))
          assert.equal(fault.state.traps, 1, `${when} ${at}: the trap fired`)
          assert.ok(got.entries.filter((e: any) => e[0] === "pi.user").length <= 1, `${when} ${at}: the input is admitted at most once, got ${got.entries.map((e: any) => e[0])}`)
          if (got.outcome === "done") assert.deepEqual(got.entries, base.entries, `${when} ${at}: the transcript is the uninterrupted one`)
          else assert.ok(got.entries.length === 0 || JSON.stringify(got.entries) === JSON.stringify(base.entries), `${when} ${at}: an uncertain submission was admitted whole or not at all`)
          seen[got.outcome] = (seen[got.outcome] ?? 0) + 1
        } finally { fault.restore() }
      }
    }
    assert.ok((seen.uncertain ?? 0) > 0, `some trap point falls between the admission's commit and its answer: ${JSON.stringify(seen)}`)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

test("a second trap on the same event fails the conversation with that error; a Harness opened over the store finishes the work", { skip }, async () => {
  const dir = tempDir("recycle-poison")
  const path = join(dir, "a.sqlite")
  try {
    const r = rig()
    const poisoned = inject((_n, kind) => kind === EVENT.submit ? "before" : undefined)
    const h = await open(path, r)
    const conversation = await again(() => h.root(context, { agent: { model: r.model } } as never))
    await assert.rejects(conversation.submit({ type: "input", content: "poison", requestId: "q1" }, context), (error: unknown) =>
      error instanceof FatalError && /trapped again while recovering/.test(error.message) && error.cause2 instanceof CoreTrap)
    assert.equal(poisoned.state.traps, 2, "the event trapped, then the refeed of it trapped")
    assert.equal(h.host.counters.traps, 2)
    assert.equal(h.host.counters.recycles, 1, "one recycle, not a loop")
    assert.ok(h.host.discarded, "the conversation failed: the host is discarded")
    await assert.rejects(h.waitForIdle(context), /trapped again/, "every later wait rejects with the failure")
    assert.ok(reports.some(e => e instanceof FatalError && /trapped again/.test(e.message)), "the failure is reported")
    poisoned.restore()
    // The trap cost nothing durable: a Harness over the same store runs the same submission to its end.
    const next = await open(path, r)
    const same = (await next.conversation(1, context))!
    const settled = await (await same.submit({ type: "input", content: "poison", requestId: "q1" }, context)).wait(context)
    assert.equal(settled.status, "done")
    assert.equal((await transcript(next, 1)).filter((e: any) => e[0] === "pi.user").length, 1, "the input is in the transcript once")
    await next.close(context)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

const tick = (ms = 20) => new Promise(resolve => setTimeout(resolve, ms))

/** Resolves when the pool next takes an instance back: the host's recycle at a quiet point (or a close). Armed before the work whose end is awaited. */
function nextRelease(pool: CorePool) {
  return new Promise<void>(resolve => {
    const released = pool.released.bind(pool)
    pool.released = instance => { pool.released = released; released(instance); resolve() }
  })
}

test("quiet-point recycling: over the high-water mark a quiet host drops its instance and the next call reopens from SQLite; nothing in flight is recycled", { skip, timeout: 30_000 }, async () => {
  const dir = tempDir("recycle-quiet")
  try {
    const mod = module()
    const pool = new CorePool(mod, { highWater: 1 })
    const r = rig(1)
    const h = await open(join(dir, "a.sqlite"), r, mod, { pool })
    const conversation = await again(() => h.root(context, { agent: { model: r.model } } as never))
    // A turn held in flight: the lookup waits, so the core has work running on its instance and the host is not quiet.
    let release!: () => void
    r.hold.gate = new Promise<void>(resolve => { release = resolve })
    const held = await conversation.submit({ type: "input", content: "turn held", requestId: "held" }, context)
    while (r.executed.length === 0) await tick()
    const before = { recycles: h.host.counters.recycles, created: pool.created }
    await tick(50)
    assert.equal(h.host.counters.recycles, before.recycles, "the instance under a running tool is not recycled")
    assert.equal(pool.created, before.created, "and no new instance was made for it")
    assert.equal(h.quiet, false)
    let dropped = nextRelease(pool)
    release()
    r.hold.gate = undefined
    assert.equal((await held.wait(context)).status, "done")
    // Further turns: each quiet point over the mark recycles, and each next call opens a fresh instance. The core is still running
    // hops chains (Pi's line-job awaits, about forty microtask hops) when a turn's wait resolves, so the quiet point is awaited
    // (`dropped`), not timed and not assumed: a caller that goes straight on to its next call leaves none.
    for (const id of ["a", "b"]) {
      await dropped
      assert.equal(pool.instances, 0, "a quiet host over the mark has given its instance back")
      dropped = nextRelease(pool)
      assert.equal((await (await conversation.submit({ type: "input", content: `turn ${id}`, requestId: id }, context)).wait(context)).status, "done")
    }
    await dropped
    assert.equal(pool.instances, 0, "and so has the host after the last turn")
    const entries = await transcript(h, conversation.id)
    assert.equal(entries.filter((e: any) => e[0] === "pi.user").length, 3)
    assert.equal(entries.filter((e: any) => e[0] === "pi.assistant").length, 6, "two assistant entries per turn: the lookup call and the answer")
    assert.equal(h.host.counters.recycles, 3, `recycled at ${h.host.counters.recycles} quiet points`)
    assert.equal(pool.created, 1 + h.host.counters.recycles, "one fresh instance per recycle, none leaked")
    assert.ok(pool.instances <= 1, `${pool.instances} instances alive`)
    assert.equal(h.host.counters.traps, 0)
    await h.close(context)
    assert.equal(pool.instances, 0, "closing releases the last instance")
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

test("a caller's transaction that a trap interrupts either committed or fails with a RecycledError, never both and never neither", { skip }, async () => {
  const dir = tempDir("recycle-tx")
  try {
    const mod = module()
    const probe = async (plan: (n: number) => "before" | "after" | undefined, path: string) => {
      const r = rig()
      const h = await open(path, r, mod)
      const fault = inject(n => plan(n - baseline))
      const baseline = fault.state.events
      let outcome: "resolved" | "recycled"
      try {
        await h.commit(async tx => { await tx.createConversation({ ownership: { kind: "ownerless" } }) }, context)
        outcome = "resolved"
      } catch (error) {
        if (!(error instanceof RecycledError)) throw error
        outcome = "recycled"
      } finally { fault.restore() }
      const rows = Number((h as any).store.all("SELECT COUNT(*) FROM conversations", [])[0][0])
      await h.close(context)
      return { outcome, rows, events: fault.state.events - baseline, traps: fault.state.traps }
    }
    const clean = await probe(() => undefined, join(dir, "clean.sqlite"))
    assert.deepEqual([clean.outcome, clean.rows], ["resolved", 1])
    const seen = { resolved: 0, recycled: 0 }
    for (const when of ["before", "after"] as const) {
      for (let at = 0; at < clean.events; at++) {
        const got = await probe(n => n === at ? when : undefined, join(dir, `${when}-${at}.sqlite`))
        assert.equal(got.traps, 1, `${when} ${at}`)
        // Resolved means the commit is in the store; a RecycledError means it is not (the message says so).
        assert.equal(got.rows, got.outcome === "resolved" ? 1 : 0, `${when} ${at}: ${got.outcome}, ${got.rows} conversations`)
        seen[got.outcome]++
      }
    }
    assert.ok(seen.recycled > 0 && seen.resolved > 0, `both outcomes occur across the trap points: ${JSON.stringify(seen)}`)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

test("a caller's transaction interrupted while a turn is live: the reopened core's own open commit does not make it look committed", { skip }, async () => {
  const dir = tempDir("recycle-tx-live")
  try {
    const mod = module()
    const probe = async (plan: (n: number) => "before" | "after" | undefined, path: string) => {
      const r = rig(1)
      const h = await open(path, r, mod)
      const conversation = await again(() => h.root(context, { agent: { model: r.model } } as never))
      // A tool held in flight: the store has a live run, which the reopened core reconciles with a commit as it opens.
      let release!: () => void
      r.hold.gate = new Promise<void>(resolve => { release = resolve })
      const held = await conversation.submit({ type: "input", content: "turn held", requestId: "held" }, context)
      while (r.executed.length === 0) await tick()
      const fault = inject(n => plan(n - baseline))
      const baseline = fault.state.events
      let outcome: "resolved" | "recycled"
      try {
        await h.commit(async tx => { await tx.createConversation({ ownership: { kind: "ownerless" } }) }, context)
        outcome = "resolved"
      } catch (error) {
        if (!(error instanceof RecycledError)) throw error
        outcome = "recycled"
      } finally { fault.restore() }
      const created = Number((h as any).store.all("SELECT COUNT(*) FROM conversations", [])[0][0]) - 1
      const events = fault.state.events - baseline, traps = fault.state.traps
      r.hold.gate = undefined
      release()
      assert.equal((await held.wait(context)).status, "done")
      await h.close(context)
      return { outcome, created, events, traps }
    }
    const clean = await probe(() => undefined, join(dir, "clean.sqlite"))
    assert.deepEqual([clean.outcome, clean.created], ["resolved", 1])
    const seen = { resolved: 0, recycled: 0 }
    for (const when of ["before", "after"] as const) {
      for (let at = 0; at < clean.events; at++) {
        const got = await probe(n => n === at ? when : undefined, join(dir, `${when}-${at}.sqlite`))
        assert.equal(got.traps, 1, `${when} ${at}`)
        assert.equal(got.created, got.outcome === "resolved" ? 1 : 0, `${when} ${at}: ${got.outcome}, ${got.created} conversations created`)
        seen[got.outcome]++
      }
    }
    assert.ok(seen.recycled > 0, `a trap on the transaction's end fails it: ${JSON.stringify(seen)}`)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

const Live = defineDoc<{ items: number[] }>({ kind: "recycle.live", checkpointWhen: () => false, version: 1, scope: "conversation", history: "latest", fork: "initial", initial: () => ({ items: [] }) })
const noModels = { getModel: () => undefined, streamSimple: () => { throw new Error("a Session has no models") } }

test("a watch survives a trap at any event of a commit: it ends on the committed value and goes on receiving frames", { skip }, async () => {
  const dir = tempDir("recycle-watch")
  try {
    const mod = module()
    const probe = async (plan: (n: number) => "before" | "after" | undefined, path: string) => {
      const h = await Harness.open(await openNodeSqliteStorage(path) as never, { models: noModels, registry: createRegistry() as never, mode: "session", core: mod } as never, context)
      const id = await again(() => h.commit(async (tx: any) => (await tx.createConversation({ ownership: { kind: "ownerless" } })).id, context))
      await again(() => h.commit(async (tx: any) => { (await tx.doc(Live, id)).items.push(0) }, context))
      const watch = (await h.watchDoc(Live, id, context))! as any
      const seen: number[][] = []
      watch.start(async (value: { items: number[] }) => { seen.push([...value.items]) })
      const fault = inject(n => plan(n - baseline))
      const baseline = fault.state.events
      try {
        await again(() => h.commit(async (tx: any) => { (await tx.doc(Live, id)).items.push(1) }, context))
        await tick(30)
        // The watch holds what the store holds, and a later commit reaches it.
        const committed = (await h.snapshot(Live, id, context) as { items: number[] }).items
        const held = [...watch.value.items]
        await again(() => h.commit(async (tx: any) => { (await tx.doc(Live, id)).items.push(2) }, context))
        await tick(30)
        const after = [...watch.value.items]
        return { committed, held, after, seen, events: fault.state.events - baseline, traps: fault.state.traps }
      } finally { fault.restore(); await h.close(context) }
    }
    const clean = await probe(() => undefined, join(dir, "clean.sqlite"))
    assert.deepEqual([clean.committed, clean.held, clean.after], [[0, 1], [0, 1], [0, 1, 2]])
    for (const when of ["before", "after"] as const) {
      for (let at = 0; at < clean.events; at++) {
        const got = await probe(n => n === at ? when : undefined, join(dir, `${when}-${at}.sqlite`))
        assert.equal(got.traps, 1, `${when} ${at}: the trap fired`)
        assert.deepEqual(got.committed, [0, 1], `${when} ${at}: the commit is in the store exactly once`)
        assert.deepEqual(got.held, got.committed, `${when} ${at}: the watch holds the committed value`)
        assert.deepEqual(got.after, [0, 1, 2], `${when} ${at}: the watch is alive on the new core and sees the next commit`)
      }
    }
  } finally { rmSync(dir, { recursive: true, force: true }) }
})
