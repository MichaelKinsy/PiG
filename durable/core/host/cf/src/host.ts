// The ABI 1 host loop (ABI section 3). One HostSession drives one core session over one Store:
//   1. the core is never called re-entrantly (events queue while a step is applied);
//   2. each commit of a step is one transaction, applied in order; a failed commit or a failed single-writer guard is fatal;
//   3. a step with reads is answered by one rows event before any other event is delivered;
//   4. notices are delivered after the step's commits are applied;
//   5. effects start only after the step's commits are applied;
//   6. effect IDs are unique for the life of the handle; discarding the handle cancels its effects and drops late completions.
import { EFFECT, EVENT, NOTICE, STATUS, effectName, noticeName } from "./abi.ts"
import { captureContext, type ContextRun } from "./async-context.ts"
import { answerContextRead } from "./contextreads.ts"
import { ContextReader } from "./contextstore.ts"
import { ContextSegments } from "./contextsegments.ts"
import type { Store } from "./store.ts"
import { CoreTrap } from "./wasm.ts"
import { decodeHops, encodeAnswer, payloadBytes, u32Payload, type Commit, type Effect, type Notice, type Param, type Rows, type Step } from "./wire.ts"

/** What the host loop needs from a core session: the Wasm binding, the TypeScript control, or a native bridge. */
export interface CoreHandle {
  /** `origin` tags the host's async context of the call (ABI section 5.4); 0 for none. */
  step(kind: number, payload: Uint8Array | undefined, now: number, origin?: number): Step
  rows(answers: Rows[], now: number): Step
  free(): void
  /** Optional measurements: calls into the core and the time spent in them and in decoding their results. */
  stats?(): { steps: number; msStep: number; msDecode: number }
  /** Optional: the core's mem_stats counters. */
  memStats?(): object
  /** Set by the host loop: answers the core's synchronous reads (ABI section 4.2, pig.read) with the encoded answer. */
  reader?: (sql: number, params: Param[]) => Uint8Array
  /**
   * Optional fast path for a `hopped` event (ABI section 4.1, `hop`): the event is its effect ID, and the answer is an integer
   * when the step has nothing the host must decode. 0: the step is empty. 1: its only output is one plain hops chain, named by
   * `hopId` and `hopTicks`. A Step: anything else. The host falls back to `step` when a core has no `hop`.
   */
  hop?(effectId: number, now: number): Step | 0 | 1
  hopId?: number
  hopTicks?: number
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
  /** The event the failing step was running (a trap in a step that read rows names the event that began it): a client request may be fed again to a new instance. */
  event?: ReplayableEvent
  /** The last events the handle delivered, oldest first: attached to every fatal for a local replay (the clock is an input of each). */
  recent?: readonly RecentEvent[]
  /** The owner reopened a new session and fed the event again: the caller of the failed send sees that step's answer, not this error. */
  recovered?: boolean
  constructor(message: string, cause?: unknown) { super(message); this.name = "FatalError"; this.cause2 = cause }
}

/** An event the host can deliver to a new session of the same store. */
export interface ReplayableEvent {
  readonly kind: number
  readonly payload?: Uint8Array
  readonly now: number
  /** The `requestId` a client call waits on. */
  readonly request?: number
  /** Receives the step the event produced: the original caller's `send` returns it. */
  readonly result?: (step: Step) => void
  /** Already fed to a second session. */
  readonly retried?: boolean
  /** The async context of the call that made the event: the new session runs the event's work in it. */
  readonly context?: ContextRun
}

/** One delivered event as a trap report records it. */
export interface RecentEvent { readonly kind: number; readonly bytes: number; readonly now: number; readonly request?: number }

/** Events a handle remembers for its trap reports. */
const RECENT_EVENTS = 32
/** What an operation fails with when the core trapped under it and the host recycled the instance: nothing of the operation was committed, and the same call may be made again. */
export class RecycledError extends Error {
  readonly cause2: unknown
  constructor(message: string, cause?: unknown) { super(message); this.name = "RecycledError"; this.cause2 = cause }
}

/**
 * What an operation fails with when the host replaced the core handle after the old core had run the operation's event and before it
 * answered, and running that event again could apply it twice (a submission without a request ID, the withdrawal of a submission). The
 * operation may or may not have been applied: read the store before making the call again. It is not a RecycledError, so a caller that
 * repeats a RecycledError does not repeat this.
 */
export class RecycledUncertainError extends Error {
  readonly cause2: unknown
  constructor(message: string, cause?: unknown) { super(message); this.name = "RecycledUncertainError"; this.cause2 = cause }
}

/** The part of a HostSession an effect handler completes its effect through. It belongs to one handle: once the host has recycled that handle the effect no longer exists, and a late completion is dropped instead of reaching the new handle, whose effect IDs are numbered afresh. */
export type EffectHost = Pick<HostSession, "modelEvents" | "modelEventsRaw" | "modelBytes" | "toolProgress" | "toolDone" | "hookDone" | "phaseDone" | "now" | "contexts">

