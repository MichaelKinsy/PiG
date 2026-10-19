// Hop chains and the step order list (ABI section 5.3): the host runs real microtask hops and sends `hopped`; it processes a step's
// notices and effects in the core's order. Pi's ordering depends on microtask positions, so the tests count real microtasks.
import assert from "node:assert/strict"
import { test } from "node:test"
import { EVENT, EFFECT } from "../src/abi.ts"
import { FatalError } from "../src/host.ts"
import { decodeStep } from "../src/wire.ts"
import { apiResult, rig, type Seen } from "./scripted.ts"

const u32 = (n: number) => { const b = new Uint8Array(4); new DataView(b.buffer).setUint32(0, n, true); return b }
const hops = (id: number, n: number) => ({ id, kind: EFFECT.hops, payload: Uint8Array.of(...u32(n), 0, 0, 0, 0) })
const noRuntime = { registry: undefined as any, models: undefined as any }

// A counter that advances once per microtask round, started before the chain under test, so a chain hop of round k runs after the
// counter's tick k: a `hopped` sent by the nth hop sees counter n.
function spin() {
  const state = { ticks: 0 }
  const step = () => { state.ticks++; if (state.ticks < 1000) queueMicrotask(step) }
  queueMicrotask(step)
  return state
}

test("hops: a chain of n hops sends hopped on the nth microtask, not before and not after", async () => {
  for (const n of [1, 2, 5, 11]) {
    const seenAt: number[] = []
    const clock = spin()
    const { core, host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [hops(7, n)] } : (seenAt.push(clock.ticks), undefined)), noRuntime)
    host.send(EVENT.resume)
    for (let i = 0; i < n + 3; i++) await Promise.resolve()
    assert.deepEqual(core.ofKind(EVENT.hopped).map(e => new DataView(e.payload.buffer, e.payload.byteOffset, 4).getUint32(0, true)), [7], `chain of ${n} reports its own effect ID once`)
    assert.deepEqual(seenAt, [n], `hopped is sent by hop ${n}`)
  }
})

test("hops: concurrent chains finish in the order their hop counts and start order give, as Pi's awaits would", async () => {
  const done: number[] = []
  const { host } = rig(ev => { if (ev.kind === EVENT.resume) return { effects: [hops(1, 3), hops(2, 2), hops(3, 3)] }; done.push(new DataView(ev.payload.buffer, ev.payload.byteOffset, 4).getUint32(0, true)); return undefined }, noRuntime)
  host.send(EVENT.resume)
  for (let i = 0; i < 6; i++) await Promise.resolve()
  assert.deepEqual(done, [2, 1, 3], "the 2-hop chain first, then the equal chains in start order")
})

test("hops: a cancel effect drops an open chain and the handle's discard stops the rest", async () => {
  const done: number[] = []
  const { host } = rig(ev => {
    if (ev.kind === EVENT.resume) return { effects: [hops(1, 4), hops(2, 4)] }
    if (ev.kind === EVENT.timer) return { effects: [{ id: 3, kind: EFFECT.cancel, payload: u32(1) }] }
    done.push(new DataView(ev.payload.buffer, ev.payload.byteOffset, 4).getUint32(0, true)); return undefined
  }, noRuntime)
  host.send(EVENT.resume)
  await Promise.resolve()
  host.send(EVENT.timer, u32(0))
  assert.equal(host.inflight, 2, "a cancelled chain stays counted until its next hop")
  for (let i = 0; i < 8; i++) await Promise.resolve()
  assert.deepEqual(done, [2], "the cancelled chain never reports")
  assert.equal(host.inflight, 0)
})

test("hops: a discarded handle stops every open chain", async () => {
  const done: number[] = []
  const { host } = rig(ev => {
    if (ev.kind === EVENT.resume) return { effects: [hops(1, 4), hops(2, 4)] }
    done.push(ev.kind); return undefined
  }, noRuntime)
  host.send(EVENT.resume)
  await Promise.resolve()
  host.discard(new FatalError("test"))
  for (let i = 0; i < 8; i++) await Promise.resolve()
  assert.deepEqual(done, [])
  assert.equal(host.inflight, 0)
})

