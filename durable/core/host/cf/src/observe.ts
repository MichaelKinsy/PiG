// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Per-step observability and the flight recorder of the host (Durable plan 2, item 9).
//
// A FlightRecorder wraps one core session (and, for row counts, the object's Store). It keeps, for every step, a StepRecord: the
// event kind, the crossings between host and core, the rows read and written, the bytes in and out, linear memory, the live set
// when asked for, the time in the core and the core version; and, in a ring, the last N events the core was given together with
// the pig.read answers and pig.uuidv7 values it took while each ran. The core is a pure function of that stream (ABI section 3,
// ADR D8: the clock is the event's `now`, uuids come from the host), so a trap report that carries the ring replays the failing
// step on a developer's machine: with replayEvents here, or as a recording in durable/core/contracttest/counts (countgate
// replay), natively and under both Wasm builds.
//
// The recorder is a cache like everything else the object holds (ADR D9): it is bounded, it is rebuilt by a cold open, and it
// never blocks a step. Per event it copies the payload and the bytes of the reads the core made; it does not hash or decode a
// step. `liveEvery` asks the core for mem_stats, which runs a full collection: a diagnostic setting, off by default.
import { uuidv7 } from "@earendil-works/pi-ai"
import { eventName, EVENT, ABI_ID } from "./abi.ts"
import type { CoreHandle } from "./host.ts"
import type { Store, StoreCounters } from "./store.ts"
import { CoreTrap, WasmCore, type StepTap } from "./wasm.ts"
import { decodeReadRequest, encodeAnswer, encodeRows, type Param, type Rows, type Step } from "./wire.ts"

export interface RecorderOptions {
  /** Events kept in the ring (default 256). The first event of the session is also kept, apart from the ring. */
  events?: number
  /** Step records kept (default 1024). */
  steps?: number
  /** Ask the core for its live heap after every this many steps (0, the default, never: mem_stats collects garbage). */
  liveEvery?: number
  /** The build of the core, for step records and trap reports; defaults to its abi_id. */
  coreVersion?: string
  /**
   * Called with the trap report when the core throws inside a step, before the host handles the trap. A trap the session
   * recovers from in place (do-session.ts) raises no fatal error and reopens with a fresh ring, so this is the only place
   * its report survives. An exception the callback throws is ignored: the trap stays the step's outcome.
   */
  onTrap?: (report: TrapReport) => void
}

/** One pig.read call: the request as the core wrote it and the answer the host gave (ABI section 4.2). */
export interface RecordedRead { request: Uint8Array; answer: Uint8Array }

/** One event of the core's input stream with what the core took while it ran. `wire` is the payload bytes (abi.Event.Wire). */
/** `origin` is the step export's origin argument (ABI section 5.4); the binary recording of the Go count tool does not carry it, so a replay of one runs every event with none. */
export interface RecordedEvent { seq: number; kind: number; now: number; origin?: number; wire: Uint8Array; reads: RecordedRead[]; uuids: string[] }

export interface StepRecord {
  /** The event's ordinal in this session (0 is the open). */
  seq: number
  kind: number
  event: string
  now: number
  /** "ok", "rejected", "fatal" or "trap" (the core threw before it answered). */
  status: string
  /** Calls across the host and core boundary: the step, each pig.read, each pig.uuidv7. */
  crossings: number
  /** Statements the core read through pig.read, and the rows they returned. */
  reads: number
  /** Rows read and written in the store from this step's start to the next step's (the commits the host applied are this step's). */
  rowsRead: number
  rowsWritten: number
  statements: number
  commits: number
  bytesIn: number
  bytesOut: number
  /** Notices and effects the step returned. */
  notices: number
  effects: number
  /** Linear memory after the step. */
  linearBytes: number
  /** Bytes the core allocated and its allocations during the step (alloc_stats differences; no collection runs). */
  allocBytes?: number
  mallocs?: number
  /** The live heap after a collection, on the steps `liveEvery` selects. */
  liveBytes?: number
  /** Milliseconds inside the core's step (the host's wall clock, so a gauge, not a gate). */
  stepMs: number
  core: string
}

