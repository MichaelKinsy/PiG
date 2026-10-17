// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Per-step observability and the flight recorder (observe.ts) against the production core: step records carry what each event
// cost, the ring keeps the last events with the inputs the core took, a trap report replays, and a recording the host writes
// replays under the Go count tool.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { existsSync, readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { EVENT } from "../src/abi.ts"
import { DoSession } from "../src/do-session.ts"
import { FatalError, HostSession } from "../src/host.ts"
import { decodeRecording, encodeRecording, FlightRecorder, replayEvents, replayReport, type TrapReport } from "../src/observe.ts"
import { CorePool } from "../src/pool.ts"
import { HostRegistry } from "../src/registry.ts"
import { nodeStore } from "../src/store-node.ts"
import { CoreTrap, WasmCore, type WasmSession } from "../src/wasm.ts"
import type { Step } from "../src/wire.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const repo = join(root, "..", "..", "..", "..")
const wasmPath = join(root, "dist", `core-${toolchain}.wasm`)
const UUID = "0190a6b2-0000-7000-8000-000000000001"
const OPEN = { mode: "session", settings: {}, models: [], registry: { extensions: [] }, sidecar: "index", delta: true }

/** The production core, built on demand with the suite's toolchain. */
function coreModule(): WebAssembly.Module {
  if (!existsSync(wasmPath)) execFileSync(join(root, "..", "..", "budget", "build-wasm.sh"), [toolchain, join(root, "..", "..", "cmd", "corewasm"), wasmPath], { stdio: "inherit" })
  return new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(wasmPath))
}

/** A host session on the production core, every step it returns collected. */
function rig(options: { events?: number; liveEvery?: number; trapAt?: number } = {}) {
  const module = coreModule()
  const core = new WasmCore(module, { uuidv7: () => UUID, log: () => {} })
  const store = nodeStore(join(tempDir("observe"), "s.sqlite"))
  const recorder = new FlightRecorder({ events: options.events, liveEvery: options.liveEvery, coreVersion: "test-build" })
  const session = core.newSession()
  const steps: Step[] = []
  let delivered = 0
  // The trap is the core's: it throws inside the recorded call, as a Wasm trap does.
  const trapping = new Proxy(session, {
    get(target, name) {
      const value = (target as never as Record<string | symbol, unknown>)[name]
      if (name !== "step" && name !== "rows") return typeof value === "function" ? value.bind(target) : value
      return (...args: unknown[]) => {
        if (delivered++ === options.trapAt) throw new CoreTrap("injected trap")
        return (value as (...a: unknown[]) => Step).apply(target, args)
      }
    },
  })
  const collect = (run: () => Step): Step => { const step = run(); steps.push(step); return step }
  const observed = recorder.attach(trapping, store)
  const handle = new Proxy(observed, {
    get(target, name) {
      if (name === "step") return (kind: number, payload: Uint8Array | undefined, now: number) => collect(() => target.step(kind, payload, now))
      if (name === "rows") return (answers: never, now: number) => collect(() => target.rows(answers, now))
      const value = (target as never as Record<string | symbol, unknown>)[name]
      return typeof value === "function" ? value.bind(target) : value
    },
    set(target, name, value) { (target as never as Record<string | symbol, unknown>)[name] = value; return true },
  })
  const host = new HostSession({ core: handle, sqlTable: core.sqlTable, store, clock: () => 1000 })
  return { module, core, store, recorder, host, steps, session: session as WasmSession }
}

