// Host loop invariants (ABI section 3) against the probe core on node:sqlite.
import assert from "node:assert/strict"
import { join } from "node:path"
import { test } from "node:test"
import { EFFECT, EVENT, NOTICE } from "../src/abi.ts"
import { FatalError, HostSession, isPublishedSettlement, RejectedError, type EffectHandler, type TimerDriver } from "../src/host.ts"
import { nodeStore } from "../src/store-node.ts"
import { WasmCore } from "../src/wasm.ts"
import { probeModule, tempDir } from "./helpers.ts"

const OPEN = { settings: {}, agent: { provider: "probe", modelId: "probe-1" }, models: [], registry: [], sidecar: "index", delta: false }

function rig(options: { path?: string } = {}) {
  const path = options.path ?? join(tempDir("host"), "s.sqlite")
  const store = nodeStore(path)
  const core = new WasmCore(probeModule())
  const log: string[] = []
  const released = new Map<number, () => void>()
  const seen: { id: number; kind: number; signal: AbortSignal }[] = []
  const timers: string[] = []
  const logRows = () => (store.db.prepare("SELECT n, note FROM probe_log ORDER BY n").all() as { n: number; note: string }[]).map(r => `${r.n}:${r.note}`)
  const handler = (name: string): EffectHandler => (effect, { signal }) => {
    log.push(`${name}#${effect.id} log=${logRows().length}`)
    seen.push({ id: effect.id, kind: effect.kind, signal })
    // The effect lives until the host cancels it or the test releases it.
    return new Promise<void>(resolve => { signal.addEventListener("abort", () => resolve()); released.set(effect.id, resolve) })
  }
  const tm: TimerDriver = {
    set: (id, at, durable) => { timers.push(`set ${id} ${at} ${durable} log=${logRows().length}`) },
    clear: id => { timers.push(`clear ${id}`) },
    liveness: at => { timers.push(`liveness ${at}`) },
  }
  const events: number[] = []
  const notices: number[] = []
  const host = new HostSession({
    core: core.newSession(), sqlTable: core.sqlTable, store, timers: tm, clock: () => 5000,
    handlers: { [EFFECT.model_context]: handler("model"), [EFFECT.tool]: handler("tool") },
    trace: { event: kind => events.push(kind), commit: c => log.push(`commit ${c.seq}`) },
    onNotice: n => notices.push(n.kind),
  })
  return { host, store, core, log, seen, released, timers, events, notices, logRows, path }
}

test("open on an empty store: the read round is answered before any other event, then tables exist", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  assert.deepEqual(r.events, [EVENT.open, EVENT.rows, EVENT.rows].slice(0, r.events.length))
  assert.equal(r.events[0], EVENT.open)
  assert.equal(r.events[1], EVENT.rows)
  assert.equal(r.store.db.prepare("SELECT next_seq FROM durable_metadata").get()!.next_seq, 1)
  assert.deepEqual(r.notices, [NOTICE.report])
})

test("commits apply in order, each in one transaction, before any effect or timer starts", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  const commitsBefore = r.store.counters.commits
  r.log.length = 0
  r.host.sendJSON(EVENT.submit, { type: "input", content: "hello", requestId: "r1" })
  assert.equal(r.store.counters.commits - commitsBefore, 2)
  // Both commits are logged before the model effect starts, and the effect saw both rows.
  assert.deepEqual(r.log, ["commit 1", "commit 2", "model#2 log=2"])
  assert.deepEqual(r.timers, ["set 1 5100 true log=2", "liveness 6000"])
  assert.deepEqual(r.logRows(), ["1:submit:r1", "2:placed:r1"])
})

test("a model completion and a tool completion settle the submission; waitForNotice resolves after the commit", async () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  const settled = r.host.waitForNotice(isPublishedSettlement("r1"))
  r.host.sendJSON(EVENT.submit, { type: "input", content: "hello", requestId: "r1" })
  r.host.modelEvents(2, [{ type: "start" }, { type: "done", message: {} }])
  assert.deepEqual(r.seen.map(s => s.kind), [EFFECT.model_context, EFFECT.tool])
  r.host.toolDone(r.seen[1]!.id, 0, { content: [] })
  await settled
  assert.equal(r.logRows().length, 4)
  assert.ok(r.timers.includes("clear 1") && r.timers.includes("liveness -1"))
})