test("order: the host applies notices and effects in the step's order, not all notices first", () => {
  const log: string[] = []
  const timers = { set: () => {}, clear: () => {}, liveness: (at: number) => { log.push(`effect:${at}`) } } as any
  const live = (id: number, at: number) => { const b = new Uint8Array(8); new DataView(b.buffer).setFloat64(0, at, true); return { id, kind: EFFECT.liveness, payload: b } }
  const { host } = rig(ev => (ev.kind === EVENT.resume
    ? { notices: [apiResult(1, { ok: 1 }), apiResult(2, { ok: 2 })], effects: [live(1, 10), live(2, 20)], order: [[1, 0], [0, 0], [1, 1], [0, 1]] }
    : undefined), noRuntime, { timers, onNotice: n => { log.push(`notice:${new DataView(n.payload.buffer, n.payload.byteOffset, 4).getUint32(0, true)}`) } })
  host.send(EVENT.resume)
  assert.deepEqual(log, ["effect:10", "notice:1", "effect:20", "notice:2"])
})

test("order: without an order list the host keeps all notices, then all effects", () => {
  const log: string[] = []
  const timers = { set: () => {}, clear: () => {}, liveness: (at: number) => { log.push(`effect:${at}`) } } as any
  const live = (id: number, at: number) => { const b = new Uint8Array(8); new DataView(b.buffer).setFloat64(0, at, true); return { id, kind: EFFECT.liveness, payload: b } }
  const { host } = rig(ev => (ev.kind === EVENT.resume ? { notices: [apiResult(1, { ok: 1 })], effects: [live(1, 10)] } : undefined), noRuntime, { timers, onNotice: n => { log.push(`notice:${new DataView(n.payload.buffer, n.payload.byteOffset, 4).getUint32(0, true)}`) } })
  host.send(EVENT.resume)
  assert.deepEqual(log, ["notice:1", "effect:10"])
})

test("wire: decodeStep reads the order list and rejects an entry that names nothing or a list that is not complete", () => {
  const bytes = (order: number[][]) => {
    const parts: number[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0]                         // size, status 0, commits 0, reads 0, notices 1, effects 1
    parts.push(1, 0, 0, 0, 0, 0, 0, 0, 0)                                                     // notice kind 1, origin 0, empty payload
    parts.push(1, 0, 0, 0, 10, 0, 0, 0, 0, 0, 0, 0, 0)                                        // effect id 1, kind 10, origin 0, empty payload
    parts.push(order.length & 255, order.length >> 8)
    for (const [region, index] of order) parts.push(region!, index! & 255, index! >> 8)
    const out = Uint8Array.from(parts)
    new DataView(out.buffer).setUint32(0, out.length - 4, true)
    return out
  }
  const decode = (b: Uint8Array) => decodeStep(b, 0, b.length)
  assert.deepEqual(decode(bytes([[1, 0], [0, 0]])).order, [[1, 0], [0, 0]])
  assert.equal(decode(bytes([])).order, undefined, "n = 0 is the default order")
  assert.throws(() => decode(bytes([[1, 0], [0, 3]])), /missing notice 3/)
  assert.throws(() => decode(bytes([[1, 0]])), /lists 1 of 2/)
})