/** What a trap leaves behind: enough to replay the failing step. Everything is JSON; byte fields are base64. */
export interface TrapReport {
  error: { name: string; message: string }
  coreVersion: string
  abiId: string
  /** Events delivered before the trap, and whether the ring dropped the oldest of them. */
  totalEvents: number
  truncated: boolean
  /** The first event of the session (the open), which recovery feeds first. */
  opened?: ReportEvent
  /** The last events, oldest first; the last is the one that trapped. */
  events: ReportEvent[]
  steps: StepRecord[]
}
export interface ReportEvent { seq: number; kind: number; event: string; now: number; origin?: number; wire: string; reads: { request: string; answer: string }[]; uuids: string[] }

class Ring<T> {
  private items: T[] = []
  private head = 0
  total = 0
  readonly capacity: number
  constructor(capacity: number) { this.capacity = capacity }
  push(item: T) {
    this.total++
    if (this.items.length < this.capacity) this.items.push(item)
    else { this.items[this.head] = item; this.head = (this.head + 1) % this.capacity }
  }
  toArray(): T[] { return [...this.items.slice(this.head), ...this.items.slice(0, this.head)] }
  clear() { this.items = []; this.head = 0; this.total = 0 }
}

/** The counters of a store a step's record is the difference of. */
const snapshot = (c: StoreCounters | undefined) => ({ rowsRead: c?.rowsRead ?? 0, rowsWritten: c?.rowsWritten ?? 0, statements: c?.statements ?? 0, commits: c?.commits ?? 0 })

type Observable = CoreHandle & { tap?: StepTap; core?: { memoryBytes: number; abiId: string; allocStats?: () => { bytes: number; mallocs: number; gcs: number } } }

export class FlightRecorder {
  readonly options: Required<Omit<RecorderOptions, "coreVersion" | "onTrap">> & Pick<RecorderOptions, "coreVersion" | "onTrap">
  private readonly events: Ring<RecordedEvent>
  private readonly steps: Ring<StepRecord>
  private opened?: RecordedEvent
  private current?: RecordedEvent
  private pending?: { record: StepRecord; event: RecordedEvent; before: ReturnType<typeof snapshot> }
  private store?: Store
  private abiId = ABI_ID

  constructor(options: RecorderOptions = {}) {
    this.options = { events: options.events ?? 256, steps: options.steps ?? 1024, liveEvery: options.liveEvery ?? 0, coreVersion: options.coreVersion, onTrap: options.onTrap }
    this.events = new Ring(this.options.events)
    this.steps = new Ring(this.options.steps)
  }

  /** The core version the records carry. */
  get coreVersion(): string { return this.options.coreVersion ?? this.abiId }

  /** Begin the record of one session lifetime over `session`: forget the last one, and return the handle the host loop drives. */
  attach(session: Observable, store?: Store): CoreHandle {
    this.events.clear()
    this.steps.clear()
    this.opened = this.current = this.pending = undefined
    this.store = store
    if (session.core?.abiId) this.abiId = session.core.abiId
    session.tap = {
      uuid: text => { this.current?.uuids.push(text) },
      read: (request, answer) => { this.current?.reads.push({ request, answer }) },
    }
    return new ObservedCore(this, session)
  }

  /** @internal One event through the core: record its inputs, run it, record what it cost. */
  observe(session: Observable, kind: number, wire: Uint8Array, now: number, origin: number, run: () => Step): Step {
    this.finish()
    const event: RecordedEvent = { seq: this.events.total, kind, now, wire, reads: [], uuids: [] }
    if (origin !== 0) event.origin = origin
    this.events.push(event)
    this.opened ??= event
    this.current = event
    const record: StepRecord = {
      seq: event.seq, kind, event: eventName(kind), now, status: "ok", crossings: 1, reads: 0, rowsRead: 0, rowsWritten: 0, statements: 0, commits: 0,
      bytesIn: wire.length, bytesOut: 0, notices: 0, effects: 0, linearBytes: session.core?.memoryBytes ?? 0, stepMs: 0, core: this.coreVersion,
    }
    this.pending = { record, event, before: snapshot(this.store?.counters) }
    this.steps.push(record)
    const alloc0 = session.core?.allocStats?.()
    const t0 = performance.now()
    let step: Step
    try { step = run() } catch (error) {
      record.stepMs = performance.now() - t0
      record.status = "trap"
      this.finish()
      if (this.options.onTrap) {
        try { this.options.onTrap(this.report(error)) } catch { /* the trap is the outcome either way */ }
      }
      throw error
    }
    record.stepMs = performance.now() - t0
    record.bytesOut = step.bytes
    record.notices = step.notices.length
    record.effects = step.effects.length
    if (alloc0) { const alloc1 = session.core!.allocStats!(); record.allocBytes = alloc1.bytes - alloc0.bytes; record.mallocs = alloc1.mallocs - alloc0.mallocs }
    record.status = step.status === 0 ? "ok" : step.status === 1 ? "rejected" : "fatal"
    record.linearBytes = session.core?.memoryBytes ?? 0
    if (this.options.liveEvery > 0 && (event.seq + 1) % this.options.liveEvery === 0 && session.memStats) {
      record.liveBytes = (session.memStats() as { heap: number }).heap
    }
    return step
  }

