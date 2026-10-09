// The ABI 1 host loop (ABI section 3). One HostSession drives one core session over one Store:
//   1. the core is never called re-entrantly (events queue while a step is applied);
//   2. each commit of a step is one transaction, applied in order; a failed commit or a failed single-writer guard is fatal;
//   3. a step with reads is answered by one rows event before any other event is delivered;
//   4. notices are delivered after the step's commits are applied;
//   5. effects start only after the step's commits are applied;
//   6. effect IDs are unique for the life of the handle; discarding the handle cancels its effects and drops late completions.
import { EFFECT, EVENT, NOTICE, STATUS, effectName } from "./abi.ts"
import type { Store } from "./store.ts"
import { payloadBytes, type Commit, type Effect, type Notice, type Param, type Rows, type Step } from "./wire.ts"

/** What the host loop needs from a core session: the Wasm binding, the TypeScript control, or a native bridge. */
export interface CoreHandle {
  step(kind: number, payload: Uint8Array | undefined, now: number): Step
  rows(answers: Rows[], now: number): Step
  free(): void
  /** Optional measurements: calls into the core and the time spent in them and in decoding their results. */
  stats?(): { steps: number; msStep: number; msDecode: number }
  /** Optional: the core's mem_stats counters. */
  memStats?(): object
}

export class RejectedError extends Error {
  readonly piName: string
  constructor(name: string | undefined, message: string | undefined, raw: string) {
    super(message ?? raw)
    this.name = name ?? "Rejected"
    this.piName = this.name
  }
}
export class FatalError extends Error {
  readonly cause2: unknown
  constructor(message: string, cause?: unknown) { super(message); this.name = "FatalError"; this.cause2 = cause }
}

export interface EffectContext {
  host: HostSession
  signal: AbortSignal
}
export type EffectHandler = (effect: Effect, ctx: EffectContext) => void | Promise<void>

/** Timer and liveness effects (ABI section 6, effects 1, 2 and 10). */
export interface TimerDriver {
  set(timerId: number, at: number, durable: boolean): void
  clear(timerId: number): void
  /** The watchdog: an `at` time, or -1 when nothing is live. */
  liveness(at: number): void
}

export interface HostCounters {
  events: number
  steps: number
  commits: number
  statements: number
  reads: number
  notices: number
  effects: Record<string, number>
  rejected: number
  msApply: number
  msReads: number
  msEffects: number
}
const zeroCounters = (): HostCounters => ({ events: 0, steps: 0, commits: 0, statements: 0, reads: 0, notices: 0, effects: {}, rejected: 0, msApply: 0, msReads: 0, msEffects: 0 })

/** Receives the rows the host serves and the statements it applies; lane L3 keeps record bytes this way (ABI section 10.1). */
export interface HistoryObserver {
  rows(sql: string, rows: unknown[][]): void
  statement(sql: string, params: Param[]): void
}

export interface TraceSink {
  commit?(commit: Commit, sqlTable: string[]): void
  event?(kind: number, payload: Uint8Array | undefined, now: number): void
}

export interface HostOptions {
  core: CoreHandle
  sqlTable: string[]
  store: Store
  clock?: () => number
  handlers?: Partial<Record<number, EffectHandler>>
  timers?: TimerDriver
  onNotice?: (notice: Notice) => void
  history?: HistoryObserver
  trace?: TraceSink
  /** Called when the handle is discarded because of a fatal step, a failed commit, a failed guard or a trap. */
  onFatal?: (error: FatalError) => void
  /** Called for a rejected step that no direct caller is waiting on (an effect completion the core refused). */
  onRejected?: (error: RejectedError, kind: number) => void
}

const GUARD = /^UPDATE durable_metadata\b/

/** The IDs a handle has issued: a bitmap, because a Session issues millions of dense u32 IDs and a Set of them costs 20 times more. */
export class IdSet {
  private bits = new Uint32Array(1024)
  private readonly sparse = new Set<number>()
  /** Returns false when `id` was already present. */
  add(id: number): boolean {
    if (id >= 0x4000000) { if (this.sparse.has(id)) return false; this.sparse.add(id); return true }
    const word = id >>> 5, bit = 1 << (id & 31)
    if (word >= this.bits.length) { const grown = new Uint32Array(Math.max(this.bits.length * 2, word + 1)); grown.set(this.bits); this.bits = grown }
    if (this.bits[word]! & bit) return false
    this.bits[word]! |= bit
    return true
  }
}