export interface EffectContext {
  host: EffectHost
  signal: AbortSignal
}
export type EffectHandler = (effect: Effect, ctx: EffectContext) => void | Promise<void>

/** Timer and liveness effects (ABI section 6, effects 1, 2 and 10). */
export interface TimerDriver {
  /** `timerClass`: 0 volatile (a throttle window), 1 durable (a sleeping task: keep the object alarm), 2 background (memory only: a host may decline it). */
  set(timerId: number, at: number, timerClass: number): void
  clear(timerId: number): void
  /** The watchdog: an `at` time, or -1 when nothing is live. */
  liveness(at: number): void
  /** The handle was recycled: forget every timer, the reopened core arms its own from the store. */
  reset?(): void
}

/**
 * How a HostSession replaces its core handle (ADR D16): after a trap, and at a quiet point when the owner recycles an instance.
 * The owner holds the lease on the pooled instance; the host never sees the pool.
 */
export interface Renewal {
  /** Give the handle up: free it and return its lease. `trap` is set when the instance trapped: the instance is discarded with every session on it. Safe to call twice. */
  drop(trap?: Error): void
  /** A fresh handle on a healthy instance, with the payload of the `open` event that cold-opens it. */
  acquire(): { core: CoreHandle; sqlTable: string[]; open: unknown }
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
  /** Hops chains started, the microtask hops they asked for, and the `hopped` events the core answered with an integer. */
  hopChains: number
  hopTicks: number
  hopsThin: number
  /** Core traps the host caught, and handles it replaced (a trap, or the owner recycling at a quiet point). */
  traps: number
  recycles: number
  msApply: number
  msReads: number
  msEffects: number
}
const zeroCounters = (): HostCounters => ({ events: 0, steps: 0, commits: 0, statements: 0, reads: 0, notices: 0, effects: {}, rejected: 0, traps: 0, recycles: 0, hopChains: 0, hopTicks: 0, hopsThin: 0, msApply: 0, msReads: 0, msEffects: 0 })

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
  /** A cancel effect names effect `id`. The core also cancels the latest phase effect of a custom task invocation when the invocation ends, even after that effect settled. */
  onCancel?: (id: number) => void
  onNotice?: (notice: Notice) => void
  history?: HistoryObserver
  trace?: TraceSink
  /** Called when the handle is discarded because of a fatal step, a failed commit, a failed guard or a trap. */
  onFatal?: (error: FatalError) => void
  /** How to replace the handle after a trap (and at a quiet point). Without it a trap discards the host as a fatal step does. */
  renew?: Renewal
  /** Called each time the host has no event queued and nothing running, after the event that made it so. The owner's chance to recycle an instance at a quiet point. */
  onQuiet?: () => void
  /** Called for a rejected step that no direct caller is waiting on (an effect completion the core refused). */
  onRejected?: (error: RejectedError, kind: number) => void
  /** The core's current settings payload (Pi reads settings through getters at every decision); sent as a `registry` event with `{settings}` before the next event whenever it changed. */
  settings?: () => unknown
}

const GUARD = /^UPDATE durable_metadata\b/

// A hop is one reaction on this settled promise: a single microtask, as queueMicrotask's, in the same V8 queue. workerd's
// queueMicrotask keeps every callback it is given past the young-generation collections (measured: 30 MB of promoted
// allocation per 4 objects x 4 turns at 3,500 turns, the largest part of the isolate's peak), and a reaction does not.
const RESOLVED = Promise.resolve()

/**
 * One open hops chain (ABI section 5.3). Its state lives in these fields and `run` is bound once, so a hop queues the same
 * function again and allocates nothing; a finished chain returns to the host's free list.
 */
class Chain {
  id = 0
  n = 0
  left = 0
  at = 0
  commits: Commit[] | undefined
  aborted = false
  readonly run: () => void
  constructor(host: HostSession) { this.run = () => host.hop(this) }
}

/** Told that the host replaced its handle (`HostSession.onRecycled`). */
export type RecycleListener = (cause: Error | undefined, undelivered: ReadonlySet<Uint8Array>) => void

type Queued = { kind: number; payload?: Uint8Array; rows?: Rows[]; now: number; result?: (step: Step) => void
  /** The async context of the host call that made the event (ABI section 5.4), for the events a caller makes. The origin the core is told is numbered when the event is delivered. */
  context?: ContextRun
  /** The `requestId` a client call waits on: the event may be fed again after a trap. */
  request?: number
  /** Sent by an owner that replays it when the handle is replaced (a client request, a transaction event): the host does not refeed it. */
  owned?: boolean
  /** Ends the recovery window: queued behind the refeed of a trapped event. */
  sentinel?: boolean
  /** The event that began the step chain this rows event continues. */
  root?: Queued
  /** Fed again after a trap: a second trap on it is final. */
  retried?: boolean
}