// ABI section 5.3 rule 3: phase_done is sent in the microtask where Pi's `await handler()` resumes. The baseline is a bare
// `await handler()` timed against the same counter, so no hop constant is assumed.
test("phase_done: sent in the microtask where an await of the handler resumes, with no extra host hop", async () => {
  const { customTaskRunner } = await import("../src/facade/taskruntime.ts")
  const { BACKGROUND_CONTEXT } = await import("@earendil-works/chord/context")
  for (const abort of [false, true]) {
    const clock = spin()
    const handler = async () => { await Promise.resolve() }
    let baseline = -1
    void (async () => { await handler(); baseline = clock.ticks })()
    const task = { definition: { name: "k", version: 1, phases: { a: handler }, abort: handler } }
    const run = customTaskRunner({ registry: { task: () => task, at: () => task }, hostApi: { client: {} as never }, models: {}, settings: () => ({}), now: () => 0, report: () => {}, snapshot: () => ({}) } as never)
    let seenAt = -1
    const { host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [{ id: 1, kind: EFFECT.phase, payload: new TextEncoder().encode(JSON.stringify({ taskId: 7, kind: "k", phase: "a", record: { id: 7, conversationId: 1 }, invocation: 7, ...(abort ? { abort: true } : {}) })) }] } : (ev.kind === EVENT.phase_done ? (seenAt = clock.ticks, undefined) : undefined)),
      { registry: undefined as any, models: undefined as any, phase: run } as any)
    void BACKGROUND_CONTEXT
    host.send(EVENT.resume)
    for (let i = 0; i < 20; i++) await Promise.resolve()
    assert.notEqual(seenAt, -1, "phase_done arrived")
    assert.equal(seenAt, baseline, `the ${abort ? "abort handler" : "phase"} report lands in the tick of a bare await of the handler`)
  }
})

// Every completion event of the host's effect handlers (hook, env, section, deferred, callback) is sent in the continuation of the
// await of the user code, as Pi's `await handler()` resumes; the audit pins the hook and env paths against a bare await.
test("hook_done: the registry hook path and a host hook report in the tick of a bare await of the handler", async () => {
  const handler = async () => { await Promise.resolve(); return null }
  for (const route of ["registry", "host"]) {
    const clock = spin()
    let baseline = -1
    void (async () => { await handler(); baseline = clock.ticks })()
    let seenAt = -1
    const runtime: any = route === "host"
      ? { registry: undefined, models: undefined, hostHooks: { probe: handler } }
      : { registry: { at: () => handler }, models: undefined, hostApi: { client: {} } }
    const name = route === "host" ? "probe" : "beforeRequest"
    const payload = new TextEncoder().encode(JSON.stringify({ name, handler: 0, conversationId: 1, taskId: 1, payload: {} }))
    const { host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [{ id: 1, kind: EFFECT.hook, payload }] } : (ev.kind === EVENT.hook_done ? (seenAt = clock.ticks, undefined) : undefined)), runtime)
    host.send(EVENT.resume)
    for (let i = 0; i < 20; i++) await Promise.resolve()
    assert.notEqual(seenAt, -1, `${route}: hook_done arrived`)
    assert.equal(seenAt, baseline, `${route}: no hop between the handler settling and hook_done`)
  }
})

// dcore-trace section 10: env, section and deferred completions are sent from the reaction on the handler's own awaited result, with no
// other host hop; the core adds Pi's extra hops after receipt.
test("env, section and deferred completions report in the tick of a bare await of the user code", async () => {
  const user = async () => { await Promise.resolve(); return null }
  const cases: Array<[string, number, unknown, any]> = [
    ["env", EFFECT.env, { conversationId: 1 }, { registry: undefined, models: undefined, hostApi: { client: {} }, client: {}, env: user }],
    ["section", EFFECT.section, { conversationId: 1, taskId: 1, section: 0, input: {}, handler: 0 }, { registry: { at: () => ({ render: user }) }, models: undefined, client: {} }],
    ["deferred", EFFECT.deferred, { op: "fetch", model: { provider: "p", modelId: "m" }, handle: "h" }, { registry: undefined, models: { getModel: () => ({}), fetchDeferred: user }, client: {} }],
  ]
  for (const [name, kind, body, runtime] of cases) {
    const clock = spin()
    let baseline = -1
    void (async () => { await user(); baseline = clock.ticks })()
    let seenAt = -1
    const payload = new TextEncoder().encode(JSON.stringify(body))
    const { host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [{ id: 1, kind, payload }] } : (ev.kind === EVENT.hook_done ? (seenAt = clock.ticks, undefined) : undefined)), runtime)
    host.send(EVENT.resume)
    for (let i = 0; i < 20; i++) await Promise.resolve()
    assert.notEqual(seenAt, -1, `${name}: completion arrived`)
    assert.equal(seenAt, baseline, `${name}: no hop between the user code settling and the completion`)
  }
})