  /** Close the open step record: the store work since it began (including the commits the host applied for it) is its own. */
  private finish() {
    const p = this.pending
    if (!p) return
    this.pending = undefined
    const now = snapshot(this.store?.counters), { record, event, before } = p
    record.rowsRead = now.rowsRead - before.rowsRead
    record.rowsWritten = now.rowsWritten - before.rowsWritten
    record.statements = now.statements - before.statements
    record.commits = now.commits - before.commits
    record.reads = event.reads.length
    record.crossings = 1 + event.reads.length + event.uuids.length
  }

  /** The step records, oldest first. */
  stepRecords(): StepRecord[] {
    this.finish()
    return this.steps.toArray()
  }

  /** The ring: the last events the core was given, oldest first. */
  recordedEvents(): RecordedEvent[] { return this.events.toArray() }

  /** The report of a trap: the error, the core's build, the ring and the last step records. */
  report(error: unknown): TrapReport {
    this.finish()
    const e = error as Error
    const events = this.events.toArray()
    const cause = (e as { cause2?: unknown })?.cause2 as Error | undefined
    return {
      error: { name: (cause ?? e)?.name ?? "Error", message: (cause ?? e)?.message ?? String(error) },
      coreVersion: this.coreVersion,
      abiId: this.abiId,
      totalEvents: this.events.total,
      truncated: this.events.total > events.length,
      opened: this.opened ? reportEvent(this.opened) : undefined,
      events: events.map(reportEvent),
      steps: this.steps.toArray(),
    }
  }

  /** The ring as a recording the Go count tool replays: `countgate replay --rec FILE` (durable/core/contracttest/counts). */
  recording(): Uint8Array {
    const events = this.recordedEvents()
    if (this.events.total > events.length) throw new Error(`the ring dropped ${this.events.total - events.length} of ${this.events.total} events: the core's state before the first is not in a recording; raise \`events\`, or replay the trap report against the store`)
    return encodeRecording(events, this.abiId)
  }
}

class ObservedCore implements CoreHandle {
  private readonly recorder: FlightRecorder
  private readonly session: Observable
  constructor(recorder: FlightRecorder, session: Observable) { this.recorder = recorder; this.session = session }
  step(kind: number, payload: Uint8Array | undefined, now: number, origin = 0) {
    return this.recorder.observe(this.session, kind, payload ? payload.slice() : new Uint8Array(0), now, origin, () => this.session.step(kind, payload, now, origin))
  }
  rows(answers: Rows[], now: number) {
    let wire!: Uint8Array
    encodeRows(answers, n => (wire = new Uint8Array(n)))
    return this.recorder.observe(this.session, EVENT.rows, wire, now, 0, () => this.session.rows(answers, now))
  }
  free() { this.session.free() }
  stats() { return this.session.stats?.() as { steps: number; msStep: number; msDecode: number } }
  memStats() { return this.session.memStats?.() as object }
  get reader() { return this.session.reader }
  set reader(read) { this.session.reader = read }
}

const b64 = (bytes: Uint8Array) => {
  let s = ""
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  return btoa(s)
}
const unb64 = (text: string) => Uint8Array.from(atob(text), c => c.charCodeAt(0))