/** Events that name something the replaced handle owned: an effect it issued, a timer it set, a hops chain, a transaction. The reopened core re-derives that work from the store and numbers it afresh, so these are dropped. */
/** The events a host caller makes: the core runs their work in the context of the call. The other events complete work the core started, and continue its context. */
const CALLER_EVENTS: ReadonlySet<number> = new Set([EVENT.open, EVENT.submit, EVENT.abort, EVENT.registry, EVENT.api, EVENT.inspect, EVENT.resume, EVENT.seal, EVENT.close, EVENT.tx, EVENT.timer])

const handleBound = (kind: number) => kind === EVENT.rows || kind === EVENT.model_event || kind === EVENT.model_bytes || kind === EVENT.tool_progress || kind === EVENT.tool_done || kind === EVENT.hook_done || kind === EVENT.phase_done || kind === EVENT.timer || kind === EVENT.hopped || kind === EVENT.tx


export class HostSession {
  readonly counters: HostCounters = zeroCounters()
  readonly store: Store
  /** The contexts the core names in model requests and context reads (ABI sections 6 and 8.1), derived from this store. */
  readonly contexts: ContextReader
  /** The handle the host drives. A trap or a recycle replaces it (`epoch` counts the replacements). */
  core: CoreHandle
  sqlTable: string[]
  /** Handles this host has had: effect IDs are unique within one epoch only. */
  epoch = 0
  /** `counters.commits` when the host last dropped a handle: the commits the old handle's work applied end here, and the reopened core's own `open` may commit after it. */
  commitsAtDrop = 0
  private readonly opts: HostOptions
  private readonly clock: () => number
  private readonly queue: Queued[] = []
  /** The async contexts the core may still name (ABI section 5.4), by origin: numbered as events reach the core, dropped when it says it holds no work for one. */
  private readonly origins = new Map<number, ContextRun>()
  private nextOrigin = 1
  /** The context of the first `open`: a reopened core resumes the work of the handle it replaces in it, as Harness.open's turns run in open's. */
  private openContext?: ContextRun
  /** The contexts of the owned events, by payload: a request fed again to a new session keeps its caller's. */
  private readonly ownedContexts = new WeakMap<Uint8Array, ContextRun>()
  private readonly running = new Map<number, AbortController>()
  /** Open hops chains: cancelling one sets its flag, the chain stops at its next hop (no AbortController per chain). */
  private readonly chains = new Map<number, Chain>()
  private readonly freeChains: Chain[] = []
  /** Runs before every event reaches the core: the host's chance to hand the core state that changed outside the event stream (live Harness settings). */
  beforeSend?: () => void
  /** Effect handlers that have not settled: `closeAndJoin` waits for them. */
  private readonly handlers = new Set<Promise<unknown>>()
  /** The ID of the last effect started: the core issues effect IDs in increasing order for the life of a handle (ABI section 3, invariant 6), so one number detects a reissued ID where a set of every ID grew with the handle's life. */
  private lastEffect = -1
  private pumping = false
  private readonly recent: RecentEvent[] = []
  private dead?: FatalError
  /** The handle must be replaced before the next event: its core trapped (`cause`, with the event it trapped on as `root`), or the owner recycled it at a quiet point. */
  private reopening?: { cause?: Error; root?: Queued }
  /** From the start of a reopen until its refeed and replays have run: a trap in that window is the second trap on the same event and fails the conversation. */
  private recovering = false
  /** The events a reopened core must be told again (a Session paused until `resume`, one sealed by `close()`). */
  private resumed = false
  private sealed = false
  /** The core took the terminal `close` and has not answered it (the api_result of request 0 follows the close's line job): a reopened core is closed again. */
  private closePending = false
  private readonly recycleListeners = new Set<RecycleListener>()
  private readonly reader: (sql: number, params: Param[]) => Uint8Array
  private readonly waiters = new Set<{ test: (n: Notice) => boolean; resolve: (n: Notice) => void; reject: (e: Error) => void }>()
  private readonly listeners = new Set<(n: Notice) => void>()

  constructor(opts: HostOptions) {
    this.opts = opts
    this.core = opts.core
    this.store = opts.store
    this.contexts = new ContextReader((sql, params) => opts.store.all(sql, params), new ContextSegments(opts.store))
    this.sqlTable = opts.sqlTable
    this.reader = (sql, params) => {
      const text = this.sqlTable[sql]
      if (text === undefined) throw new Error(`pig.read: no statement ${sql}`)
      const derived = answerContextRead(this.contexts, text, params)
      if (derived !== undefined) { this.counters.reads++; return encodeAnswer(derived) }
      const rows = this.store.all(text, params)
      this.opts.history?.rows(text, rows)
      this.counters.reads++
      return encodeAnswer(rows)
    }
    this.core.reader = this.reader
    this.clock = opts.clock ?? (() => Date.now())
  }