// scheduler.ts #decide calls registry.snapshot() inside the step line job; the host hook `registrySnapshot` is that call: it runs the
// user's reader once, synchronously when the hook effect starts, and answers the snapshot JSON.
test("registrySnapshot hook: the reader runs once, synchronously at effect start, and the answer is the registry snapshot", async () => {
  const { HostRegistry } = await import("../src/registry.ts")
  let calls = 0
  const source = { snapshot: () => { calls++; return { installed: () => [], task: () => undefined, tasks: () => [] } } }
  const registry = new HostRegistry(source as any)
  const before = calls
  const payload = new TextEncoder().encode(JSON.stringify({ name: "registrySnapshot", handler: 0, conversationId: 1, taskId: 1, payload: null }))
  let answer: unknown
  const { host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [{ id: 1, kind: EFFECT.hook, payload }] } : (ev.kind === EVENT.hook_done ? (answer = JSON.parse(new TextDecoder().decode(ev.payload.subarray(5))), undefined) : undefined)),
    { registry, models: undefined, hostHooks: { registrySnapshot: () => registry.snapshot() } } as any)
  host.send(EVENT.resume)
  assert.equal(calls - before, 1, "the reader ran synchronously, once, when the effect started")
  for (let i = 0; i < 5; i++) await Promise.resolve()
  assert.deepEqual(answer, { extensions: [] })
})

// ABI section 8 (tx end): the core answers `end` with tx_result {result:null} when the commit's job completes (after its hops chain);
// the caller's promise resolves then, not when the end event is sent, so a tool's api.commit resumes at Pi's job end.
test("tx end: end() stays pending until the core's ack and resolves with it", async () => {
  let txId = 0
  const { core, host, client } = rig(ev => {
    if (ev.kind === EVENT.tx && ev.body.op === "begin") { txId = ev.id!; return { notices: [{ kind: 3, payload: txAnswer(txId, { ready: true }) }] } }
    if (ev.kind === EVENT.resume) return { notices: [{ kind: 3, payload: txAnswer(txId, { result: null }) }] }
    return undefined
  }, noRuntime)
  const channel = await client.begin({})
  let ended = false
  const done = channel.end(undefined, null).then(() => { ended = true })
  for (let i = 0; i < 10; i++) await Promise.resolve()
  assert.equal(ended, false, "the end event alone does not resolve it")
  assert.equal(core.ofKind(EVENT.tx).at(-1)!.body.op, "end")
  host.send(EVENT.resume)
  await done
  assert.equal(ended, true)
})

function txAnswer(id: number, body: unknown): Uint8Array {
  const json = new TextEncoder().encode(JSON.stringify(body))
  const out = new Uint8Array(4 + json.length)
  new DataView(out.buffer).setUint32(0, id, true)
  out.set(json, 4)
  return out
}

