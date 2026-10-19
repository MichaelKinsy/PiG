// The host loop's recovery from a trap (ADR D16, Durable plan 2 item 3) against scripted cores: what the old handle owned is
// cancelled and its late completions dropped, the reopened core is told what the old one was told, the events queued behind the
// trapped one are sorted into void, replayed and kept, and a second trap while recovering fails the host.
import assert from "node:assert/strict"
import { test } from "node:test"
import { EFFECT, EVENT } from "../src/abi.ts"
import { CoreClient } from "../src/client.ts"
import { FatalError, HostSession, RecycledError, RecycledUncertainError, type EffectHandler, type Renewal, type TimerDriver } from "../src/host.ts"
import { nodeStore } from "../src/store-node.ts"
import { CoreTrap } from "../src/wasm.ts"
import { apiResult, effect, ScriptedCore, txResult, type Seen, type Spec } from "./scripted.ts"

const u32 = (n: number) => { const b = new Uint8Array(4); new DataView(b.buffer).setUint32(0, n, true); return b }

/** A host over scripted cores: `script(n)` is the behaviour of the nth core (0 is the first handle). */
function make(script: (n: number) => (ev: Seen) => Spec | undefined, options: { handlers?: Record<number, EffectHandler>; timers?: TimerDriver; renew?: boolean } = {}) {
  const cores: ScriptedCore[] = []
  const next = () => { const core = new ScriptedCore(script(cores.length)); cores.push(core); return core }
  const dropped: (Error | undefined)[] = []
  const renewal: Renewal = { drop: trap => { dropped.push(trap) }, acquire: () => ({ core: next(), sqlTable: [], open: { reopened: cores.length } }) }
  const host = new HostSession({ core: next(), sqlTable: [], store: nodeStore(":memory:", { wal: false }), clock: () => 1000, handlers: options.handlers ?? {}, timers: options.timers, ...(options.renew === false ? {} : { renew: renewal }) })
  return { host, cores, dropped }
}
const trap = (message = "unreachable") => { throw new CoreTrap(message) }

test("a trap replaces the handle: the new core is opened with the owner's payload, then the trapped event runs on it once", () => {
  const { host, cores, dropped } = make(n => ev => { if (n === 0 && ev.kind === EVENT.abort) trap(); return undefined })
  host.sendJSON(EVENT.open, { first: true })
  const step = host.sendJSON(EVENT.abort, { requestId: 5 })
  assert.equal(cores.length, 2)
  assert.deepEqual(cores[1]!.seen.map(e => e.kind), [EVENT.open, EVENT.abort], "open, then the refeed")
  assert.deepEqual(cores[1]!.seen[0]!.body, { reopened: 2 }, "the open payload is the owner's, current as of the reopen")
  assert.deepEqual(cores[1]!.seen[1]!.body, { requestId: 5 }, "the same event, byte for byte")
  assert.equal(step?.status, 0, "the caller of send gets the step of the run that completed")
  assert.equal(host.discarded, undefined)
  assert.equal(host.counters.traps, 1)
  assert.equal(host.counters.recycles, 1)
  assert.equal(host.epoch, 1)
  assert.equal(dropped.length, 1)
  assert.ok(dropped[0] instanceof CoreTrap, "the owner is told the instance trapped, so it discards the instance")
  assert.equal(host.core, cores[1])
})

test("a second trap on the same event fails the host with an error that names both, and no third handle is made", () => {
  const { host, cores } = make(() => ev => { if (ev.kind === EVENT.abort) trap("poison"); return undefined })
  host.sendJSON(EVENT.open, {})
  assert.throws(() => host.sendJSON(EVENT.abort, { requestId: 1 }), (e: unknown) => e instanceof FatalError && /trapped again while recovering.*poison/.test(e.message) && e.cause2 instanceof CoreTrap)
  assert.equal(cores.length, 2, "one recycle")
  assert.equal(host.counters.traps, 2)
  assert.ok(host.discarded)
  assert.ok(cores[1]!.freed, "the failed handle is freed")
  assert.throws(() => host.send(EVENT.timer, u32(1)), FatalError, "every later event is refused")
})