  get discarded(): FatalError | undefined { return this.dead }
  get inflight(): number { return this.running.size + this.chains.size }
  /** The async contexts of host calls the core may still name (ABI section 5.4): none once the core has no work left that a call started. */
  get originsHeld(): number { return this.origins.size }
  now(): number { return this.clock() }

  /** Queue an event and run the loop until no event is waiting. Returns the step the event produced when it ran in this call. */
  send(kind: number, payload?: Uint8Array, request?: number | true): Step | undefined {
    this.beforeSend?.()
    let produced: Step | undefined
    this.enqueue({ kind, payload, now: this.clock(), request: request === true ? undefined : request, owned: request !== undefined, result: s => { produced = s } })
    this.pump()
    return produced
  }

  /** Queue an owned event again for the handle that replaced a dropped one (called from an `onRecycled` listener); `result` receives the step. The loop that is already running delivers it. */
  replay(kind: number, payload: Uint8Array | undefined, result: (step: Step) => void) {
    this.enqueue({ kind, payload, now: this.clock(), owned: true, result, context: payload && this.ownedContexts.get(payload) })
  }

  /** The owner can have this host replace its handle (a renewal was given). */
  get canRecycle(): boolean { return this.opts.renew !== undefined }

  /**
   * Be told after each replacement of the handle, once the new core is open and before the events queued for it run: re-register what the old handle knew (`resend`, `send`).
   * `undelivered` holds the payloads of the owned events the old core applied nothing of: still queued, or the event it trapped on.
   */
  onRecycled(listener: RecycleListener): () => void {
    this.recycleListeners.add(listener)
    return () => { this.recycleListeners.delete(listener) }
  }

  /** Deliver a JSON event (open, submit, abort, registry, inspect). */
  sendJSON(kind: number, value: unknown, request?: number | true): Step | undefined {
    return this.send(kind, new TextEncoder().encode(JSON.stringify(value)), request)
  }

  /** Feed an event a trapped session did not complete to this session, once: its answer reaches the original caller's `send`. */
  resend(event: ReplayableEvent) {
    this.enqueue({ kind: event.kind, payload: event.payload, now: event.now, request: event.request, result: event.result, retried: true, context: event.context })
    this.pump()
  }

  /** Resolve with the first notice that satisfies `test`; reject when the handle is discarded first. */
  waitForNotice(test: (n: Notice) => boolean): Promise<Notice> {
    if (this.dead) return Promise.reject(this.dead)
    return new Promise((resolve, reject) => this.waiters.add({ test, resolve, reject }))
  }

  private sentSettings?: string

  private enqueue(q: Queued) {
    if (this.dead) throw this.dead
    if (q.context === undefined && CALLER_EVENTS.has(q.kind)) q.context = captureContext()
    if (q.context !== undefined && q.owned && q.payload !== undefined) this.ownedContexts.set(q.payload, q.context)
    if (this.opts.settings !== undefined && q.kind !== EVENT.close) {
      const text = JSON.stringify(this.opts.settings())
      if (q.kind !== EVENT.open && this.sentSettings !== undefined && text !== this.sentSettings) {
        this.queue.push({ kind: EVENT.registry, payload: new TextEncoder().encode(`{"settings":${text}}`), now: q.now, result: () => {} })
      }
      this.sentSettings = text
    }
    this.queue.push(q)
  }

  /** Deliver what is queued, replacing the handle first when a trap or a recycle asked for it. */
  private drain() {
    while (!this.dead && (this.queue.length || this.reopening)) {
      if (this.reopening) { this.reopen(); continue }
      this.deliver(this.queue.shift()!)
    }
  }

  private pump() {
    if (this.pumping) return
    this.pumping = true
    try {
      this.drain()
    } finally { this.pumping = false }
    if (this.dead && !this.dead.recovered) throw this.dead
    this.quietPoint()
  }

  /** The host has nothing queued or running after the event that just ran: the owner may recycle the instance. */
  private quietPoint() {
    if (this.inflight === 0 && !this.dead) this.opts.onQuiet?.()
  }