// The caller of a commit resumes the hops after the core's ack that Pi's does after the line job: the ack promise, one reaction, and one
// adoption (Pi: `run` = tail.then(job) adopting #runCommit's promise). The baseline builds exactly that chain from a bare deferred.
test("api.commit: the commit's promise settles two hops after the ack, like Pi's run after the job", async () => {
  const { invocationCommit, UNBOUND } = await import("../src/facade/hostapi.ts")
  const { BACKGROUND_CONTEXT } = await import("@earendil-works/chord/context")
  let txId = 0
  const { host, client } = rig(ev => {
    if (ev.kind === EVENT.tx && ev.body.op === "begin") { txId = ev.id!; return { notices: [{ kind: 3, payload: txAnswer(txId, { ready: true }) }] } }
    if (ev.kind === EVENT.resume) return { notices: [{ kind: 3, payload: txAnswer(txId, { result: null }) }] }
    return undefined
  }, noRuntime)
  const commit = invocationCommit({ client } as any, { invocation: 1 }, UNBOUND)
  let seenAt = -1
  const clock = spin()
  const p = commit(async () => 7, BACKGROUND_CONTEXT).then(v => { seenAt = clock.ticks; return v })
  for (let i = 0; i < 20; i++) await Promise.resolve()   // the commit is waiting for the ack
  assert.equal(seenAt, -1)
  let baseline = -1
  let ack!: () => void
  const deferred = new Promise<void>(resolve => { ack = resolve })
  const reaction = deferred.then(() => 7)
  void (async () => reaction)().then(() => { baseline = clock.ticks })
  host.send(EVENT.resume)   // the ack notice resolves the commit's chain here ...
  ack()                     // ... and the baseline's deferred in the same tick
  assert.equal(await p, 7)
  for (let i = 0; i < 10; i++) await Promise.resolve()
  assert.notEqual(baseline, -1)
  assert.equal(seenAt, baseline, "the same hops after the ack as the bare chain")
})

test("hops: hopped carries the chain's effect ID as a little-endian u32, whole range", async () => {
  for (const id of [0, 1, 255, 256, 65535, 65536, 0x12345678, 0xffffffff]) {
    const { core, host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [hops(id, 1)] } : undefined), noRuntime)
    host.send(EVENT.resume)
    for (let i = 0; i < 4; i++) await Promise.resolve()
    const sent = core.ofKind(EVENT.hopped).map(e => Buffer.from(e.payload).toString("hex"))
    assert.deepEqual(sent, [Buffer.from(new Uint32Array([id]).buffer).toString("hex")], `id ${id}`)
  }
})

// A chain that carries commits (ABI section 5.3, hops effect `u32 n, u32 at, commit*`): the host applies them after hop `at`, in the microtask of that
// hop, and drops them with the chain.
const i64 = (v: number) => { const b = new Uint8Array(9); b[0] = 1; new DataView(b.buffer).setBigInt64(1, BigInt(v), true); return b }
const commitBytes = (seq: number, value: number) => {
  const stmt = Uint8Array.of(0, 0, 1, ...i64(value))
  const body = new Uint8Array(8 + 2 + stmt.length)
  const dv = new DataView(body.buffer)
  dv.setBigInt64(0, BigInt(seq), true); dv.setUint16(8, 1, true); body.set(stmt, 10)
  const out = new Uint8Array(4 + body.length)
  new DataView(out.buffer).setUint32(0, body.length, true); out.set(body, 4)
  return out
}
const hopsWith = (id: number, n: number, at: number, ...commits: Uint8Array[]) => ({ id, kind: EFFECT.hops, payload: Uint8Array.of(...u32(n), ...u32(at), ...commits.flatMap(c => [...c])) })
const countRows = (store: any) => Number(store.all("SELECT count(*) FROM t", [])[0][0])

test("hops: a chain's commits are applied in the microtask of hop `at`, before the chain's later hops, and in order", async () => {
  const { host, store, core } = rig(ev => (ev.kind === EVENT.resume ? { effects: [hopsWith(7, 5, 2, commitBytes(1, 10), commitBytes(2, 20))] } : undefined), noRuntime, { sqlTable: ["INSERT INTO t VALUES (?)"] })
  store.run("CREATE TABLE t (x)", [])
  const seen: number[] = []
  const probe = () => { seen.push(countRows(store)); if (seen.length < 7) queueMicrotask(probe) }
  queueMicrotask(probe)
  host.send(EVENT.resume)
  for (let i = 0; i < 8; i++) await Promise.resolve()
  assert.deepEqual(seen.slice(0, 5), [0, 0, 2, 2, 2], "both commits land in hop 2: not at the step that opened the chain, not at its end")
  assert.deepEqual(store.all("SELECT x FROM t ORDER BY rowid", []).map((r: unknown[]) => r[0]), [10, 20])
  assert.equal(core.ofKind(EVENT.hopped).length, 1, "hopped follows hop 5")
  assert.equal(host.counters.commits, 2)
})