test("a trap while the reopened core opens fails the host: the open is part of recovering", () => {
  const { host, cores } = make(n => ev => { if (n === 0 && ev.kind === EVENT.abort) trap(); if (n === 1 && ev.kind === EVENT.open) trap("open"); return undefined })
  host.sendJSON(EVENT.open, {})
  assert.throws(() => host.sendJSON(EVENT.abort, {}), (e: unknown) => e instanceof FatalError && /trapped again/.test(e.message))
  assert.equal(cores.length, 2)
})

test("the recovery window ends once the refeed has run: a later trap is a new event and recycles again", () => {
  const { host, cores } = make(n => ev => { if (n === 0 && ev.kind === EVENT.abort) trap(); return undefined })
  host.sendJSON(EVENT.open, {})
  host.sendJSON(EVENT.abort, { requestId: 1 })
  assert.equal(host.counters.recycles, 1)
  // core 1 now traps on the next abort: its own, unrelated event.
  cores[1]!.respond = ev => { if (ev.kind === EVENT.abort) trap(); return undefined }
  host.sendJSON(EVENT.abort, { requestId: 2 })
  assert.equal(host.counters.recycles, 2)
  assert.equal(host.discarded, undefined)
})

test("without a renewal a trap is fatal, as every other failure of the core is", () => {
  const { host } = make(() => ev => { if (ev.kind === EVENT.abort) trap(); return undefined }, { renew: false })
  host.sendJSON(EVENT.open, {})
  assert.throws(() => host.sendJSON(EVENT.abort, {}), (e: unknown) => e instanceof FatalError && e.cause2 instanceof CoreTrap)
})

test("an error that is not a trap is fatal even with a renewal: the host cannot tell what state the core is in", () => {
  const { host, cores } = make(() => ev => { if (ev.kind === EVENT.abort) throw new RangeError("bug"); return undefined })
  host.sendJSON(EVENT.open, {})
  assert.throws(() => host.sendJSON(EVENT.abort, {}), FatalError)
  assert.equal(cores.length, 1)
})

test("a Session the old core had resumed or sealed is resumed or sealed again before the refeed", () => {
  const { host, cores } = make(n => ev => { if (n === 0 && ev.kind === EVENT.abort) trap(); return undefined })
  host.sendJSON(EVENT.open, {})
  host.send(EVENT.resume)
  host.send(EVENT.seal)
  host.sendJSON(EVENT.abort, {})
  assert.deepEqual(cores[1]!.seen.map(e => e.kind), [EVENT.open, EVENT.resume, EVENT.seal, EVENT.abort])
  // A core the host never told to resume is not resumed: paused stays paused.
  const fresh = make(n => ev => { if (n === 0 && ev.kind === EVENT.abort) trap(); return undefined })
  fresh.host.sendJSON(EVENT.open, {})
  fresh.host.sendJSON(EVENT.abort, {})
  assert.deepEqual(fresh.cores[1]!.seen.map(e => e.kind), [EVENT.open, EVENT.abort])
})

test("the effects of the old handle are cancelled and their completions dropped; the reopened core's effects are numbered afresh and complete normally", async () => {
  const running: { id: number; signal: AbortSignal; host: Parameters<EffectHandler>[1]["host"] }[] = []
  const tool: EffectHandler = (e, ctx) => { running.push({ id: e.id, signal: ctx.signal, host: ctx.host }); return new Promise<void>(resolve => ctx.signal.addEventListener("abort", () => resolve())) }
  const cancelled: number[] = []
  const timers: string[] = []
  const options = { handlers: { [EFFECT.tool]: tool }, timers: { set: () => {}, clear: () => {}, liveness: () => {}, reset: () => { timers.push("reset") } } as TimerDriver }
  const { host, cores } = make(n => ev => {
    if (ev.kind === EVENT.resume) return { effects: [effect(1, EFFECT.tool, {})] }
    if (n === 0 && ev.kind === EVENT.abort) trap()
    return undefined
  }, options)
  ;(host as unknown as { opts: { onCancel?: (id: number) => void } }).opts.onCancel = id => { cancelled.push(id) }
  host.sendJSON(EVENT.open, {})
  host.send(EVENT.resume)
  assert.equal(running.length, 1)
  assert.equal(host.inflight, 1)
  host.sendJSON(EVENT.abort, {})
  // The reopen resumed the new core, which started its own effect number 1.
  assert.equal(running.length, 2, "the new core's effect ran")
  assert.ok(running[0]!.signal.aborted, "the old effect was cancelled")
  assert.deepEqual(cancelled, [1], "onCancel names it, so a custom task runtime ends the invocation")
  assert.deepEqual(timers, ["reset"], "the timers of the old handle are forgotten")
  assert.equal(host.inflight, 1)
  // Late completion of the old effect 1 would collide with the new effect 1: it is dropped.
  running[0]!.host.toolDone(1, 0, { output: [] })
  assert.equal(cores[1]!.ofKind(EVENT.tool_done).length, 0, "the old effect's completion never reaches the new core")
  running[1]!.host.toolDone(1, 0, { output: [] })
  assert.equal(cores[1]!.ofKind(EVENT.tool_done).length, 1, "the new effect's completion does")
})