type Queued = { kind: number; payload?: Uint8Array; rows?: Rows[]; now: number; result?: (step: Step) => void }

export class HostSession {
  readonly counters: HostCounters = zeroCounters()
  readonly store: Store
  readonly core: CoreHandle
  readonly sqlTable: string[]
  private readonly opts: HostOptions
  private readonly clock: () => number
  private readonly queue: Queued[] = []
  private readonly running = new Map<number, AbortController>()
  private readonly seen = new IdSet()
  private pumping = false
  private dead?: FatalError
  private readonly waiters = new Set<{ test: (n: Notice) => boolean; resolve: (n: Notice) => void; reject: (e: Error) => void }>()
  private readonly listeners = new Set<(n: Notice) => void>()

  constructor(opts: HostOptions) {
    this.opts = opts
    this.core = opts.core
    this.store = opts.store
    this.sqlTable = opts.sqlTable
    this.clock = opts.clock ?? (() => Date.now())
  }

  get discarded(): FatalError | undefined { return this.dead }
  get inflight(): number { return this.running.size }
  now(): number { return this.clock() }

  /** Queue an event and run the loop until no event is waiting. Returns the step the event produced when it ran in this call. */
  send(kind: number, payload?: Uint8Array): Step | undefined {
    let produced: Step | undefined
    this.enqueue({ kind, payload, now: this.clock(), result: s => { produced = s } })
    this.pump()
    return produced
  }

  /** Deliver a JSON event (open, submit, abort, registry, inspect). */
  sendJSON(kind: number, value: unknown): Step | undefined {
    return this.send(kind, new TextEncoder().encode(JSON.stringify(value)))
  }

  /** Resolve with the first notice that satisfies `test`; reject when the handle is discarded first. */
  waitForNotice(test: (n: Notice) => boolean): Promise<Notice> {
    if (this.dead) return Promise.reject(this.dead)
    return new Promise((resolve, reject) => this.waiters.add({ test, resolve, reject }))
  }

  private enqueue(q: Queued) {
    if (this.dead) throw this.dead
    this.queue.push(q)
  }

  private pump() {
    if (this.pumping) return
    this.pumping = true
    try {
      while (this.queue.length && !this.dead) {
        const q = this.queue.shift()!
        this.deliver(q)
      }
    } finally { this.pumping = false }
    if (this.dead) throw this.dead
  }

  private deliver(q: Queued) {
    this.counters.events++
    this.opts.trace?.event?.(q.kind, q.payload, q.now)
    let step: Step
    try { step = q.rows ? this.core.rows(q.rows, q.now) : this.core.step(q.kind, q.payload, q.now) } catch (e) {
      return this.discard(new FatalError(`the core failed on a ${q.kind} event: ${(e as Error).message}`, e))
    }
    q.result?.(step)
    this.apply(step, q)
  }

