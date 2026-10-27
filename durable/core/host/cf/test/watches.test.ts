// Watches and attached states over `watch.open` / `watch.close` and published frames (host/PROTOCOL.md), against a scripted core.
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { EVENT } from "../src/abi.ts"
import { createRegistry, defineDoc, Harness, MemoryStorage } from "../src/facade/index.ts"
import type { Notice } from "../src/wire.ts"
import { apiResult, apis, published, ScriptedCore, type Seen } from "./scripted.ts"

const context = BACKGROUND_CONTEXT
const models = () => { const faux = fauxProvider(); const m = createModels(); m.setProvider(faux.provider); return m as any }
const Notes = defineDoc({ kind: "example.notes", checkpointWhen: () => false, version: 1, scope: "session", initial: () => ({ n: 0 }) })

/**
 * A core that answers watch.open with `first` and replays `frames` (published notices) when the test sends a `registry` event
 * (the test's way of making the core publish). `sameStep` publishes the frames in the answer's own step.
 */
function watching(first: unknown, options: { sameStep?: { value: unknown; ops: unknown[] }[]; frames?: (watchId: number) => Notice[] } = {}) {
  let watchId = 0
  const core = new ScriptedCore((ev: Seen) => {
    if (ev.kind === EVENT.api && ev.body.op === "watch.open") {
      watchId = ev.body.watchId
      const frames = (options.sameStep ?? []).map(f => published({ watchId, frame: f }))
      return { notices: [apiResult(ev.id!, { ok: { value: first } }), ...frames] }
    }
    if (ev.kind === EVENT.api && ev.body.op === "watch.close") return { notices: [apiResult(ev.id!, { ok: null })] }
    if (ev.kind === EVENT.registry) return { notices: options.frames?.(watchId) ?? [] }
    return undefined
  })
  return core
}

const open = (core: ScriptedCore) =>
  Harness.open(new MemoryStorage(), { models: models(), registry: createRegistry(), core: { newSession: () => core, sqlTable: [] } } as any, context)

const pump = (h: Harness) => (h as any).host.sendJSON(EVENT.registry, {})
const closes = (core: ScriptedCore) => apis(core).filter(e => e.body.op === "watch.close")
const tick = () => new Promise(resolve => setTimeout(resolve, 5))