test("events queued behind the trapped one: those that name the old handle's work are void, the rest keep their order after the refeed", () => {
  const { host, cores } = make(n => ev => {
    if (n === 0 && ev.kind === EVENT.abort) {
      // While the core runs the abort, three more events are queued behind it.
      host.send(EVENT.tool_done, u32(7))
      host.send(EVENT.timer, u32(3))
      host.send(EVENT.close)
      trap()
    }
    return undefined
  })
  host.sendJSON(EVENT.open, {})
  host.sendJSON(EVENT.abort, { requestId: 1 })
  assert.deepEqual(cores[1]!.seen.map(e => e.kind), [EVENT.open, EVENT.abort, EVENT.close], "the tool completion and the timer belonged to the old handle; the close did not")
})

test("a recycle at a quiet point hands the handle back at once and cold-opens the next one with the next event", () => {
  const { host, cores, dropped } = make(() => () => undefined)
  host.sendJSON(EVENT.open, {})
  assert.equal(host.quiet, true)
  host.recycle()
  assert.equal(dropped.length, 1)
  assert.equal(dropped[0], undefined, "no trap: the instance is healthy")
  assert.equal(cores.length, 1, "nothing is reopened until an event needs it")
  assert.equal(host.quiet, false)
  host.sendJSON(EVENT.abort, {})
  assert.equal(cores.length, 2)
  assert.deepEqual(cores[1]!.seen.map(e => e.kind), [EVENT.open, EVENT.abort])
  assert.equal(host.counters.recycles, 1)
  assert.equal(host.counters.traps, 0)
})

test("a rejected step is not a trap: nothing is recycled", () => {
  const { host, cores } = make(() => ev => ev.kind === EVENT.abort ? { status: 1, error: { name: "Bad", message: "no" } } : undefined)
  host.sendJSON(EVENT.open, {})
  assert.equal(host.sendJSON(EVENT.abort, {})?.status, 1)
  assert.equal(cores.length, 1)
})

test("a client request is sent again to the reopened core under its ID; one that ran for an invocation fails with RecycledError, since the invocation ended with its handle", async () => {
  const { host, cores } = make(n => ev => {
    if (ev.kind !== EVENT.api) return undefined
    if (n === 0) { if (ev.body.op === "boom") trap(); return { notices: [] } }
    return { notices: [apiResult(ev.id!, { ok: ev.body.op })] }
  })
  host.sendJSON(EVENT.open, {})
  const client = new CoreClient(host)
  const waiting = client.api("wait")
  const bound = client.api("inv", { invocation: 7 })
  const boom = client.api("boom")
  assert.equal(await boom, "boom", "the request the core trapped on is answered by the new core")
  assert.equal(await waiting, "wait", "an unanswered request is replayed")
  await assert.rejects(bound, (e: Error) => e instanceof RecycledError && /nothing of this operation was committed/.test(e.message))
  const ops = cores[1]!.seen.filter(e => e.kind === EVENT.api).map(e => e.body.op)
  assert.deepEqual(ops, ["wait", "boom"], "each replayed once, the invocation's request not at all")
  assert.deepEqual(cores[1]!.seen.filter(e => e.kind === EVENT.api).map(e => e.id), [1, 3], "under their own IDs")
})