const reportEvent = (e: RecordedEvent): ReportEvent => ({
  seq: e.seq, kind: e.kind, event: eventName(e.kind), now: e.now, ...(e.origin === undefined ? {} : { origin: e.origin }), wire: b64(e.wire),
  reads: e.reads.map(r => ({ request: b64(r.request), answer: b64(r.answer) })), uuids: e.uuids,
})
export const recordedEvent = (e: ReportEvent): RecordedEvent => ({
  seq: e.seq, kind: e.kind, now: e.now, ...(e.origin === undefined ? {} : { origin: e.origin }), wire: unb64(e.wire), reads: e.reads.map(r => ({ request: unb64(r.request), answer: unb64(r.answer) })), uuids: e.uuids,
})

// The recording format of durable/core/contracttest/counts (record.go): items of u8 kind, u32 length, body, little endian.
const ITEM = { abi: 1, event: 2, read: 3, uuid: 4, step: 5, mark: 6, rowsChanged: 7 } as const
const enc = new TextEncoder()
const dec = new TextDecoder()

/** Events as a counts recording: the abi_id, then each event followed by its reads and uuids. A recorder keeps no step digests, so a replay of it checks only that the core runs. */
export function encodeRecording(events: RecordedEvent[], abiId: string): Uint8Array {
  const chunks: Uint8Array[] = []
  const item = (kind: number, ...parts: Uint8Array[]) => {
    const head = new Uint8Array(5)
    head[0] = kind
    new DataView(head.buffer).setUint32(1, parts.reduce((n, p) => n + p.length, 0), true)
    chunks.push(head, ...parts)
  }
  const u32 = (n: number) => { const b = new Uint8Array(4); new DataView(b.buffer).setUint32(0, n, true); return b }
  item(ITEM.abi, enc.encode(abiId))
  for (const e of events) {
    const head = new Uint8Array(9)
    head[0] = e.kind
    new DataView(head.buffer).setFloat64(1, e.now, true)
    item(ITEM.event, head, e.wire)
    for (const r of e.reads) item(ITEM.read, u32(r.request.length), r.request, r.answer)
    for (const u of e.uuids) item(ITEM.uuid, enc.encode(u))
  }
  const out = new Uint8Array(chunks.reduce((n, c) => n + c.length, 0))
  let at = 0
  for (const c of chunks) { out.set(c, at); at += c.length }
  return out
}

/** The inverse of encodeRecording for a recording of events and their inputs (step digests and marks are skipped). */
export function decodeRecording(input: Uint8Array): { abiId: string; events: RecordedEvent[] } {
  const bytes = new Uint8Array(input.buffer, input.byteOffset, input.byteLength) // plain views: a Node Buffer's slices are Buffers
  const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const events: RecordedEvent[] = []
  let abiId = ""
  for (let i = 0; i < bytes.length;) {
    const kind = bytes[i]!, n = dv.getUint32(i + 1, true)
    const body = bytes.subarray(i + 5, i + 5 + n)
    i += 5 + n
    const last = events.at(-1)
    switch (kind) {
      case ITEM.abi: abiId = dec.decode(body); break
      case ITEM.event: events.push({ seq: events.length, kind: body[0]!, now: new DataView(body.buffer, body.byteOffset).getFloat64(1, true), wire: body.slice(9), reads: [], uuids: [] }); break
      case ITEM.read: {
        const rl = new DataView(body.buffer, body.byteOffset).getUint32(0, true)
        last!.reads.push({ request: body.slice(4, 4 + rl), answer: body.slice(4 + rl) })
        break
      }
      case ITEM.uuid: last!.uuids.push(dec.decode(body)); break
      case ITEM.step: case ITEM.mark: case ITEM.rowsChanged: break
      default: throw new Error(`recording: unknown item kind ${kind}`)
    }
  }
  return { abiId, events }
}

export interface ReplayOutcome {
  /** The steps the core returned, in order, up to the trap if there was one. */
  steps: Step[]
  /** The core's trap, when an event made it throw; `at` is the index of that event in `events`. */
  trap?: Error
  at?: number
}