  private apply(step: Step, q: Queued): void {
    this.counters.steps++
    if (step.status === STATUS.rejected) {
      this.counters.rejected++
      const error = new RejectedError(step.error?.name, step.error?.message, step.error?.raw ?? "")
      if (q.result) q.result(step)
      else this.opts.onRejected?.(error, q.kind)
      return
    }
    if (step.status === STATUS.fatal) return this.discard(new FatalError(`the core reported a fatal step: ${step.error?.raw ?? ""}`))
    if (step.reads.length) {
      if (step.commits.length || step.effects.length) return this.discard(new FatalError("a step with reads must have no commits and no effects"))
      let answers: Rows[]
      const t0 = performance.now()
      try {
        answers = step.reads.map(r => {
          const rows = this.store.all(this.sqlTable[r.sql]!, r.params)
          this.opts.history?.rows(this.sqlTable[r.sql]!, rows)
          return { id: r.id, cols: rows[0]?.length ?? 0, rows }
        })
      } catch (e) { return this.discard(new FatalError(`a read failed: ${(e as Error).message}`, e)) }
      this.counters.reads += step.reads.length
      this.counters.msReads += performance.now() - t0
      let next: Step
      try { next = this.core.rows(answers, this.clock()) } catch (e) { return this.discard(new FatalError(`the core failed on a rows event: ${(e as Error).message}`, e)) }
      this.counters.events++
      this.opts.trace?.event?.(EVENT.rows, undefined, this.clock())
      return this.apply(next, { kind: EVENT.rows, now: q.now, result: q.result })
    }
    const t0 = performance.now()
    for (const commit of step.commits) {
      try { this.commit(commit) } catch (e) { return this.discard(new FatalError(`a commit failed: ${(e as Error).message}`, e)) }
    }
    this.counters.msApply += performance.now() - t0
    for (const notice of step.notices) this.notify(notice)
    const t1 = performance.now()
    for (const effect of step.effects) {
      if (this.dead) return
      this.start(effect)
    }
    this.counters.msEffects += performance.now() - t1
  }

  private commit(commit: Commit) {
    this.opts.trace?.commit?.(commit, this.sqlTable)
    this.store.tx(() => {
      for (const stmt of commit.stmts) {
        const text = this.sqlTable[stmt.sql]
        if (text === undefined) throw new Error(`statement ${stmt.sql} is not in the core's sql_table`)
        const written = this.store.run(text, stmt.params)
        if (GUARD.test(text) && written !== 1) throw new Error(`single-writer guard: the durable_metadata update changed ${written} rows, not 1`)
        this.opts.history?.statement(text, stmt.params)
      }
    })
    this.counters.commits++
    this.counters.statements += commit.stmts.length
  }