test("a request the old core ran is sent again only when running it twice is harmless; a submission without a request ID and the abort of a submission fail with RecycledUncertainError", async () => {
  const { host, cores } = make(n => ev => {
    if (n === 0) {
      if (ev.kind === EVENT.submit && ev.body.content === "trapped") trap()
      return undefined
    }
    if (ev.kind === EVENT.submit) return { notices: [apiResult(ev.body.id, { ok: { id: 50 + ev.body.id } })] }
    if (ev.kind === EVENT.abort) return { notices: [apiResult(ev.body.requestId, { ok: "aborted" })] }
    if (ev.kind === EVENT.api) return { notices: [apiResult(ev.id!, { ok: ev.body.op })] }
    return undefined
  })
  host.sendJSON(EVENT.open, {})
  const client = new CoreClient(host)
  // Each of these ran on the old core, which had not answered when the trap came.
  const bare = client.submit({ conversationId: 1, type: "input", content: "no request ID" })
  const keyed = client.submit({ conversationId: 1, type: "input", content: "keyed", requestId: "r1" })
  const withdraw = client.abort({ submissionId: 4, conversationId: 1 })
  const stop = client.abort({ conversationId: 1 })
  const boundRead = client.api("read", { invocation: 7 })
  const boundSubmit = client.submit({ conversationId: 1, type: "input", content: "bound", invocation: 7 })
  // This one traps on its own event, so the old core never applied it; the trap replaces the handle under all of them.
  const trapped = client.submit({ conversationId: 1, type: "input", content: "trapped" })
  const uncertain = (e: unknown) => e instanceof RecycledUncertainError && !(e instanceof RecycledError) && /may or may not have been applied/.test((e as Error).message)
  await assert.rejects(bare, uncertain, "admitted or not, the old core does not say: sending it again could admit it twice")
  await assert.rejects(withdraw, uncertain, "a second withdrawal would answer settled where the first answered aborted")
  await assert.rejects(boundSubmit, uncertain, "an invocation's submission that ran is not known to be undone either")
  await assert.rejects(boundRead, (e: Error) => e instanceof RecycledError, "an invocation's read committed nothing")
  assert.deepEqual(await keyed, { id: 52 }, "the request ID makes a second admission return the first")
  assert.equal(await stop, "aborted", "aborting a conversation again is harmless")
  assert.deepEqual(await trapped, { id: 57 }, "the submission the old core never applied is sent again")
  const sent = cores[1]!.seen.filter(e => e.kind === EVENT.submit || e.kind === EVENT.abort).map(e => e.body.content ?? `abort ${e.body.submissionId ?? "conversation"}`)
  assert.deepEqual(sent, ["keyed", "abort conversation", "trapped"], "in request order, only the requests that cannot take effect twice")
})

test("a transaction whose end trapped before its commit fails with RecycledError even when the reopened core's open commits", async () => {
  const { host, cores } = make(n => ev => {
    if (ev.kind === EVENT.tx && ev.body.op === "begin") return { notices: [txResult(ev.id!, { ready: true })] }
    if (n === 0 && ev.kind === EVENT.tx && ev.body.op === "end") trap()
    // The reopened core reconciles live work as it opens: that commit is not the transaction's.
    if (n === 1 && ev.kind === EVENT.open) return { commits: [{ seq: 9, stmts: [] }] }
    return undefined
  })
  host.sendJSON(EVENT.open, {})
  const client = new CoreClient(host)
  const tx = await client.begin()
  const before = host.counters.commits
  await assert.rejects(tx.end(undefined, "value"), (e: Error) => e instanceof RecycledError && /nothing of this operation was committed/.test(e.message))
  assert.equal(host.counters.commits, before + 1, "the open's commit was applied, under the new handle")
  assert.equal(cores.length, 2)
})

test("a close the old core took and had not answered is sent to the reopened core, so closeAndJoin ends", async () => {
  const { host, cores } = make(n => ev => {
    // The old core answers the close behind its line, after a hop; the event that would carry the answer traps.
    if (n === 0 && ev.kind === EVENT.close) return { notices: [] }
    if (n === 0 && ev.kind === EVENT.hopped) trap()
    return undefined
  })
  host.sendJSON(EVENT.open, {})
  host.seal()
  const closed = host.closeAndJoin()
  host.send(EVENT.hopped, u32(1))
  const outcome = await Promise.race([closed.then(() => "closed"), new Promise(resolve => setTimeout(() => resolve("still waiting"), 200))])
  assert.equal(outcome, "closed")
  assert.deepEqual(cores[1]!.seen.map(e => e.kind), [EVENT.open, EVENT.seal, EVENT.close], "sealed again, then closed again; the hop of the old handle is void")
  assert.ok(host.discarded)
})