const sameParams = (a: Param[], b: Param[]) => a.length === b.length && a.every((v, i) => v instanceof Uint8Array && b[i] instanceof Uint8Array ? v.length === (b[i] as Uint8Array).length && v.every((x, j) => x === (b[i] as Uint8Array)[j]) : v === b[i])

/**
 * Replay recorded events through a fresh core: each event, with the pig.read answers and pig.uuidv7 values recorded for it
 * served in order. A read or uuid the recorded run did not make is a divergence and throws, as is a read that asks something
 * else. `read` answers a read the recording has no answer for (the trap replay's store), and `uuid` a uuid it has none for;
 * without them that is a divergence too. An event that takes fewer reads or uuids than the recording holds is one as well.
 * `recorder` observes the replay like any session: its records and ring describe it.
 */
export function replayEvents(module: WebAssembly.Module, events: RecordedEvent[], options: { read?: (sql: number, params: Param[]) => Uint8Array; uuid?: () => string; recorder?: FlightRecorder } = {}): ReplayOutcome {
  let at = 0, readAt = 0, uuidAt = 0
  // A divergence is raised inside a pig import, where WasmSession turns every exception into a CoreTrap: it is kept here so that
  // the replay reports it as a divergence and never as a trap of the core.
  let diverged: Error | undefined
  const diverge = (message: string): never => { throw (diverged = new Error(message)) }
  const core = new WasmCore(module, {
    log: () => {},
    uuidv7: () => {
      const id = events[at]?.uuids[uuidAt++] ?? options.uuid?.()
      return id ?? diverge(`replay: the core took a uuid the recorded run did not take, in event ${at}`)
    },
  })
  const raw = core.newSession()
  const session = options.recorder ? options.recorder.attach(raw) : raw
  session.reader = (sql, params) => {
    const recorded = events[at]?.reads[readAt]
    if (!recorded) {
      if (options.read) return options.read(sql, params)
      return diverge(`replay: the core made a pig.read the recorded run did not make, in event ${at}`)
    }
    readAt++
    const want = decodeReadRequest(recorded.request, 0, recorded.request.length)
    if (want.sql !== sql || !sameParams(want.params, params)) diverge(`replay: the core asked a different pig.read than the recorded run, in event ${at}`)
    return recorded.answer
  }
  const steps: Step[] = []
  try {
    for (; at < events.length; at++) {
      readAt = uuidAt = 0
      steps.push(session.step(events[at]!.kind, events[at]!.wire, events[at]!.now, events[at]!.origin ?? 0))
      const want = events[at]!
      if (readAt < want.reads.length || uuidAt < want.uuids.length) throw new Error(`replay: event ${at} took ${readAt} reads and ${uuidAt} uuids, the recorded run ${want.reads.length} and ${want.uuids.length}`)
    }
  } catch (error) {
    if (diverged) throw diverged
    if (error instanceof CoreTrap) return { steps, trap: error, at }
    throw error
  } finally { session.free() }
  return { steps }
}

/**
 * Replay a trap report. A report whose ring holds the whole session replays as recorded. A truncated one cannot: the state
 * before its first event is gone from the ring, and lives in SQLite. It replays the way recovery does (ADR D16): a fresh core
 * opened over `store` with the first event of the session, then the failing event once, every read answered by the store.
 */
export function replayReport(module: WebAssembly.Module, report: TrapReport, store?: Store, sqlTable?: string[]): ReplayOutcome {
  const events = report.events.map(recordedEvent)
  if (!report.truncated) return replayEvents(module, events)
  if (!store || !report.opened) throw new Error("replayReport: the ring dropped the start of the session; give the store the object had when it trapped")
  const table = sqlTable ?? new WasmCore(module, { log: () => {} }).sqlTable
  const read = (sql: number, params: Param[]) => encodeAnswer(store.all(table[sql]!, params))
  // The store answers every read; the recorded uuids of the open are kept, and the failing event draws fresh ones.
  const open = { ...recordedEvent(report.opened), reads: [] }
  const failing = { ...events[events.length - 1]!, reads: [], uuids: [] }
  const out = replayEvents(module, [open, failing], { read, uuid: () => uuidv7() })
  return out.trap && out.at === 1 ? { ...out, at: events.length - 1 } : out
}