  /** Observe every notice after the step's commits are applied (ABI section 3, invariant 4). Returns the unsubscribe function. */
  onNotice(listener: (n: Notice) => void): () => void {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  private notify(notice: Notice) {
    this.counters.notices++
    this.opts.onNotice?.(notice)
    for (const l of [...this.listeners]) l(notice)
    for (const w of [...this.waiters]) if (w.test(notice)) { this.waiters.delete(w); w.resolve(notice) }
  }

  private start(effect: Effect) {
    if (!this.seen.add(effect.id)) return this.discard(new FatalError(`effect ID ${effect.id} was issued twice`))
    const name = effectName(effect.kind)
    this.counters.effects[name] = (this.counters.effects[name] ?? 0) + 1
    const timers = this.opts.timers
    switch (effect.kind) {
      case EFFECT.timer: {
        const dv = new DataView(effect.payload.buffer, effect.payload.byteOffset, effect.payload.byteLength)
        timers?.set(dv.getUint32(0, true), dv.getFloat64(4, true), dv.getUint8(12) === 1)
        return
      }
      case EFFECT.timer_clear: return timers?.clear(new DataView(effect.payload.buffer, effect.payload.byteOffset, 4).getUint32(0, true))
      case EFFECT.liveness: return timers?.liveness(new DataView(effect.payload.buffer, effect.payload.byteOffset, 8).getFloat64(0, true))
      case EFFECT.cancel: return this.running.get(new DataView(effect.payload.buffer, effect.payload.byteOffset, 4).getUint32(0, true))?.abort()
    }
    const handler = this.opts.handlers?.[effect.kind]
    if (!handler) return this.discard(new FatalError(`no handler for a ${name} effect`))
    const controller = new AbortController()
    this.running.set(effect.id, controller)
    const finish = () => this.running.delete(effect.id)
    try {
      const result = handler(effect, { host: this, signal: controller.signal })
      if (result) result.then(finish, error => { finish(); this.discardOnHandlerError(effect, error) })
      else finish()
    } catch (error) { finish(); this.discardOnHandlerError(effect, error) }
  }

  private discardOnHandlerError(effect: Effect, error: unknown) {
    if (this.dead || (error as Error)?.name === "AbortError") return
    this.discard(new FatalError(`the ${effectName(effect.kind)} handler for effect ${effect.id} failed: ${(error as Error)?.message ?? error}`, error))
  }

  /** Discard the handle (ABI section 4.6 and ADR D16): cancel its effects, drop late completions, free the core session. */
  discard(error: FatalError) {
    if (this.dead) return
    this.dead = error
    for (const c of this.running.values()) c.abort()
    this.running.clear()
    this.queue.length = 0
    try { this.core.free() } catch { /* a trapped instance is discarded by its owner */ }
    for (const w of this.waiters) w.reject(error)
    this.waiters.clear()
    this.opts.onFatal?.(error)
  }

  /** Completion events. A completion that arrives after the handle was discarded is dropped. */
  modelEvents(effectId: number, events: unknown[]) {
    if (this.dead) return
    this.send(EVENT.model_event, payloadBytes([["u32", effectId]], JSON.stringify(events)))
  }
  /** model_event whose JSON array is already serialized. */
  modelEventsRaw(effectId: number, json: string) {
    if (this.dead) return
    this.send(EVENT.model_event, payloadBytes([["u32", effectId]], json))
  }
  toolDone(effectId: number, outcome: 0 | 1, value: unknown) {
    if (this.dead) return
    this.send(EVENT.tool_done, payloadBytes([["u32", effectId], ["u8", outcome]], JSON.stringify(value)))
  }
  /** waitId is nonzero only for a details update (kind 1), which the core answers with a progress_ack notice. */
  toolProgress(effectId: number, kind: number, skipped: number, bytes: Uint8Array | string, waitId = 0) {
    if (this.dead) return
    this.send(EVENT.tool_progress, payloadBytes([["u32", effectId], ["u8", kind], ["u32", skipped], ["u32", waitId]], bytes))
  }
  /** Completes a hook, env, section, deferred or callback effect. */
  hookDone(effectId: number, outcome: 0 | 1, value: unknown) {
    if (this.dead) return
    this.send(EVENT.hook_done, payloadBytes([["u32", effectId], ["u8", outcome]], JSON.stringify(value ?? null)))
  }
  /** Completes a custom task's phase or abort handler. */
  phaseDone(effectId: number, outcome: 0 | 1, value: unknown) {
    if (this.dead) return
    this.send(EVENT.phase_done, payloadBytes([["u32", effectId], ["u8", outcome]], JSON.stringify(value ?? null)))
  }
  /** One `tx` event of the extension transaction channel (ABI section 8). */
  tx(txId: number, op: unknown) {
    if (this.dead) return
    this.send(EVENT.tx, payloadBytes([["u32", txId]], JSON.stringify(op)))
  }
  /** One `api` request (ABI section 8.1); the answer arrives as an api_result notice. */
  api(requestId: number, request: unknown) {
    if (this.dead) return
    this.send(EVENT.api, payloadBytes([["u32", requestId]], JSON.stringify(request)))
  }
  /** Wire-plane bytes of a model_http effect: phase 0 response head JSON, 1 body chunk, 2 end, 3 transport error JSON. */
  modelBytes(effectId: number, phase: 0 | 1 | 2 | 3, bytes: Uint8Array | string) {
    if (this.dead) return
    this.send(EVENT.model_bytes, payloadBytes([["u32", effectId], ["u8", phase]], bytes))
  }
  timerFired(timerId: number) {
    if (this.dead) return
    this.send(EVENT.timer, payloadBytes([["u32", timerId]]))
  }

  /** Seal admission (the close event), cancel the effects, free the core handle. */
  close() {
    if (this.dead) return
    try { this.send(EVENT.close) } finally { this.discard(new FatalError("closed")) }
  }
}

export const isPublishedSettlement = (requestId: string) => (n: Notice) => {
  if (n.kind !== NOTICE.published) return false
  const text = new TextDecoder().decode(n.payload)
  if (!text.includes(requestId)) return false
  try {
    return (JSON.parse(text).submissions ?? []).some((s: { requestId: string; status: string }) => s.requestId === requestId && s.status !== "queued" && s.status !== "placed")
  } catch { return false }
}