test("every completion an old effect makes after the handle was replaced is dropped; the same completion from the new handle's effect reaches the core", () => {
  type Host = Parameters<EffectHandler>[1]["host"]
  const hosts: Host[] = []
  const tool: EffectHandler = (_e, ctx) => { hosts.push(ctx.host); return new Promise<void>(() => {}) }
  const { host, cores } = make(n => ev => {
    if (ev.kind === EVENT.resume) return { effects: [effect(1, EFFECT.tool, {})] }
    if (n === 0 && ev.kind === EVENT.abort) trap()
    return undefined
  }, { handlers: { [EFFECT.tool]: tool } })
  host.sendJSON(EVENT.open, {})
  host.send(EVENT.resume)
  host.sendJSON(EVENT.abort, {})
  assert.equal(hosts.length, 2, "the reopened core re-issued its effect")
  const completions: [number, (h: Host) => void][] = [
    [EVENT.model_event, h => h.modelEvents(1, [{ type: "start" }])],
    [EVENT.model_event, h => h.modelEventsRaw(1, "[]")],
    [EVENT.model_bytes, h => h.modelBytes(1, 2, "")],
    [EVENT.tool_progress, h => h.toolProgress(1, 0, 0, "x")],
    [EVENT.hook_done, h => h.hookDone(1, 0, null)],
    [EVENT.phase_done, h => h.phaseDone(1, 0, null)],
    [EVENT.tool_done, h => h.toolDone(1, 0, { output: [] })],
  ]
  for (const [kind, complete] of completions) {
    const before = cores[1]!.ofKind(kind).length
    complete(hosts[0]!)
    assert.equal(cores[1]!.ofKind(kind).length, before, `the old handle's ${kind} completion never reaches the new core`)
    complete(hosts[1]!)
    assert.equal(cores[1]!.ofKind(kind).length, before + 1, `the new handle's ${kind} completion does`)
  }
})

test("a transaction that had not taken the line when the handle was replaced asks the new core for it and commits there", async () => {
  const { host, cores } = make(n => ev => {
    if (n === 0 && ev.kind === EVENT.tx && ev.body.op === "begin") return { notices: [] }
    if (n === 0 && ev.kind === EVENT.abort) trap()
    if (n === 1 && ev.kind === EVENT.tx && ev.body.op === "begin") return { notices: [txResult(ev.id!, { ready: true })] }
    if (n === 1 && ev.kind === EVENT.tx && ev.body.op === "end") return { commits: [{ seq: 1, stmts: [] }], notices: [txResult(ev.id!, { result: null })] }
    return undefined
  })
  host.sendJSON(EVENT.open, {})
  const client = new CoreClient(host)
  // The old core queued the begin behind its line (no answer yet); an unrelated event then traps.
  const began = client.begin()
  host.sendJSON(EVENT.abort, {})
  const tx = await began
  assert.equal(await tx.end(undefined, "value"), "value")
  assert.deepEqual(cores[1]!.seen.filter(e => e.kind === EVENT.tx).map(e => e.body.op), ["begin", "end"], "the begin went to the new core once, then the end")
})