test("hops: a chain cancelled before hop `at` never applies its commits", async () => {
  const { host, store } = rig(ev => {
    if (ev.kind === EVENT.resume) return { effects: [hopsWith(7, 5, 3, commitBytes(1, 10))] }
    if (ev.kind === EVENT.timer) return { effects: [{ id: 8, kind: EFFECT.cancel, payload: u32(7) }] }
    return undefined
  }, noRuntime, { sqlTable: ["INSERT INTO t VALUES (?)"] })
  store.run("CREATE TABLE t (x)", [])
  host.send(EVENT.resume)
  await Promise.resolve()
  host.send(EVENT.timer, u32(0))
  for (let i = 0; i < 8; i++) await Promise.resolve()
  assert.equal(countRows(store), 0)
  assert.equal(host.inflight, 0)
})

test("hops: a malformed chain (commits without `at`, `at` past the end) discards the handle", () => {
  for (const effect of [hopsWith(7, 5, 0, commitBytes(1, 10)), hopsWith(7, 5, 5, commitBytes(1, 10)), hopsWith(7, 5, 2)]) {
    const { host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [effect] } : undefined), noRuntime, { sqlTable: ["INSERT INTO t VALUES (?)"] })
    assert.throws(() => host.send(EVENT.resume), FatalError)
  }
})

// The `hop` export (ABI section 4.1): the host asks the core to run a hopped event without a payload and reads an integer for a step it would not decode.
test("hop: a core that answers 1 opens the chain it names; 0 ends it; a step is applied as any step", async () => {
  const log: string[] = []
  const answers: Array<0 | 1 | "step"> = [1, 1, "step", 0]
  const { core, host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [hops(7, 2)] } : undefined), noRuntime)
  const session = core as any
  session.hop = (id: number, _now: number) => {
    log.push(`hop ${id}`)
    const next = answers.shift()!
    if (next === "step") return { status: 0, commits: [], reads: [], notices: [], effects: [{ ...hops(40, 1), origin: 0 }], bytes: 0 }
    session.hopId = 20 + log.length; session.hopTicks = 1 + log.length
    return next
  }
  host.send(EVENT.resume)
  for (let i = 0; i < 20; i++) await Promise.resolve()
  assert.deepEqual(log, ["hop 7", "hop 21", "hop 22", "hop 40"], "each chain's end asks the core through hop, with its own ID")
  assert.equal(host.counters.hopsThin, 2)
  assert.equal(host.counters.hopChains, 4)
  assert.equal(core.ofKind(EVENT.hopped).length, 0, "no hopped event went through step")
  assert.equal(host.inflight, 0)
})

test("hop: a trace, a settings reader or queued events take the step path, so nothing observes the event out of order", async () => {
  for (const options of [{ trace: { event: () => {} } }, { settings: () => ({}) }]) {
    const { core, host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [hops(7, 1)] } : undefined), noRuntime, options as any)
    let asked = 0
    ;(core as any).hop = () => { asked++; return 0 }
    host.send(EVENT.resume)
    for (let i = 0; i < 4; i++) await Promise.resolve()
    assert.equal(asked, 0)
    assert.equal(core.ofKind(EVENT.hopped).length, 1)
  }
})