test("a step record says what the event cost: kind, crossings, rows, bytes, memory, live set and core version", () => {
  const r = rig({ liveEvery: 1 })
  r.host.sendJSON(EVENT.open, OPEN)
  const open = r.recorder.stepRecords()
  assert.equal(open[0]!.event, "open")
  assert.equal(open[0]!.seq, 0)
  assert.equal(open[0]!.status, "ok")
  assert.equal(open[0]!.core, "test-build")
  assert.equal(open[0]!.bytesIn, new TextEncoder().encode(JSON.stringify(OPEN)).length)
  assert.ok(open[0]!.bytesOut > 0 && open[0]!.linearBytes > 0 && open[0]!.stepMs >= 0)
  assert.ok(open[0]!.liveBytes! > 0, "liveEvery asks the core for mem_stats")
  assert.ok(open[0]!.crossings >= 1 + open[0]!.reads, "the step, and a crossing for every pig.read")
  // The empty database is migrated by the open: its commits and rows are the open's, not the next event's.
  const written = open.reduce((n, s) => n + s.rowsWritten, 0)
  assert.equal(written, r.store.counters.rowsWritten)
  assert.equal(open.reduce((n, s) => n + s.commits, 0), r.store.counters.commits)
  assert.equal(open.reduce((n, s) => n + s.rowsRead, 0), r.store.counters.rowsRead)
  assert.ok(written > 0, "a migration wrote rows")
  assert.equal(open.reduce((n, s) => n + s.statements, 0), r.store.counters.statements)
  // What the core allocated during each step: alloc_stats differences.
  assert.ok(open.every(s => s.allocBytes! > 0 && s.mallocs! > 0 && s.allocBytes! >= s.mallocs!), JSON.stringify(open.map(s => [s.allocBytes, s.mallocs])))
  const total = r.core.allocStats()
  assert.ok(open.length > 1 && open.reduce((n, s) => n + s.allocBytes!, 0) <= total.bytes && open.reduce((n, s) => n + s.mallocs!, 0) <= total.mallocs, "each record has its own step's allocations, not the instance's running total")
  // Events after the close are rejected, and the record says so.
  r.host.sendJSON(EVENT.close, {})
  const late = r.host.sendJSON(EVENT.submit, { type: "input", content: "late", requestId: "r2" })
  assert.equal(late?.status, 1, "the core rejects an event after close")
  const records = r.recorder.stepRecords()
  assert.equal(records.at(-1)!.event, "submit")
  assert.equal(records.at(-1)!.status, "rejected")
  assert.equal(records.at(-2)!.status, "ok", "the close itself is accepted")
  // Each record counts the notices and effects its step returned (the close answers with notices).
  assert.deepEqual(records.map(s => [s.notices, s.effects]), r.steps.map(s => [s.notices.length, s.effects.length]))
  assert.ok(records.some(s => s.notices > 0), "some step returned a notice")
  r.host.discard(new FatalError("done"))
})

test("a record with no live-set request carries none, and the recorder never asks the core for mem_stats", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  assert.ok(r.recorder.stepRecords().every(s => s.liveBytes === undefined))
})

test("the ring keeps the last events, the first event of the session apart, and says what it dropped", () => {
  const r = rig({ events: 2 })
  r.host.sendJSON(EVENT.open, OPEN)
  for (let i = 0; i < 3; i++) r.host.sendJSON(EVENT.registry, { settings: { n: i } })
  const report = r.recorder.report(new Error("probe"))
  assert.equal(report.totalEvents, r.recorder.stepRecords().length)
  assert.equal(report.events.length, 2)
  assert.equal(report.truncated, true)
  assert.equal(report.opened?.event, "open")
  assert.equal(report.opened?.seq, 0)
  assert.deepEqual(report.events.map(e => e.seq), [report.totalEvents - 2, report.totalEvents - 1])
  assert.equal(report.coreVersion, "test-build")
  assert.throws(() => r.recorder.recording(), /the ring dropped/)
})

test("a trap report names the trapping event last, with its record marked trap", () => {
  const r = rig({ trapAt: 2 })
  r.host.sendJSON(EVENT.open, OPEN)
  let error: unknown
  try { r.host.sendJSON(EVENT.registry, { settings: {} }); r.host.sendJSON(EVENT.registry, { settings: { n: 1 } }) } catch (e) { error = e }
  assert.ok(error instanceof FatalError)
  const report = r.recorder.report(error)
  assert.equal(report.error.message, "injected trap")
  const last = report.steps.at(-1)!
  assert.equal(last.status, "trap")
  assert.equal(last.seq, report.events.at(-1)!.seq, "the trapping event is the ring's last")
  assert.equal(last.event, report.events.at(-1)!.event)
  assert.equal(report.steps.length, r.steps.length + 1, "every step before it returned; it did not")
  assert.ok(report.steps.slice(0, -1).every(step => step.status === "ok"))
  JSON.stringify(report) // a report is JSON
})