test("a trap in the rows that answer a step's reads runs the event that began the chain again: nothing of it was applied", async () => {
  const { host, cores } = make(n => ev => {
    if (n > 0 && ev.kind === EVENT.submit) return { notices: [apiResult(ev.body.id, { ok: { id: 50 + ev.body.id } })] }
    return undefined
  })
  // The first core asks for a read on a registry event and on a submission; the rows that answer the read trap.
  const old = cores[0]!
  const scripted = old.step.bind(old)
  old.step = (kind, payload, now) => {
    const step = scripted(kind, payload, now)
    return kind === EVENT.registry || kind === EVENT.submit ? { ...step, reads: [{ id: 1, sql: 0, params: [] }] } : step
  }
  old.rows = () => trap()
  host.sqlTable[0] = "SELECT 1"
  host.sendJSON(EVENT.open, {})
  // An event no client replays: the host runs it again on the new core.
  host.sendJSON(EVENT.registry, { settings: { a: 1 } })
  assert.equal(host.counters.recycles, 1)
  assert.deepEqual(cores[1]!.seen.map(e => e.kind), [EVENT.open, EVENT.registry], "the registry event is fed to the new core once")
  assert.deepEqual(cores[1]!.seen[1]!.body, { settings: { a: 1 } })
  // A client request: the old core applied nothing of it, so even a submission without a request ID is sent again.
  const second = cores[1]!
  const scripted2 = second.step.bind(second)
  second.step = (kind, payload, now) => { const step = scripted2(kind, payload, now); return kind === EVENT.submit ? { ...step, notices: [], reads: [{ id: 1, sql: 0, params: [] }] } : step }
  second.rows = () => trap()
  host.sqlTable[0] = "SELECT 1"
  const client = new CoreClient(host)
  assert.deepEqual(await client.submit({ conversationId: 1, type: "input", content: "no request ID" }), { id: 51 }, "answered by the third core")
  assert.equal(host.counters.recycles, 2)
  assert.deepEqual(cores[2]!.seen.filter(e => e.kind === EVENT.submit).map(e => e.body.content), ["no request ID"], "sent to the new core once")
  assert.equal(host.discarded, undefined)
})

const u32le = (n: number) => { const b = new Uint8Array(4); new DataView(b.buffer).setUint32(0, n, true); return b }
const hopsEffect = (id: number, n: number, at = 0, ...commits: Uint8Array[]) => ({ id, kind: EFFECT.hops, payload: Uint8Array.of(...u32le(n), ...u32le(at), ...commits.flatMap(c => [...c])) })
const heldCommit = (seq: number) => {
  const body = new Uint8Array(10)
  new DataView(body.buffer).setBigInt64(0, BigInt(seq), true)
  const out = new Uint8Array(4 + body.length)
  new DataView(out.buffer).setUint32(0, body.length, true); out.set(body, 4)
  return out
}

test("a trap aborts the open hops chains of the old handle and drops the commits they hold: the new core is not sent their hopped", async () => {
  const { host, cores } = make(n => ev => {
    if (n === 0 && ev.kind === EVENT.resume) return { effects: [hopsEffect(7, 6, 3, heldCommit(1))] }
    if (n === 0 && ev.kind === EVENT.abort) trap()
    return undefined
  })
  host.sendJSON(EVENT.open, {})
  host.send(EVENT.resume)
  assert.equal(host.inflight, 1, "the chain is open")
  host.sendJSON(EVENT.abort, { requestId: 5 })
  assert.equal(host.counters.recycles, 1)
  assert.equal(host.inflight, 0, "the old handle's chain is gone with it")
  for (let i = 0; i < 12; i++) await Promise.resolve()
  assert.equal(host.counters.commits, 0, "the held commit was never applied: the reopened core re-derives it from the store")
  assert.equal(cores[1]!.ofKind(EVENT.hopped).length, 0, "the late hop did not reach the new core")
  assert.equal(host.discarded, undefined)
})

test("a trap in the core's hop export recycles the handle like a trap in a step; the old chain's id means nothing to the new core", async () => {
  const { host, cores, dropped } = make(n => ev => (n === 0 && ev.kind === EVENT.resume ? { effects: [hopsEffect(7, 2)] } : undefined))
  const first = cores[0]! as any
  first.hop = () => { throw new CoreTrap("unreachable in hop") }
  host.sendJSON(EVENT.open, {})
  host.send(EVENT.resume)
  for (let i = 0; i < 6; i++) await Promise.resolve()
  assert.equal(host.counters.traps, 1)
  assert.equal(host.counters.recycles, 1)
  assert.equal(dropped.length, 1)
  assert.equal(host.discarded, undefined, "the host is alive on the new core")
  assert.deepEqual(cores[1]!.seen.map(e => e.kind), [EVENT.open, EVENT.resume], "open, then resume replayed; the hopped the old core died on is not")
  assert.equal(host.inflight, 0)
})