test("watchDoc delivers the first value, then each published frame to the listener in order, serialized", async () => {
  const core = watching({ n: 1 }, { frames: id => [
    published({ watchId: id, frame: { value: { n: 2 }, ops: [["r", { n: 2 }]] } }),
    published({ watchId: id, frame: { value: { n: 3 }, ops: [["r", { n: 3 }]] } }),
    published({ watchId: id + 100, frame: { value: { n: 99 }, ops: [] } }), // another watch's frame
  ] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  assert.deepEqual(watch.value, { n: 1 })
  assert.deepEqual(apis(core)[0]!.body.target.address.definition, "example.notes")
  const seen: unknown[] = []
  let running = 0, overlap = false
  watch.start(async ({ value, ops }) => { if (++running > 1) overlap = true; await tick(); seen.push([value, ops]); running-- })
  pump(h)
  await tick(); await tick(); await tick()
  assert.deepEqual(seen, [[{ n: 3 }, [["r", { n: 3 }]]]], "frames that arrive before a call runs fold into it: the newest value and the folded ops")
  assert.equal(overlap, false, "the listener never runs twice at once")
  assert.deepEqual(watch.value, { n: 3 }, "value is the latest delivered revision")
})

test("an absent document gives undefined and leaves nothing to close", async () => {
  const core = watching(null)
  const h = await open(core)
  assert.equal(await h.watchDoc(Notes, context), undefined)
  await h.close(context)
  assert.equal(closes(core).length, 0)
})

test("a frame published in the answer's own step is not lost", async () => {
  const core = watching({ n: 1 }, { sameStep: [{ value: { n: 2 }, ops: [["r", { n: 2 }]] }] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  const seen: unknown[] = []
  watch.start(async ({ value }) => { seen.push(value) })
  await tick()
  assert.deepEqual(seen, [{ n: 2 }])
})

test("stop() closes the core's watch once and ignores later frames", async () => {
  const core = watching({ n: 1 }, { frames: id => [published({ watchId: id, frame: { value: { n: 2 }, ops: [] } })] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  const seen: unknown[] = []
  watch.start(async ({ value }) => { seen.push(value) })
  assert.deepEqual(await watch.stop(), { reason: "stopped" })
  assert.deepEqual(await watch.stop(), { reason: "stopped" }, "stop is idempotent")
  pump(h)
  await tick()
  assert.deepEqual(seen, [])
  assert.equal(closes(core).length, 1)
  assert.equal(closes(core)[0]!.body.watchId, apis(core)[0]!.body.watchId)
})

test("a retired frame is a retired change with the last value and no ops: the watch goes on, and a document created at the address arrives whole", async () => {
  const core = watching({ n: 1 }, { frames: id => [
    published({ watchId: id, frame: { retired: true } }),
    published({ watchId: id, frame: { value: { n: 2 }, ops: [["r", { n: 2 }]] } }),
  ] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  const seen: unknown[] = []
  watch.start(async change => { seen.push(change) })
  pump(h)
  await tick(); await tick()
  assert.deepEqual(seen, [{ value: { n: 2 }, ops: [["r", { n: 2 }]], retired: true }], "folded into one call: the retirement shows, the later incarnation's value is the newest")
  assert.equal(closes(core).length, 0, "the core's watch stays open: the watch follows the address")
  assert.deepEqual(await watch.stop(), { reason: "stopped" })
  assert.equal(closes(core).length, 1)
})

test("a retirement alone repeats the last value the watch was given, with no ops", async () => {
  const core = watching({ n: 1 }, { frames: id => [published({ watchId: id, frame: { retired: true } })] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  const seen: unknown[] = []
  watch.start(async change => { seen.push(change) })
  pump(h)
  await tick(); await tick()
  assert.deepEqual(seen, [{ value: { n: 1 }, ops: [], retired: true }])
})

test("an end frame ends the watch once the retirement was delivered, and the core's watch is closed", async () => {
  const core = watching({ n: 1 }, { frames: id => [published({ watchId: id, frame: { retired: true } }), published({ watchId: id, frame: { end: true } })] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  const seen: unknown[] = []
  watch.start(async change => { seen.push(change) })
  pump(h)
  assert.deepEqual(await watch.closed, { reason: "ended" })
  assert.deepEqual(seen, [{ value: { n: 1 }, ops: [], retired: true }])
  assert.equal(closes(core).length, 1)
})

test("observeDoc hands the observer every change unfolded and ends it with the owner's end", async () => {
  const core = watching({ n: 1 }, { frames: id => [
    published({ watchId: id, frame: { value: { n: 2 }, ops: [["r", { n: 2 }]] } }),
    published({ watchId: id, frame: { retired: true } }),
    published({ watchId: id, frame: { end: true } }),
  ] })
  const h = await open(core)
  const seen: unknown[] = []
  const attached = (await (h as any).observeDoc(Notes, { advance: (change: unknown) => seen.push(change), end: (failure: unknown) => seen.push(["end", failure]) }, context))!
  assert.deepEqual(attached.value, { n: 1 })
  pump(h)
  await tick()
  assert.deepEqual(seen, [{ value: { n: 2 }, ops: [["r", { n: 2 }]], retired: false }, { value: { n: 2 }, ops: [], retired: true }, ["end", undefined]])
  assert.equal(closes(core).length, 1, "the observer was released from its source")
})

test("an observer that throws is reported, detached and ended with the error", async () => {
  const core = watching({ n: 1 }, { frames: id => [published({ watchId: id, frame: { value: { n: 2 }, ops: [["r", { n: 2 }]] } })] })
  const reports: unknown[] = []
  const h = await Harness.open(new MemoryStorage(), { models: models(), registry: createRegistry(), core: { newSession: () => core, sqlTable: [] }, onReport: (error: unknown) => reports.push(error) } as any, context)
  const boom = new Error("boom")
  const ended: unknown[] = []
  await (h as any).observeDoc(Notes, { advance: () => { throw boom }, end: (failure: unknown) => ended.push(failure) }, context)
  pump(h)
  await tick()
  assert.deepEqual(ended, [{ error: boom }])
  assert.deepEqual(reports, [boom])
  assert.equal(closes(core).length, 1)
})

test("a started document watch follows the published frames and stop releases the core's watch", async () => {
  const core = watching({ n: 1 }, { frames: id => [published({ watchId: id, frame: { value: { n: 2 }, ops: [["r", { n: 2 }]] } })] })
  const h = await open(core)
  const state = (await h.watchDoc(Notes, context))!
  state.start(async () => {})
  assert.deepEqual(state.value, { n: 1 })
  pump(h)
  await tick()
  assert.deepEqual(state.value, { n: 2 })
  await state.stop()
  await tick()
  assert.equal(closes(core).length, 1)
})

test("closing the Harness ends every watch as ended without asking the core to close them", async () => {
  const core = watching({ n: 1 })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  watch.start(async () => {})
  await h.close(context)
  assert.deepEqual(await watch.closed, { reason: "ended" })
  assert.equal(closes(core).length, 0)
})

test("the task graph watch and a conversation's view watch name their targets", async () => {
  const core = watching({ tasks: {} })
  const h = await open(core)
  await h.watchTaskGraph(context)
  const viewCore = watching({ messages: [] })
  const h2 = await open(viewCore)
  await (h2 as any).conversationState(7, context)
  await (h2 as any).watchConversation(7, context, { min: 10, max: 20, from: 5 })
  assert.deepEqual(apis(core).map(e => e.body.target), [{ kind: "graph" }], "the state and the watch of the graph share its mount")
  assert.deepEqual(viewOpens(viewCore).map(e => e.body.target), [{ kind: "view", conversationId: 7 }, { kind: "view", conversationId: 7, window: { min: 10, max: 20, from: 5 } }], "each observer of a conversation's view opens its own watch, with its own window")
})

test("a rejected watch.open surfaces the core's error and leaves no watch behind", async () => {
  const core = new ScriptedCore(ev => ev.kind === EVENT.api && ev.body.op !== "opened" ? { notices: [apiResult(ev.id!, { error: { name: "NotFound", message: "no such document" } })] } : undefined)
  const h = await open(core)
  await assert.rejects(h.watchDoc(Notes, context), /no such document/)
})

test("a view frame carries ops only: the host applies them to the value it holds, a set of `before` included", async () => {
  const first = { conversation: { id: 1 }, entries: [{ id: 3, kind: "a" }, { id: 4, kind: "b" }], docs: { "pi.live": {} } }
  const core = watching(first, { frames: id => [
    published({ watchId: id, frame: { ops: [["s", ["docs", "pi.live", "run"], { taskId: 9 }], ["p", ["entries"], 2, 0, [{ id: 5, kind: "c" }]]] } }),
    published({ watchId: id, frame: { ops: [["p", ["entries"], 3, 0, [{ id: 6, kind: "d" }]], ["p", ["entries"], 0, 2, []], ["s", ["before"], 4]] } }),
  ] })
  const h = await open(core)
  const seen: any[] = []
  const sink = await (h as any).watches.attach({ kind: "view", conversationId: 1, window: { min: 2, max: 3 } }, context, (value: unknown, release: () => void) => ({
    advance: ({ value: after, ops }: { value: unknown; ops: unknown[] }) => seen.push([after, ops.length]),
    end: release,
  }))
  assert.ok(sink)
  assert.deepEqual(apis(core)[0]!.body.target, { kind: "view", conversationId: 1, window: { min: 2, max: 3 } }, "the open request names the observer's window")
  pump(h)
  await tick()
  assert.deepEqual(seen, [
    [{ conversation: { id: 1 }, entries: [{ id: 3, kind: "a" }, { id: 4, kind: "b" }, { id: 5, kind: "c" }], docs: { "pi.live": { run: { taskId: 9 } } } }, 2],
    [{ conversation: { id: 1 }, entries: [{ id: 5, kind: "c" }, { id: 6, kind: "d" }], before: 4, docs: { "pi.live": { run: { taskId: 9 } } } }, 3],
  ])
})

// The core publishes a graph frame as {ops} alone (core/graph.go publishGraph): serializing the whole graph per commit made a task commit cost O(live tasks).
test("a graph frame carries ops only: the host applies them to the graph it holds", async () => {
  const node = (id: number) => ({ id, kind: "k", conversationId: 1, background: false, abortRequested: false, state: { status: "pending", phase: "start" }, conversations: [] as number[] })
  const first = { tasks: { "2": node(2), "4": node(4) } }
  const core = watching(first, { frames: id => [
    published({ watchId: id, frame: { ops: [["s", ["tasks", "6"], node(6)], ["d", ["tasks", "2"]]] } }),
    published({ watchId: id, frame: { ops: [["s", ["tasks", "4", "conversations"], [5]], ["s", ["tasks", "4", "blocked"], "missing_task"]] } }),
    published({ watchId: id, frame: { ops: [["d", ["tasks", "4", "blocked"]]] } }),
  ] })
  const h = await open(core)
  const seen: any[] = []
  const sink = await (h as any).watches.attach({ kind: "graph" }, context, (_value: unknown, release: () => void) => ({
    advance: ({ value: after, ops }: { value: unknown; ops: unknown[] }) => seen.push([after, ops.length]),
    end: release,
  }))
  assert.ok(sink)
  pump(h)
  await tick()
  assert.deepEqual(seen, [
    [{ tasks: { "4": node(4), "6": node(6) } }, 2],
    [{ tasks: { "4": { ...node(4), conversations: [5], blocked: "missing_task" }, "6": node(6) } }, 2],
    [{ tasks: { "4": { ...node(4), conversations: [5] }, "6": node(6) } }, 1],
  ])
})

const viewOpens = (core: ScriptedCore) => apis(core).filter(e => e.body.op === "watch.open" && e.body.target.kind === "view")

// view.ts: each observer has its own window over the entries, which the core keeps per watch: observers of one conversation's view do
// not share a mount, and each core watch is closed with its observer.
test("observers of one conversation's view each open and close a watch of their own", async () => {
  const core = watching({ conversation: { id: 1 }, entries: [], docs: {} })
  const h = await open(core)
  const [first, second] = await Promise.all([(h as any).conversationState(1, context), (h as any).conversationState(1, context)]) as any[]
  assert.equal(viewOpens(core).length, 2)
  first.dispose()
  await tick()
  assert.deepEqual(closes(core).map(e => e.body.watchId), [viewOpens(core)[0]!.body.watchId], "the first observer's watch closes with it")
  second.dispose()
  await tick()
  assert.deepEqual(closes(core).map(e => e.body.watchId), viewOpens(core).map(e => e.body.watchId))
})

/** A core that answers watch.open only when the test pumps it (a registry event), as one does when the Session line is held. */
function lateCore() {
  const late: Notice[] = []
  const core = new ScriptedCore((ev: Seen) => {
    if (ev.kind === EVENT.api && ev.body.op === "watch.open") { late.push(apiResult(ev.id!, { ok: { value: { n: 1 } } })); return undefined }
    if (ev.kind === EVENT.api && ev.body.op === "watch.close") return { notices: [apiResult(ev.id!, { ok: null })] }
    if (ev.kind === EVENT.registry) return { notices: late.splice(0) }
    return undefined
  })
  return core
}

test("an acquisition cancelled while the core has not answered rejects, and the watch the core opens for it is closed", async () => {
  const core = lateCore()
  const h = await open(core)
  const controller = new AbortController()
  const acquiring = h.watchDoc(Notes, { ...context, abortSignal: controller.signal })
  controller.abort(new Error("cancelled"))
  await assert.rejects(acquiring, /cancelled/)
  pump(h)
  await tick()
  assert.equal(closes(core).length, 1, "the late answer's watch is released")
})

test("an acquisition that the handle's close overtakes rejects with Harness is closed", async () => {
  const core = lateCore()
  const h = await open(core)
  const rejected = assert.rejects(h.watchDoc(Notes, context), /Harness is closed/)
  ;(h as any).watches.closeSession()
  pump(h)
  await rejected
})

test("an acquisition whose context is already cancelled rejects without asking the core", async () => {
  const core = watching({ n: 1 })
  const h = await open(core)
  const controller = new AbortController()
  controller.abort(new Error("already cancelled"))
  await assert.rejects(h.watchDoc(Notes, { ...context, abortSignal: controller.signal }), /already cancelled/)
  assert.equal(apis(core).filter(e => e.body.op === "watch.open").length, 0)
})

test("observers of the task graph share one mount and one value object, closed with the last observer", async () => {
  const core = watching({ tasks: {} })
  const h = await open(core)
  const [first, second] = await Promise.all([h.watchTaskGraph(context), h.watchTaskGraph(context)]) as any[]
  assert.equal(apis(core).filter(e => e.body.op === "watch.open").length, 1)
  assert.ok(first.value === second.value)
  await first.stop(); await tick()
  assert.equal(closes(core).length, 0)
  await second.stop(); await tick()
  assert.equal(closes(core).length, 1)
})