test("a rejected step changes nothing and leaves the host alive", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  const step = r.host.sendJSON(EVENT.submit, { type: "input", content: "reject", requestId: "x" })!
  assert.equal(step.status, 1)
  assert.equal(new RejectedError(step.error?.name, step.error?.message, step.error!.raw).message, "rejected by the probe")
  assert.deepEqual(r.logRows(), [])
  assert.equal(r.host.discarded, undefined)
  assert.equal(r.host.counters.rejected, 1)
})

test("the single-writer guard: a metadata update that changes no row is fatal", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  assert.throws(() => r.host.sendJSON(EVENT.submit, { type: "input", content: "stale", requestId: "s" }), (e: unknown) => e instanceof FatalError && /single-writer guard/.test(e.message))
  assert.ok(r.host.discarded)
  // The failing commit rolled back whole, and no effect of the step started.
  assert.deepEqual(r.logRows(), [])
  assert.deepEqual(r.seen, [])
  assert.throws(() => r.host.send(EVENT.timer), FatalError)
})

test("a fatal step discards the handle: effects are cancelled and late completions are dropped", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  r.host.sendJSON(EVENT.submit, { type: "input", content: "hello", requestId: "r1" })
  const signal = r.seen[0]!.signal
  assert.equal(signal.aborted, false)
  assert.throws(() => r.host.sendJSON(EVENT.submit, { type: "input", content: "fatal", requestId: "r2" }), FatalError)
  assert.equal(signal.aborted, true)
  assert.doesNotThrow(() => r.host.modelEvents(r.seen[0]!.id, [{ type: "done" }]))
  assert.equal(r.logRows().length, 2, "the late completion changed nothing")
})

test("an abort event turns into cancel effects that abort the running effects", async () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  r.host.sendJSON(EVENT.submit, { type: "input", content: "multi", requestId: "r1" })
  assert.equal(r.seen.length, 3)
  r.host.sendJSON(EVENT.abort, { conversationId: 1 })
  assert.ok(r.seen.every(s => s.signal.aborted))
  await Promise.resolve()
  assert.equal(r.host.inflight, 0)
})

test("reopening a store resumes from durable_metadata, and a pi-style reader sees the rows", () => {
  const a = rig()
  a.host.sendJSON(EVENT.open, OPEN)
  a.host.sendJSON(EVENT.submit, { type: "input", content: "hello", requestId: "r1" })
  a.store.close()
  const b = rig({ path: a.path })
  b.host.sendJSON(EVENT.open, OPEN)
  assert.equal(b.notices.length, 1)
  b.host.sendJSON(EVENT.submit, { type: "input", content: "again", requestId: "r2" })
  assert.deepEqual(b.logRows(), ["1:submit:r1", "2:placed:r1", "3:submit:r2", "4:placed:r2"])
})

test("effect IDs are unique for the life of a handle, across steps", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  r.host.sendJSON(EVENT.submit, { type: "input", content: "hello", requestId: "r1" })
  r.host.sendJSON(EVENT.submit, { type: "input", content: "hello", requestId: "r2" })
  const ids = r.seen.map(s => s.id)
  assert.equal(new Set(ids).size, ids.length)
})

test("an event after close is rejected by the core, not delivered to a freed handle", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  r.host.send(EVENT.close)
  const step = r.host.sendJSON(EVENT.submit, { type: "input", content: "late", requestId: "r2" })!
  assert.equal(step.status, 1)
})

test("a trap in the core discards the handle and rejects the waiters", async () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  const waiting = r.host.waitForNotice(() => false)
  const raw = r.host as unknown as { core: { step: () => never } }
  raw.core.step = () => { throw new Error("unreachable") }
  assert.throws(() => r.host.send(EVENT.timer), FatalError)
  await assert.rejects(waiting, FatalError)
})