test("a ring that holds the whole session replays it: the same steps, from the recorded reads and uuids", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  r.host.sendJSON(EVENT.registry, { settings: { a: 1 } })
  r.host.sendJSON(EVENT.close, {})
  const report = r.recorder.report(new Error("replay"))
  assert.equal(report.truncated, false)
  const out = replayReport(r.module, JSON.parse(JSON.stringify(report)))
  assert.equal(out.trap, undefined)
  assert.deepEqual(out.steps, r.steps, "every step of the replay equals the step the live run returned")
  assert.ok(r.recorder.recordedEvents().some(e => e.reads.length > 0 || e.uuids.length > 0 || e.kind === EVENT.rows), "the open took inputs besides its event")
})

// The open of an empty store takes no pig.read and no uuid: the divergences of reads and uuids are pinned on the production
// recordings below, which take both.
test("a changed event changes the steps of a replay", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  const events = r.recorder.recordedEvents()
  const changed = events.map(e => ({ ...e }))
  changed[0] = { ...changed[0]!, wire: new TextEncoder().encode("{") }
  const out = replayEvents(r.module, changed)
  assert.notEqual(out.steps[0]!.status, r.steps[0]!.status, "an open that is not JSON is not the recorded open")
})

test("a truncated ring replays the way recovery does: open over the store, then the failing event once", () => {
  const r = rig({ events: 2 })
  r.host.sendJSON(EVENT.open, OPEN)
  for (let i = 0; i < 4; i++) r.host.sendJSON(EVENT.registry, { settings: { n: i } })
  const report = r.recorder.report(new Error("late"))
  assert.equal(report.truncated, true)
  assert.throws(() => replayReport(r.module, report), /dropped the start/)
  const out = replayReport(r.module, report, r.store, r.core.sqlTable)
  assert.equal(out.trap, undefined)
  assert.equal(out.steps.length, 2, "the open and the failing event")
})

test("the recording codec round-trips events with their reads and uuids", () => {
  const events = [
    { seq: 0, kind: EVENT.open, now: 1000.5, wire: new Uint8Array([1, 2, 3]), reads: [{ request: new Uint8Array([9, 9]), answer: new Uint8Array([7]) }], uuids: [UUID] },
    { seq: 1, kind: EVENT.close, now: 1001, wire: new Uint8Array(0), reads: [], uuids: [] },
  ]
  const { abiId, events: back } = decodeRecording(encodeRecording(events, "f".repeat(64)))
  assert.equal(abiId, "f".repeat(64))
  assert.deepEqual(back, events)
})

test("a recording the host writes replays under the Go count tool, natively and as the shipped Wasm build", () => {
  const r = rig()
  r.host.sendJSON(EVENT.open, OPEN)
  r.host.sendJSON(EVENT.registry, { settings: { a: 1 } })
  const file = join(tempDir("recording"), "ring.rec")
  writeFileSync(file, r.recorder.recording())
  const out = execFileSync("go", ["run", "./durable/core/contracttest/cmd/countgate", "replay", "--rec", file, `--${toolchain}`, wasmPath], { cwd: repo, env: { ...process.env, GOWORK: "" }, encoding: "utf8" })
  assert.match(out, /native: replayed/)
  assert.match(out, new RegExp(`${toolchain}: replayed`))
  assert.match(out, /"storage.events": \d+/)
})


/** A Durable Object's storage over node:sqlite: the surface doStore and AlarmTimers use. */
function fakeStorage(path: string): DurableObjectStorage {
  const store = nodeStore(path)
  const db = store.db
  const exec = (query: string, ...bindings: unknown[]) => {
    const stmt = db.prepare(query)
    const args = bindings.map(b => b instanceof ArrayBuffer ? new Uint8Array(b) : b) as (string | number | null | Uint8Array)[]
    if (stmt.columns().length > 0) {
      stmt.setReturnArrays(true)
      const rows = stmt.all(...args) as unknown as unknown[][]
      return { toArray: () => rows, raw: () => rows[Symbol.iterator](), rowsRead: rows.length, rowsWritten: 0 }
    }
    const changes = Number(stmt.run(...args).changes)
    return { toArray: () => [], raw: () => [][Symbol.iterator](), rowsRead: 0, rowsWritten: changes }
  }
  return {
    sql: { exec },
    transactionSync: (work: () => unknown) => { db.exec("BEGIN IMMEDIATE"); try { const out = work(); db.exec("COMMIT"); return out } catch (e) { db.exec("ROLLBACK"); throw e } },
    getAlarm: () => null, setAlarm: () => {}, deleteAlarm: () => {},
  } as never as DurableObjectStorage
}