  private deliver(q: Queued) {
    if (q.sentinel) { this.recovering = false; return }
    this.counters.events++
    this.opts.trace?.event?.(q.kind, q.payload, q.now)
    this.remember(q)
    let step: Step
    let origin = 0
    if (q.context !== undefined) {
      origin = this.nextOrigin++
      this.origins.set(origin, q.context)
      if (q.kind === EVENT.open) this.openContext ??= q.context
    }
    try { step = q.rows ? this.core.rows(q.rows, q.now) : this.core.step(q.kind, q.payload, q.now, origin) } catch (e) {
      this.origins.delete(origin)
      return this.trapped(q, e, `the core failed on a ${q.kind} event`)
    }
    // A step that is not ok leaves nothing of the event in the core, so it will not name the origin again.
    if (step.status !== STATUS.ok) this.origins.delete(origin)
    if (step.status === STATUS.ok) { if (q.kind === EVENT.resume) this.resumed = true; else if (q.kind === EVENT.seal) this.sealed = true; else if (q.kind === EVENT.close) this.closePending = true }
    q.result?.(step)
    this.apply(step, q)
  }

  private remember(q: Queued) {
    this.recent.push({ kind: q.kind, bytes: q.payload?.byteLength ?? 0, now: q.now, request: q.request })
    if (this.recent.length > RECENT_EVENTS) this.recent.shift()
  }

  /** A step that failed: a client request that began it may be fed again, and the recent events go with the report. */
  private failed(error: FatalError, q: Queued): FatalError {
    const root = q.root ?? q
    error.event = { kind: root.kind, payload: root.payload, now: root.now, request: root.request, result: root.result, retried: root.retried, context: root.context }
    return error
  }

