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
function watching(first: unknown, options: { sameStep?: { value: unknown; ops: unknown[] }[]; frames?: (watchId: number) => Notice[]; completing?: number[]; attached?: number[] } = {}) {
  let watchId = 0
  const core = new ScriptedCore((ev: Seen) => {
    if (ev.kind === EVENT.api && ev.body.op === "watch.open") {
      watchId = ev.body.watchId
      const frames = (options.sameStep ?? []).map(f => published({ watchId, frame: f }))
      return { notices: [apiResult(ev.id!, { ok: { value: first, ...(ev.body.target.completing ? { completing: options.completing ?? [] } : {}) } }), ...frames] }
    }
    // An observer joining a mounted view is a line job of its own: the answer carries the completing generations it read.
    if (ev.kind === EVENT.api && ev.body.op === "view.attach") return { notices: [apiResult(ev.id!, { ok: ev.body.completing ? { completing: options.attached ?? [] } : {} })] }
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
  watch.start(async (value, ops) => { if (++running > 1) overlap = true; await tick(); seen.push([value, ops]); running-- })
  pump(h)
  await tick(); await tick(); await tick()
  assert.deepEqual(seen, [[{ n: 2 }, [["r", { n: 2 }]]], [{ n: 3 }, [["r", { n: 3 }]]]])
  assert.equal(overlap, false, "the listener never runs twice at once")
  assert.deepEqual(watch.value, { n: 3 }, "value is the latest delivered revision")
})

test("an absent document gives undefined and leaves nothing to close", async () => {
  const core = watching(null)
  const h = await open(core)
  assert.equal(await h.watchDoc(Notes, context), undefined)
  assert.equal(await h.documentState(Notes, context), undefined)
  await h.close(context)
  assert.equal(closes(core).length, 0)
})

test("a frame published in the answer's own step is not lost", async () => {
  const core = watching({ n: 1 }, { sameStep: [{ value: { n: 2 }, ops: [["r", { n: 2 }]] }] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  const seen: unknown[] = []
  watch.start(async value => { seen.push(value) })
  await tick()
  assert.deepEqual(seen, [{ n: 2 }])
})

test("stop() closes the core's watch once and ignores later frames", async () => {
  const core = watching({ n: 1 }, { frames: id => [published({ watchId: id, frame: { value: { n: 2 }, ops: [] } })] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  const seen: unknown[] = []
  watch.start(async value => { seen.push(value) })
  assert.deepEqual(await watch.stop(), { reason: "stopped" })
  assert.deepEqual(await watch.stop(), { reason: "stopped" }, "stop is idempotent")
  pump(h)
  await tick()
  assert.deepEqual(seen, [])
  assert.equal(closes(core).length, 1)
  assert.equal(closes(core)[0]!.body.watchId, apis(core)[0]!.body.watchId)
})

test("a null frame retires the watch: the listener sees it, the watch ends retired and the core's watch is closed", async () => {
  const core = watching({ n: 1 }, { frames: id => [published({ watchId: id, frame: { value: null, ops: [["r", null]] } })] })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  const seen: unknown[] = []
  watch.start(async value => { seen.push(value) })
  pump(h)
  assert.deepEqual(await watch.closed, { reason: "retired" })
  assert.deepEqual(seen, [null])
  assert.equal(closes(core).length, 1)
})

test("documentState follows the published frames and dispose releases the core's watch", async () => {
  const core = watching({ n: 1 }, { frames: id => [published({ watchId: id, frame: { value: { n: 2 }, ops: [["r", { n: 2 }]] } })] })
  const h = await open(core)
  const state = (await h.documentState(Notes, context))!
  assert.deepEqual(state.value, { n: 1 })
  pump(h)
  await tick()
  assert.deepEqual(state.value, { n: 2 })
  state.dispose()
  await tick()
  assert.equal(closes(core).length, 1)
})

test("closing the Harness ends every watch as session_closed without asking the core to close them", async () => {
  const core = watching({ n: 1 })
  const h = await open(core)
  const watch = (await h.watchDoc(Notes, context))!
  watch.start(async () => {})
  await h.close(context)
  assert.deepEqual(await watch.closed, { reason: "session_closed" })
  assert.equal(closes(core).length, 0)
})

test("taskGraph and a conversation's view watch name their targets", async () => {
  const core = watching({ tasks: {} })
  const h = await open(core)
  await h.taskGraph(context)
  await h.watchTaskGraph(context)
  const viewCore = watching({ messages: [] })
  const h2 = await open(viewCore)
  await (h2 as any).conversationState(7, context)
  await (h2 as any).watchConversation(7, context)
  assert.deepEqual(apis(core).map(e => e.body.target), [{ kind: "graph" }], "the state and the watch of the graph share its mount")
  assert.deepEqual(viewOpens(viewCore).map(e => e.body.target), [{ kind: "view", conversationId: 7, changes: true }], "the state and the watch of one conversation share its mount, opened with the commit's records for an event stream")
})

test("a rejected watch.open surfaces the core's error and leaves no watch behind", async () => {
  const core = new ScriptedCore(ev => ev.kind === EVENT.api && ev.body.op !== "opened" ? { notices: [apiResult(ev.id!, { error: { name: "NotFound", message: "no such document" } })] } : undefined)
  const h = await open(core)
  await assert.rejects(h.watchDoc(Notes, context), /no such document/)
})

test("a view frame carries ops only: the host applies them to the value it holds and hands the commit's records to an observer", async () => {
  const first = { conversation: { id: 1 }, entries: [{ id: 3, kind: "a" }], docs: { "pi.live": {} } }
  const changes = { entries: [{ id: 4, kind: "b" }], tasks: [], submissions: [] }
  const core = watching(first, { frames: id => [
    published({ watchId: id, frame: { ops: [["s", ["docs", "pi.live", "run"], { taskId: 9 }], ["p", ["entries"], 1, 0, [{ id: 4, kind: "b" }]]], changes } }),
    published({ watchId: id, frame: { ops: [], changes: { entries: [], tasks: [{ id: 7 }], submissions: [] } } }),
  ] })
  const h = await open(core)
  const seen: any[] = []
  const sink = await (h as any).watches.attach({ kind: "view", conversationId: 1, changes: true }, context, (value: unknown, release: () => void) => ({
    advance: (after: unknown, ops: unknown[]) => seen.push(["advance", after, ops]),
    publication: (before: unknown, after: unknown, ops: unknown[], c: unknown) => seen.push(["publication", before, after, ops.length, c]),
    closeSession: release,
  }))
  assert.ok(sink)
  assert.deepEqual(apis(core)[0]!.body.target, { kind: "view", conversationId: 1, changes: true }, "the open request asks for the commit's records")
  pump(h)
  await tick()
  const after = { conversation: { id: 1 }, entries: [{ id: 3, kind: "a" }, { id: 4, kind: "b" }], docs: { "pi.live": { run: { taskId: 9 } } } }
  assert.deepEqual(seen[0], ["advance", after, [["s", ["docs", "pi.live", "run"], { taskId: 9 }], ["p", ["entries"], 1, 0, [{ id: 4, kind: "b" }]]]])
  assert.deepEqual(seen[1], ["publication", first, after, 2, changes])
  assert.equal(seen.length, 3, "a frame without ops reaches publication only")
  assert.deepEqual(seen[2], ["publication", after, after, 0, { entries: [], tasks: [{ id: 7 }], submissions: [] }])
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
    advance: (after: unknown, ops: unknown[]) => seen.push([after, ops.length]),
    closeSession: release,
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

// view.ts advance: one mount per conversation hands every observer the new revision, then every observer the publication. A view state
// and an event stream of one conversation were two mounts (two core watches) and heard a commit in core notice order instead (the
// differential rows F-*, seed 2).
test("a view state and event observers of one conversation share one core watch; a frame advances every observer before any publication", async () => {
  const first = { conversation: { id: 1 }, entries: [], docs: {} }
  const core = watching(first, { frames: id => [published({ watchId: id, frame: { ops: [["p", ["entries"], 0, 0, [{ id: 4, kind: "b" }]]], changes: { entries: [{ id: 4, kind: "b" }], tasks: [], submissions: [] } } })] })
  const h = await open(core)
  const order: string[] = []
  const observer = (name: string) => (_value: unknown, release: () => void) => ({ advance: () => order.push(`${name} advance`), publication: () => order.push(`${name} publication`), closeSession: release })
  await (h as any).attachView(1, context, observer("a"))
  const state = await (h as any).conversationState(1, context)
  await (h as any).attachView(1, context, observer("b"))
  assert.ok(state)
  assert.equal(apis(core).filter(e => e.body.op === "watch.open").length, 1, "one core watch serves the state and both observers")
  pump(h)
  await tick()
  assert.deepEqual(order, ["a advance", "b advance", "a publication", "b publication"])
})

const viewOpens = (core: ScriptedCore) => apis(core).filter(e => e.body.op === "watch.open" && e.body.target.kind === "view")

test("observers of one conversation's view share one mount: one watch.open, one value object, closed with the last observer", async () => {
  const core = watching({ conversation: { id: 1 }, entries: [], docs: {} })
  const h = await open(core)
  const [first, second] = await Promise.all([(h as any).conversationState(1, context), (h as any).conversationState(1, context)]) as any[]
  assert.equal(viewOpens(core).length, 1)
  assert.ok(first.value === second.value, "both observers hold the same revision object")
  first.dispose()
  await tick()
  assert.equal(closes(core).length, 0, "an observer is left")
  second.dispose()
  await tick()
  assert.equal(closes(core).length, 1)
  assert.equal(closes(core)[0]!.body.watchId, viewOpens(core)[0]!.body.watchId)
  const again = await (h as any).conversationState(1, context)
  assert.equal(viewOpens(core).length, 2, "a new mount after the last observer detached")
  again!.dispose()
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
  const [first, second] = await Promise.all([h.taskGraph(context), h.taskGraph(context)]) as any[]
  assert.equal(apis(core).filter(e => e.body.op === "watch.open").length, 1)
  assert.ok(first.value === second.value)
  first.dispose(); await tick()
  assert.equal(closes(core).length, 0)
  second.dispose(); await tick()
  assert.equal(closes(core).length, 1)
})

// view.ts attach / events.ts watchEvents: the generations in `completing` are read on the line job that attaches the observer, with the
// mount's build for the first observer (watch.open answers them) and in the observer's own job for one that joins (view.attach).
test("watchEvents' completing generations come from the core's line job: watch.open for the mount's first observer, view.attach for a joined one", async () => {
  const core = watching({ conversation: { id: 1 }, entries: [], docs: {} }, { completing: [5], attached: [6] })
  const h = await open(core)
  const seen: (readonly number[] | undefined)[] = []
  const make = (_value: unknown, release: () => void, completing?: readonly number[]) => { seen.push(completing); return { advance: () => {}, closeSession: release } }
  await (h as any).attachView(1, context, make)
  await (h as any).attachView(1, context, make)
  assert.deepEqual(seen, [[5], [6]])
  assert.deepEqual(viewOpens(core).map(e => e.body.target.completing), [true], "the mount is opened once, asking for the generations")
  assert.deepEqual(apis(core).filter(e => e.body.op === "view.attach").map(e => e.body), [{ op: "view.attach", conversationId: 1, completing: true }])
})

test("a state joining a mounted view attaches through the core without asking for generations", async () => {
  const core = watching({ conversation: { id: 1 }, entries: [], docs: {} })
  const h = await open(core)
  const first = await (h as any).conversationState(1, context)
  const second = await (h as any).conversationState(1, context)
  assert.equal(viewOpens(core).length, 1)
  assert.deepEqual(apis(core).filter(e => e.body.op === "view.attach").map(e => e.body.completing), [false])
  first.dispose()
  second.dispose()
})

// The joined observer's job is queued when the observer asks, behind the job that builds the mount (a Session line keeps its order), not
// when the mount's answer reaches the host (F-gate-3: the event stream attached after the first commit).
test("an observer joining a mount still being built asks the core to attach it before the mount's answer", async () => {
  const late: Notice[] = []
  const order: string[] = []
  const core = new ScriptedCore((ev: Seen) => {
    if (ev.kind === EVENT.api && ev.body.op === "watch.open") { order.push("open"); late.push(apiResult(ev.id!, { ok: { value: { n: 1 } } })); return undefined }
    if (ev.kind === EVENT.api && ev.body.op === "view.attach") { order.push("attach"); late.push(apiResult(ev.id!, { ok: {} })); return undefined }
    if (ev.kind === EVENT.registry) { order.push("answers"); return { notices: late.splice(0) } }
    return undefined
  })
  const h = await open(core)
  const states = Promise.all([(h as any).conversationState(1, context), (h as any).conversationState(1, context)])
  await tick()
  assert.deepEqual(order, ["open", "attach"], "both jobs are asked before either answers")
  pump(h)
  const [first, second] = await states as any[]
  first.dispose()
  second.dispose()
})

// A view's creator that gives up leaves the frames of its answer's step to the observer that joined it. That observer waits for its own
// line job (view.attach), and commits between the two jobs publish meanwhile: it must start from the committed value at its job, with
// the answer's step applied first and once (view.ts attach builds or finds the mount on the line).
async function joinedAfterTheCreatorGaveUp(abortAfterAnswer: boolean) {
  const held: Notice[] = []
  let watchId = 0
  const core = new ScriptedCore((ev: Seen) => {
    if (ev.kind === EVENT.api && ev.body.op === "watch.open") {
      watchId = ev.body.watchId
      held.push(apiResult(ev.id!, { ok: { value: { n: 1 } } }), published({ watchId, frame: { ops: [["r", { n: 1.5 }]] } }))
      return undefined
    }
    if (ev.kind === EVENT.api && ev.body.op === "view.attach") { held.push(apiResult(ev.id!, { ok: {} })); return undefined }
    if (ev.kind === EVENT.api && ev.body.op === "watch.close") return { notices: [apiResult(ev.id!, { ok: null })] }
    if (ev.kind === EVENT.registry) return { notices: held.splice(0, ev.body.n ?? held.length) }
    return undefined
  })
  const h = await open(core)
  const send = (n?: number) => (h as any).host.sendJSON(EVENT.registry, n === undefined ? {} : { n })
  const controller = new AbortController()
  const creator = (h as any).conversationState(1, { ...context, abortSignal: controller.signal })
  creator.catch(() => {})
  const joiner = (h as any).watchConversation(1, context)
  await tick()
  if (!abortAfterAnswer) controller.abort(new Error("cancelled"))
  send(2) // the build job's answer and a frame of its step
  if (abortAfterAnswer) controller.abort(new Error("cancelled"))
  await assert.rejects(creator, /cancelled/)
  await tick()
  held.unshift(published({ watchId, frame: { ops: [["r", { n: 2 }]] } }))
  send(1); await tick() // a commit between the two jobs
  send(); await tick() // the joiner's job answers
  const watch = await joiner
  const seen: unknown[] = [watch.value]
  watch.start(async (value: unknown) => { seen.push(value) })
  await tick(); await tick()
  return seen
}

test("an observer joining a view whose creator gave up before the answer starts from the value at its own job", async () => {
  assert.deepEqual(await joinedAfterTheCreatorGaveUp(false), [{ n: 2 }])
})

test("an observer joining a view whose creator gave up as the answer arrived starts from the value at its own job", async () => {
  assert.deepEqual(await joinedAfterTheCreatorGaveUp(true), [{ n: 2 }])
})

test("a view's creator takes the frames of its answer's step while an observer joins it; the joiner starts from the value at its own job", async () => {
  const held: Notice[] = []
  let watchId = 0
  const core = new ScriptedCore((ev: Seen) => {
    if (ev.kind === EVENT.api && ev.body.op === "watch.open") {
      watchId = ev.body.watchId
      held.push(apiResult(ev.id!, { ok: { value: { n: 1 } } }), published({ watchId, frame: { ops: [["r", { n: 1.5 }]] } }))
      return undefined
    }
    if (ev.kind === EVENT.api && ev.body.op === "view.attach") { held.push(apiResult(ev.id!, { ok: {} })); return undefined }
    if (ev.kind === EVENT.api && ev.body.op === "watch.close") return { notices: [apiResult(ev.id!, { ok: null })] }
    if (ev.kind === EVENT.registry) return { notices: held.splice(0, ev.body.n ?? held.length) }
    return undefined
  })
  const h = await open(core)
  const send = (n?: number) => (h as any).host.sendJSON(EVENT.registry, n === undefined ? {} : { n })
  // A cancellable creator resumes a tick after the shared answer, behind the joiner's continuation.
  const creator = (h as any).watchConversation(1, { ...context, abortSignal: new AbortController().signal })
  const joiner = (h as any).watchConversation(1, context)
  await tick()
  send(2); await tick()
  const first = await creator
  const seenFirst: unknown[] = [first.value]
  first.start(async (value: unknown) => { seenFirst.push(value) })
  held.unshift(published({ watchId, frame: { ops: [["r", { n: 2 }]] } }))
  send(1); await tick()
  send(); await tick()
  const second = await joiner
  const seenSecond: unknown[] = [second.value]
  second.start(async (value: unknown) => { seenSecond.push(value) })
  await tick(); await tick()
  assert.deepEqual(seenFirst, [{ n: 1 }, { n: 1.5 }, { n: 2 }])
  assert.deepEqual(seenSecond, [{ n: 2 }])
})