// The Harness hands the core changed settings through beforeSend before every event (facade/harness.ts syncSettings); a `hopped`
// event the `hop` export answers is an event like any other, so the step that resumes the parked work sees the settings of its time.
test("hop: the integer path runs beforeSend before the core takes the hopped event", async () => {
  const log: string[] = []
  const { core, host } = rig(ev => (ev.kind === EVENT.resume ? { effects: [hops(7, 2)] } : undefined), noRuntime)
  ;(core as any).hop = (id: number) => { log.push(`hop ${id}`); return 0 }
  host.send(EVENT.resume)
  host.beforeSend = () => { log.push("beforeSend") }
  for (let i = 0; i < 6; i++) await Promise.resolve()
  assert.deepEqual(log, ["beforeSend", "hop 7"])
})

// The `hop` export's step answer is applied as `send` applies a step: inside the host loop, so an event a handler sends while the step's effects
// start (an env effect without HarnessOptions.env completes synchronously) queues behind the step instead of running inside it.
test("hop: a step answered through hop applies its effects before an event a handler sends while they start, as through step", async () => {
  const timer = (id: number) => { const b = new Uint8Array(13); const dv = new DataView(b.buffer); dv.setUint32(0, id, true); dv.setFloat64(4, 2000, true); return { id, kind: EFFECT.timer, payload: b } }
  const envEffect = { id: 50, kind: EFFECT.env, payload: new TextEncoder().encode(JSON.stringify({ conversationId: 1 })) }
  const effects = () => [envEffect, timer(51)]
  const run = async (thin: boolean) => {
    const set: number[] = []
    const timers = { set: (id: number) => { set.push(id) }, clear: () => {}, liveness: () => {} }
    const { core, host } = rig(ev => {
      if (ev.kind === EVENT.resume) return { effects: [hops(7, 1)] }
      if (ev.kind === EVENT.hopped) return { effects: effects() }
      if (ev.kind === EVENT.hook_done) return { effects: [timer(60)] }
      return undefined
    }, { registry: undefined as any, models: undefined as any, hostApi: { client: {} } as any, client: {} as any }, { timers })
    if (thin) (core as any).hop = (id: number) => { core.seen.push({ kind: EVENT.hopped, payload: u32(id) }); return { status: 0, commits: [], reads: [], notices: [], effects: effects().map(e => ({ ...e, origin: 0 })), bytes: 0 } }
    host.send(EVENT.resume)
    for (let i = 0; i < 6; i++) await Promise.resolve()
    assert.equal(host.discarded, undefined)
    return set
  }
  assert.deepEqual(await run(false), [51, 60], "through step: the hopped step's effects start, then the env completion's step runs")
  assert.deepEqual(await run(true), [51, 60], "through hop: the same order")
})

// The host keeps the last effect ID it started, not a set of every ID of the handle's life (ABI section 3, invariant 6: effect
// IDs increase). A reissued ID or one below the last, in the same step or a later one, discards the handle; increasing IDs do not.
test("effect IDs: a reissued or decreasing effect ID discards the handle; increasing IDs across steps run", () => {
  const cases: Array<[string, number[][], boolean]> = [
    ["increasing across steps", [[1, 2], [3], [7]], false],
    ["zero first", [[0], [1]], false],
    ["reissued in one step", [[1, 1]], true],
    ["reissued in a later step", [[1, 2], [2]], true],
    ["below the last one", [[4], [3]], true],
  ]
  for (const [name, steps, fatal] of cases) {
    const script = steps.slice()
    const { host } = rig(ev => (ev.kind === EVENT.resume ? { effects: (script.shift() ?? []).map(id => hops(id, 1)) } : undefined), noRuntime)
    let thrown: unknown
    try { for (let i = 0; i < steps.length; i++) host.send(EVENT.resume) } catch (e) { thrown = e }
    if (fatal) assert.ok(thrown instanceof FatalError && /issued twice or out of order/.test(thrown.message), `${name}: ${String(thrown)}`)
    else assert.equal(thrown, undefined, `${name}: ${String(thrown)}`)
  }
})