test("a DoSession with a recorder attaches the trap report to the fatal error it raises, and the report replays", () => {
  const module = coreModule()
  const pool = new CorePool(module, { core: { uuidv7: () => UUID, log: () => {} } })
  const recorder = new FlightRecorder({ coreVersion: "do-build" })
  const fatal: Array<Error & { report?: ReturnType<FlightRecorder["report"]> }> = []
  const session = new DoSession({
    pool, storage: fakeStorage(join(tempDir("do"), "s.sqlite")), open: () => OPEN, clock: () => 1000, recorder,
    runtime: () => ({ registry: { snapshot: () => ({ installed: () => [], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} } as never, models: { getModel: () => ({}), streamSimple: () => { throw new Error("no models") } } as never, report: () => {} }),
    onFatal: error => { fatal.push(error) },
  })
  session.ensureOpen()
  session.broken(new CoreTrap("unreachable executed"))
  assert.equal(fatal.length, 1)
  const report = fatal[0]!.report!
  assert.ok(report, "the fatal error carries the report")
  assert.equal(report.coreVersion, "do-build")
  assert.equal(report.error.message, "unreachable executed")
  assert.equal(report.events[0]!.event, "open")
  assert.equal(report.truncated, false)
  assert.ok(report.steps.length >= 1 && report.steps.every(s => s.core === "do-build"))
  assert.equal(replayReport(module, JSON.parse(JSON.stringify(report))).trap, undefined)
  // The next call cold-opens again with a fresh ring: the recorder describes one session lifetime.
  session.ensureOpen()
  assert.equal(recorder.recordedEvents()[0]!.kind, EVENT.open)
  assert.equal(recorder.report(new Error("x")).totalEvents, recorder.stepRecords().length)
  session.close()
})

test("a trap the session recovers from in place still reports: onTrap has the report before the reopen starts a fresh ring", async () => {
  const module = coreModule()
  // The first instance traps after the core ran its first api event, as a Wasm trap at the end of a step does.
  let instances = 0
  const pool = new CorePool(module, {
    core: { uuidv7: () => UUID, log: () => {} },
    onInstance: core => {
      if (instances++ > 0) return
      const original = core.newSession.bind(core)
      core.newSession = () => new Proxy(original(), {
        get(target, name) {
          const value = (target as never as Record<string | symbol, unknown>)[name]
          if (name !== "step") return typeof value === "function" ? value.bind(target) : value
          return (...args: unknown[]) => {
            const step = (value as (...a: unknown[]) => Step).apply(target, args)
            if (args[0] === EVENT.api) throw new CoreTrap("injected trap after api")
            return step
          }
        },
      }) as WasmSession
    },
  })
  const traps: TrapReport[] = []
  const recorder = new FlightRecorder({ coreVersion: "do-build", onTrap: report => { traps.push(report) } })
  const fatal: Error[] = []
  const session = new DoSession({
    pool, storage: fakeStorage(join(tempDir("do-recover"), "s.sqlite")), clock: () => 1_000_000, recorder,
    open: () => ({ mode: "storage", settings: {}, models: [], registry: { extensions: [] }, sidecar: "off", delta: true }),
    runtime: () => ({ registry: new HostRegistry({ snapshot: () => ({ installed: () => [], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} } as never), models: { getModel: () => ({}), streamSimple: () => { throw new Error("no models") } } as never, report: () => {} }),
    onFatal: error => { fatal.push(error) },
  })
  session.ensureOpen()
  assert.equal(await session.client.api("storage.task", { id: 1 }), null, "the request was fed again on a new instance")
  assert.equal(pool.created, 2, "the trap was recovered in place")
  assert.equal(fatal.length, 0, "a recovered trap raises no fatal error")
  assert.equal(traps.length, 1, "the recovered trap still reported")
  const report = traps[0]!
  assert.equal(report.error.message, "injected trap after api")
  assert.equal(report.coreVersion, "do-build")
  assert.equal(report.events.at(-1)!.kind, EVENT.api, "the trapping event is the ring's last")
  assert.equal(report.steps.at(-1)!.status, "trap")
  assert.equal(report.events[0]!.kind, EVENT.open)
  assert.equal(recorder.recordedEvents()[0]!.kind, EVENT.open)
  assert.ok(recorder.stepRecords().every(s => s.status !== "trap"), "the reopen started a fresh ring: the trap is only in the report")
  session.close()
})

test("recordings of the production native run replay through the Wasm core with their reads and uuids; a recorder watching the replay writes the same recording", () => {
  const dir = tempDir("goreplay")
  const exe = join(dir, "bench")
  execFileSync("go", ["build", "-o", exe, "./durable/core/sqlhost/cmd/bench"], { cwd: repo, env: { ...process.env, GOWORK: "" } })
  // A process that never touches a lane's or a user's agent directory; the same deterministic clock the gate uses.
  const env = { ...process.env, HOME: join(dir, "home"), PIG_CODING_AGENT_DIR: join(dir, "agent"), PI_CODING_AGENT_DIR: join(dir, "agent"), PIG_HOME: join(dir, "pig"), PI_HOME: join(dir, "pig"), TZ: "UTC", DETENV_EPOCH: "1800000000000" }
  const db = join(dir, "s.sqlite")
  const bench = (name: string, ...args: string[]) => {
    execFileSync(exe, ["--db", db, "--trace", join(dir, `${name}.trace`), "--record", join(dir, `${name}.rec`), "--tools", "1", "--variant", "incr", ...args], { env, stdio: "ignore" })
    return decodeRecording(readFileSync(join(dir, `${name}.rec`)))
  }
  // Seed three turns on an empty store (the core mints its session id: a uuid), then run one in another process, which opens
  // the store that now exists (the core reads it back: pig.read).
  const seeded = bench("seed", "--seed", "3", "--turns", "0")
  const reopened = bench("reopen", "--turns", "1")
  assert.ok(seeded.events.some(e => e.uuids.length > 0), "creating the conversation takes a uuid")
  assert.ok(reopened.events.some(e => e.reads.length > 0), "reopening a store makes the core read it")
  const module = coreModule()
  for (const { abiId, events } of [seeded, reopened]) {
    assert.equal(abiId, new WasmCore(module, { log: () => {} }).abiId)
    assert.ok(events.length > 100)
    const observer = new FlightRecorder({ events: events.length + 1 })
    const out = replayEvents(module, events, { recorder: observer })
    assert.equal(out.trap, undefined)
    assert.equal(out.steps.length, events.length)
    assert.deepEqual(decodeRecording(observer.recording()).events, events, "the recorder's taps saw the same reads (request bytes as the core wrote them) and uuids")
    const records = observer.stepRecords()
    assert.equal(records.reduce((n, s) => n + s.reads, 0), events.reduce((n, e) => n + e.reads.length, 0))
    assert.equal(records.reduce((n, s) => n + s.crossings, 0), events.reduce((n, e) => n + 1 + e.reads.length + e.uuids.length, 0), "the Go gate's storage.crossings, event by event")
  }
  // A replay whose core takes other inputs than the recorded run stops at the event, whichever way the inputs differ.
  const copy = (events: typeof seeded.events) => events.map(e => ({ ...e, reads: e.reads.map(r => ({ ...r, request: r.request.slice() })), uuids: [...e.uuids] }))
  const withUuid = seeded.events.findIndex(e => e.uuids.length > 0), withRead = reopened.events.findIndex(e => e.reads.length > 0)
  const diverges = (events: typeof seeded.events, at: number, pattern: RegExp) => assert.throws(() => replayEvents(module, events), (e: Error) => pattern.test(e.message) && e.message.includes(`event ${at}`))
  let altered = copy(seeded.events)
  altered[withUuid]!.uuids.shift()
  diverges(altered, withUuid, /took a uuid the recorded run did not take/)
  altered = copy(seeded.events)
  altered[withUuid]!.uuids.push(altered[withUuid]!.uuids[0]!)
  diverges(altered, withUuid, /took \d+ reads and \d+ uuids, the recorded run/)
  altered = copy(reopened.events)
  altered[withRead]!.reads = altered[withRead]!.reads.slice(1)
  diverges(altered, withRead, /asked a different pig\.read|pig\.read the recorded run did not make/)
  altered = copy(reopened.events)
  altered[withRead]!.reads = []
  diverges(altered, withRead, /pig\.read the recorded run did not make/)
  altered = copy(reopened.events)
  altered[withRead]!.reads[0]!.request[0]! ^= 1 // the statement id
  diverges(altered, withRead, /asked a different pig\.read/)
  altered = copy(reopened.events)
  altered[withRead]!.reads.push(altered[withRead]!.reads[0]!)
  diverges(altered, withRead, /took \d+ reads and \d+ uuids, the recorded run/)
})