  /** The core threw on an event. A trap is recoverable once per event (ADR D16): the instance is dropped, a fresh one is opened from the store, and the work the trapped step carried is run again. Anything else is fatal. */
  private trapped(origin: Queued, e: unknown, what: string) {
    if (!(e instanceof CoreTrap) || !this.opts.renew) return this.discard(this.failed(new FatalError(`${what}: ${(e as Error).message}`, e), origin))
    this.counters.traps++
    if (this.recovering) return this.discard(new FatalError(`${what}: the core trapped again while recovering from a trap, so the conversation fails: ${e.message}`, e))
    // A rows event continues the step chain of the event that began it. The host applies nothing of a step that asks for reads (it may carry no commits or effects), so that event is the one to run again, as `failed` names it for a DoSession.
    this.reopening = { cause: e, root: origin.root ?? origin }
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
      if (step.effects.length) return this.discard(new FatalError("a step with reads must have no effects"))
      // ABI section 3, invariant 3: the step's commits are applied, in order, before its reads run (cold open's schema prelude).
      const tc = performance.now()
      for (const commit of step.commits) {
        try { this.commit(commit) } catch (e) { return this.discard(new FatalError(`a commit failed: ${(e as Error).message}`, e)) }
      }
      this.counters.msApply += performance.now() - tc
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
      const rows: Queued = { kind: EVENT.rows, now: this.clock(), result: q.result, root: q.root ?? q }
      this.remember(rows)
      try { next = this.core.rows(answers, rows.now) } catch (e) { return this.trapped(rows, e, "the core failed on a rows event") }
      this.counters.events++
      this.opts.trace?.event?.(EVENT.rows, undefined, rows.now)
      return this.apply(next, { ...rows, now: q.now })
    }
    const t0 = performance.now()
    for (const commit of step.commits) {
      try { this.commit(commit) } catch (e) { return this.discard(new FatalError(`a commit failed: ${(e as Error).message}`, e)) }
    }
    this.counters.msApply += performance.now() - t0
    const t1 = performance.now()
    if (step.order) {
      // ABI section 5.3: the core lists its notices and effects in Pi's synchronous order; a chain started after an api_result must queue after the host resolves it.
      for (const [region, index] of step.order) {
        if (this.dead) return
        if (region === 0) this.notifyIn(step.notices[index]!)
        else this.startIn(step.effects[index]!)
      }
    } else {
      for (const notice of step.notices) this.notifyIn(notice)
      for (const effect of step.effects) {
        if (this.dead) return
        this.startIn(effect)
      }
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

  /** Run `fn` in the async context the core stamped on a notice or effect (ABI section 5.4). */
  private within(origin: number, what: string, fn: () => void) {
    if (origin === 0) return fn()
    const run = this.origins.get(origin)
    if (run === undefined) return this.discard(new FatalError(`${what} names origin ${origin}, which the host does not hold`))
    run(fn)
  }

  private notifyIn(notice: Notice) {
    if (notice.kind === NOTICE.origin_free) return this.freeOrigins(notice.payload)
    this.within(notice.origin, `a ${noticeName(notice.kind)} notice`, () => this.notify(notice))
  }

  private startIn(effect: Effect) {
    this.within(effect.origin, `${effectName(effect.kind)} effect ${effect.id}`, () => this.start(effect))
  }

  /** The core holds no work for these origins any more (ABI section 5.4). */
  private freeOrigins(payload: Uint8Array) {
    const dv = new DataView(payload.buffer, payload.byteOffset, payload.byteLength)
    const n = dv.getUint32(0, true)
    for (let i = 0; i < n; i++) this.origins.delete(dv.getUint32(4 + 4 * i, true))
  }

  private notify(notice: Notice) {
    this.counters.notices++
    if (this.closePending && notice.kind === NOTICE.api_result && new DataView(notice.payload.buffer, notice.payload.byteOffset, 4).getUint32(0, true) === 0) this.closePending = false
    this.opts.onNotice?.(notice)
    for (const l of [...this.listeners]) l(notice)
    for (const w of [...this.waiters]) if (w.test(notice)) { this.waiters.delete(w); w.resolve(notice) }
  }

  private start(effect: Effect) {
    if (effect.id <= this.lastEffect) return this.discard(new FatalError(`effect ID ${effect.id} was issued twice or out of order (the last one started is ${this.lastEffect})`))
    this.lastEffect = effect.id
    const name = effectName(effect.kind)
    this.counters.effects[name] = (this.counters.effects[name] ?? 0) + 1
    const timers = this.opts.timers
    switch (effect.kind) {
      case EFFECT.timer: {
        const dv = new DataView(effect.payload.buffer, effect.payload.byteOffset, effect.payload.byteLength)
        timers?.set(dv.getUint32(0, true), dv.getFloat64(4, true), dv.getUint8(12))
        return
      }
      case EFFECT.timer_clear: return timers?.clear(new DataView(effect.payload.buffer, effect.payload.byteOffset, 4).getUint32(0, true))
      case EFFECT.liveness: return timers?.liveness(new DataView(effect.payload.buffer, effect.payload.byteOffset, 8).getFloat64(0, true))
      case EFFECT.hops: {
        let hops
        try { hops = decodeHops(effect.payload) } catch (e) { return this.discard(new FatalError(`hops effect ${effect.id} is malformed: ${(e as Error).message}`, e)) }
        return this.openChain(effect.id, hops.n, hops.at, hops.commits.length === 0 ? undefined : hops.commits)
      }
      case EFFECT.cancel: {
        const id = new DataView(effect.payload.buffer, effect.payload.byteOffset, 4).getUint32(0, true)
        this.running.get(id)?.abort()
        const chain = this.chains.get(id)
        if (chain) chain.aborted = true
        return this.opts.onCancel?.(id)
      }
    }
    const handler = this.opts.handlers?.[effect.kind]
    if (!handler) return this.discard(new FatalError(`no handler for a ${name} effect`))
    const controller = new AbortController()
    this.running.set(effect.id, controller)
    // An effect belongs to the handle that issued it: once that handle is replaced its ID may name another effect.
    const epoch = this.epoch
    const finish = () => { if (epoch === this.epoch) this.running.delete(effect.id) }
    try {
      const result = handler(effect, { host: this.effectHost(epoch), signal: controller.signal })
      if (result) {
        const settled = result.then(finish, error => { finish(); this.discardOnHandlerError(effect, error, epoch) }).finally(() => this.handlers.delete(settled))
        this.handlers.add(settled)
      } else finish()
    } catch (error) { finish(); this.discardOnHandlerError(effect, error, epoch) }
  }

  /** ABI section 5.3: a chain of n real microtask hops, then `hopped`; after hop `at` the chain applies `commits`, where the core would have emitted them. */
  private openChain(id: number, n: number, at: number, commits: Commit[] | undefined) {
    const chain = this.freeChains.pop() ?? new Chain(this)
    chain.id = id; chain.n = n; chain.left = n; chain.at = at; chain.commits = commits; chain.aborted = false
    this.chains.set(id, chain)
    this.counters.hopChains++
    this.counters.hopTicks += n
    void RESOLVED.then(chain.run)
  }

  /** One hop of a chain: the nth sends `hopped`. */
  hop(chain: Chain) {
    if (chain.aborted || this.dead) return this.endChain(chain)
    if (--chain.left > 0) {
      if (chain.commits !== undefined && chain.n - chain.left === chain.at) {
        const commits = chain.commits
        chain.commits = undefined
        try { for (const commit of commits) this.commit(commit) } catch (e) { this.endChain(chain); return this.discard(new FatalError(`a commit failed: ${(e as Error).message}`, e)) }
      }
      return void RESOLVED.then(chain.run)
    }
    const id = chain.id
    this.endChain(chain)
    this.sendHopped(id)
  }

  private endChain(chain: Chain) {
    if (this.chains.get(chain.id) === chain) this.chains.delete(chain.id)
    chain.commits = undefined
    this.freeChains.push(chain)
  }

  /** Deliver `hopped`. A core with a `hop` export answers an integer for a step the host would not decode; the other events and cores take `send`. */
  private sendHopped(id: number) {
    const core = this.core
    if (core.hop === undefined || this.pumping || this.queue.length > 0 || this.opts.settings !== undefined || this.opts.trace !== undefined) return void this.send(EVENT.hopped, u32Payload(id))
    this.beforeSend?.()
    if (this.dead) return
    // beforeSend may have sent an event of its own; it ran to completion, so the queue is empty again and this event is next.
    const now = this.clock()
    this.counters.events++
    this.recent.push({ kind: EVENT.hopped, bytes: 4, now, request: undefined })
    if (this.recent.length > RECENT_EVENTS) this.recent.shift()
    // The step runs inside the host loop, as pump runs it: an event a handler sends while the step's effects start (a completion
    // that settles synchronously) queues behind the step and runs after it, not inside it.
    this.pumping = true
    try {
      this.hopNow(id, now)
      this.drain()
    } finally { this.pumping = false }
    this.quietPoint()
  }

  /** Run `hopped` through the core's `hop` export and act on its answer. */
  private hopNow(id: number, now: number) {
    const core = this.core
    let answer: Step | 0 | 1
    try { answer = core.hop!(id, now) } catch (e) { return this.trapped({ kind: EVENT.hopped, now }, e, `the core failed on a ${EVENT.hopped} event`) }
    if (typeof answer === "object") return this.apply(answer, { kind: EVENT.hopped, now })
    this.counters.steps++
    if (answer === 0) return
    this.counters.hopsThin++
    this.counters.effects.hops = (this.counters.effects.hops ?? 0) + 1
    if (core.hopId! <= this.lastEffect) return this.discard(new FatalError(`effect ID ${core.hopId} was issued twice or out of order (the last one started is ${this.lastEffect})`))
    this.lastEffect = core.hopId!
    this.openChain(core.hopId!, core.hopTicks!, 0, undefined)
  }

  private effectView?: { epoch: number; host: EffectHost }
  /** The completion surface of effects issued in `epoch`: calls after the handle was replaced are dropped. */
  private effectHost(epoch: number): EffectHost {
    if (this.effectView?.epoch === epoch) return this.effectView.host
    const live = () => epoch === this.epoch
    const host: EffectHost = {
      modelEvents: (id, events) => { if (live()) this.modelEvents(id, events) },
      modelEventsRaw: (id, json) => { if (live()) this.modelEventsRaw(id, json) },
      modelBytes: (id, phase, bytes) => { if (live()) this.modelBytes(id, phase, bytes) },
      toolProgress: (id, kind, skipped, bytes, waitId) => { if (live()) this.toolProgress(id, kind, skipped, bytes, waitId) },
      toolDone: (id, outcome, value) => { if (live()) this.toolDone(id, outcome, value) },
      hookDone: (id, outcome, value) => { if (live()) this.hookDone(id, outcome, value) },
      phaseDone: (id, outcome, value) => { if (live()) this.phaseDone(id, outcome, value) },
      now: () => this.now(),
      contexts: this.contexts,
    }
    this.effectView = { epoch, host }
    return host
  }

  private discardOnHandlerError(effect: Effect, error: unknown, epoch: number) {
    if (this.dead || epoch !== this.epoch || (error as Error)?.name === "AbortError") return
    this.discard(new FatalError(`the ${effectName(effect.kind)} handler for effect ${effect.id} failed: ${(error as Error)?.message ?? error}`, error))
  }

  /** Discard the handle (ABI section 4.6 and ADR D16): cancel its effects, drop late completions, free the core session. */
  discard(error: FatalError) {
    if (this.dead) return
    this.dead = error
    error.recent ??= this.recent.slice()
    this.reopening = undefined
    for (const c of this.running.values()) c.abort()
    this.running.clear()
    for (const c of this.chains.values()) c.aborted = true
    this.chains.clear()
    this.origins.clear()
    this.queue.length = 0
    try { this.core.free() } catch { /* a trapped instance is discarded by its owner */ }
    for (const w of this.waiters) w.reject(error)
    this.waiters.clear()
    this.opts.onFatal?.(error)
  }

  /**
   * Recycle at a quiet point (ADR D10): the owner found the host idle, so the handle is returned now, which lets a pooled instance
   * whose memory passed the mark go with its last session, and the next event cold-opens a fresh one from the store.
   */
  recycle() {
    if (this.dead || this.reopening || !this.opts.renew) return
    this.opts.renew.drop()
    this.reopening = {}
  }

  /** True when no event is queued and nothing the core started is still running: the points at which recycling loses nothing but the cache. */
  get quiet(): boolean { return !this.dead && !this.pumping && this.queue.length === 0 && this.inflight === 0 && this.reopening === undefined }

  /**
   * Replace the handle (ADR D16, Durable plan 2 item 3): cancel what the old handle started, take a fresh handle on a healthy
   * instance, cold-open it from the store (the core reconciles live work and re-issues its effects), tell it what the old one had
   * been told (`resume`, `seal`), let the owner re-register (`onRecycled`), and run the pending event again. Everything but
   * effects it had in flight survives a recycle; those the core starts anew under new IDs. A trap while this runs is the second
   * trap on the same event: the conversation fails with that error.
   */
  private reopen() {
    const { cause, root: origin } = this.reopening!
    this.reopening = undefined
    this.recovering = true
    this.counters.recycles++
    // The effects, hops chains and timers of the old handle are gone with it; their late completions are dropped (effectHost).
    const ended = [...this.running.keys(), ...this.chains.keys()]
    for (const c of this.running.values()) c.abort()
    for (const c of this.chains.values()) c.aborted = true
    this.running.clear()
    this.chains.clear()
    this.origins.clear()
    this.lastEffect = -1
    this.epoch++
    for (const id of ended) this.opts.onCancel?.(id)
    this.opts.timers?.reset?.()
    this.commitsAtDrop = this.counters.commits
    // Queued events that name the old handle's work are void; the rest keep their order. Owned events, and the one the core trapped on, reached no applied step: their senders may send them again whatever they are.
    const queued = this.queue.splice(0)
    const undelivered = new Set<Uint8Array>()
    for (const q of [...queued, ...(origin ? [origin] : [])]) if (q.owned && q.payload !== undefined) undelivered.add(q.payload)
    const remaining = queued.filter(q => !q.sentinel && !q.owned && !handleBound(q.kind))
    let fresh: ReturnType<Renewal["acquire"]>
    try {
      this.opts.renew!.drop(cause)
      fresh = this.opts.renew!.acquire()
    } catch (e) {
      return this.discard(new FatalError(`the core could not be reopened${cause ? ` after a trap (${cause.message})` : ""}: ${(e as Error).message}`, e))
    }
    this.core = fresh.core
    this.sqlTable = fresh.sqlTable
    this.core.reader = this.reader
    const now = this.clock()
    if (this.opts.settings !== undefined) this.sentSettings = JSON.stringify(this.opts.settings())
    let refused: Step | undefined
    this.deliver({ kind: EVENT.open, payload: new TextEncoder().encode(JSON.stringify(fresh.open)), now, context: this.openContext, result: step => { if (step.status) refused = step } })
    if (!this.dead && refused) return this.discard(new FatalError(`the reopened core refused to open: ${refused.error?.raw ?? ""}`))
    if (!this.dead && this.resumed) this.deliver({ kind: EVENT.resume, now, context: this.openContext })
    if (!this.dead && this.sealed) this.deliver({ kind: EVENT.seal, now, context: this.openContext })
    if (this.dead) return
    // What the reopen's own steps queued, then what the owner re-registers, behind the refeed.
    const early = this.queue.splice(0)
    for (const listener of [...this.recycleListeners]) listener(cause, undelivered)
    if (this.dead) return
    const replays = this.queue.splice(0)
    const refeed = origin !== undefined && !origin.owned && !handleBound(origin.kind) && origin.kind !== EVENT.open ? [origin] : []
    // A close the old core took is final: nothing was sent after it but events of the old handle's work, so the close goes last.
    const close: Queued[] = this.closePending && !remaining.some(q => q.kind === EVENT.close) ? [{ kind: EVENT.close, now }] : []
    this.closePending = false
    this.queue.push(...refeed, ...replays, { kind: -1, now, sentinel: true }, ...remaining, ...early, ...close)
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
  toolDone(effectId: number, outcome: 0 | 1 | 2, value: unknown) {
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

  /**
   * Seal admission, let the invocations the core cancels end, then free the handle (Harness.close joins task invocations
   * after admission is sealed and before Storage closes; scheduler.ts join). Completions of the cancelled effects still
   * reach the core while it is joined, which commits nothing for them.
   */
  /** The `seal` event (ABI section 5.3): `Harness.close()` was called. Sent at once; a core that does not know the event rejects it and nothing else changes. */
  seal(): void {
    if (this.dead) return
    this.send(EVENT.seal)
  }

  async closeAndJoin(): Promise<void> {
    // Pi's close joins the invocations the seal signalled before it closes the Session (harness.ts close: scheduler.join(), then session.close()): a handler that outlives its signal still delivers its completion to the core, which is open until the terminal close.
    while (this.handlers.size > 0 && !this.dead) await Promise.allSettled([...this.handlers])
    if (this.dead) return
    // The core closes behind its Session line (handler.go EventClose) and reports it as the api_result of request 0: work admitted before the close settles first, and its hop chains must still run.
    const closed = this.waitForNotice(n => n.kind === NOTICE.api_result && new DataView(n.payload.buffer, n.payload.byteOffset, 4).getUint32(0, true) === 0)
    closed.catch(() => {})
    try { this.send(EVENT.close) } catch (error) { this.discard(new FatalError("closed")); throw error }
    try { await closed } catch { /* the handle was discarded while it closed */ }
    this.discard(new FatalError("Harness is closed"))
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
