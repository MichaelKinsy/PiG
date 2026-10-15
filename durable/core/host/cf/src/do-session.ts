// One Durable Object's session on the core: a lease on the object's own Wasm instance, the host loop over the object's SQL, the
// client for api and transaction requests, the effect executors, and the alarm. The object owns a DoSession and nothing
// else that outlives a request: everything here is a cache of SQLite (ADR D9), so dropping it costs one cold open.
import { EVENT } from "./abi.ts"
import { CoreClient } from "./client.ts"
import { createEffectHandlers, type EffectRuntime } from "./effects.ts"
import { FatalError, HostSession, RejectedError, type HostOptions } from "./host.ts"
import type { FlightRecorder } from "./observe.ts"
import { CorePool, type Lease, type Tenant } from "./pool.ts"
import { doStore } from "./store-do.ts"
import { AlarmTimers } from "./timers.ts"
import { CoreTrap } from "./wasm.ts"

export interface DoSessionConfig {
  pool: CorePool
  storage: DurableObjectStorage
  /** The `open` event payload for one cold open: settings, models in use, registry snapshot, sidecar mode (ABI section 5). */
  open(): unknown
  /** Everything an effect needs except the client, which the session owns. */
  runtime(client: CoreClient): Omit<EffectRuntime, "client" | "hostApi"> & { hostApi?: Partial<EffectRuntime["hostApi"]> }
  clock?: () => number
  /** Receives a discard: the handle died and the next call cold-opens. */
  onFatal?: (error: Error) => void
  onNotice?: HostOptions["onNotice"]
  onRejected?: HostOptions["onRejected"]
  trace?: HostOptions["trace"]
  /** Per-step observability and the ring of the last events (observe.ts). A fatal step's error carries `report`, the trap report that replays it. */
  recorder?: FlightRecorder
}

/** Reopens in place before a session with this many consecutive traps waits for its retry alarm. */
const RECOVERIES = 3
const RETRY_BASE_MS = 1000
const RETRY_MAX_MS = 60_000

export class DoSession implements Tenant {
  private readonly config: DoSessionConfig
  private lease?: Lease
  private hostSession?: HostSession
  private clientRef?: CoreClient
  private timersRef?: AlarmTimers
  /** Cold opens this object has performed (the M1 count). */
  coldOpens = 0

  constructor(config: DoSessionConfig) { this.config = config }

  get open(): boolean { return this.hostSession !== undefined && this.hostSession.discarded === undefined }
  get host(): HostSession { return this.ensureOpen() }
  get client(): CoreClient { this.ensureOpen(); return this.clientRef! }
  get timers(): AlarmTimers | undefined { return this.timersRef }

  /** The session, cold-opening it first when it was dropped or never opened. */
  ensureOpen(): HostSession {
    if (this.hostSession && this.hostSession.discarded === undefined) return this.hostSession
    this.teardown(this.reopening)
    const cfg = this.config
    const lease = this.lease = cfg.pool.acquire(this)
    const store = doStore(cfg.storage)
    const timers = this.timersRef = new AlarmTimers(cfg.storage, id => this.hostSession?.timerFired(id), cfg.clock, error => this.fail(error))
    const handlers: NonNullable<HostOptions["handlers"]> = {}
    const host = this.hostSession = new HostSession({
      core: cfg.recorder ? cfg.recorder.attach(lease.session, store) : lease.session, sqlTable: lease.core.sqlTable, store, clock: cfg.clock, timers, handlers,
      onNotice: cfg.onNotice, onRejected: cfg.onRejected, trace: cfg.trace,
      onFatal: error => this.discarded(error),
    })
    // A client that survived a trap (recover) moves to the new session: its callers' promises stay valid.
    const client = this.clientRef ??= new CoreClient(host)
    if (client.host !== host) client.rebind(host)
    const runtime = cfg.runtime(client)
    Object.assign(handlers, createEffectHandlers({ ...runtime, client, hostApi: { ...runtime.hostApi, client } }))
    this.coldOpens++
    let step
    try { step = host.sendJSON(EVENT.open, cfg.open()) } catch (error) { this.fail(error); throw error }
    if (step?.status) {
      const refused = new RejectedError(step.error?.name, step.error?.message, step.error?.raw ?? "")
      this.teardown()
      throw refused
    }
    return host
  }

  /** The object alarm: reopen when the cache was dropped (the core reconciles and resumes, ADR D7), then run what is due. */
  alarm() {
    this.ensureOpen()
    this.timersRef?.due()
  }

  idle(): boolean {
    const host = this.hostSession
    if (!host || host.discarded) return true
    return host.inflight === 0 && (this.clientRef?.pending ?? 0) === 0 && (this.timersRef?.volatilePending ?? 0) === 0
  }

  /** Cache drop (ADR D9): free the handle, forget the timers, keep the alarm in storage. */
  unload() { this.teardown() }

  broken(error: Error) {
    this.hostSession?.discard(new FatalError(`the Wasm instance failed: ${error.message}`, error))
  }

  /** Seal the session and release the lease. */
  close() {
    const host = this.hostSession
    if (host && !host.discarded) { try { host.close() } catch { /* already discarded */ } }
    this.teardown()
  }

  private quiet = false

  private discarded(error: FatalError) {
    if (this.config.recorder && !(error as FatalError & { report?: unknown }).report) (error as FatalError & { report?: unknown }).report = this.config.recorder.report(error)
    if (error.cause2 instanceof CoreTrap) {
      this.lease?.broke(error)
      if (!this.quiet && this.recover(error)) return
    }
    this.clientRef?.failAll(error)
    if (!this.quiet) this.config.onFatal?.(error)
    this.teardown()
  }

  /** Consecutive traps without a recovered request: each one doubles the retry alarm's delay (an idempotent alarm: the earlier of it and any alarm already set stands). */
  private failures = 0
  private reopening = false

  /**
   * A trap poisons the instance; nothing of the failed step was applied. Drop the instance, reopen a new one from SQLite (the core
   * reconciles and resumes the work the dead session held) and feed a client request that did not complete once. Events whose ids
   * belong to the dead session (effect completions, timers) are not fed: their work is re-derived by the reopen. A trap on the fed
   * request, or on the reopen itself, is final: the caller gets the error and a retry alarm is set with backoff.
   */
  private recover(error: FatalError): boolean {
    const event = error.event
    this.failures++
    if (this.reopening || event === undefined || event.retried || this.failures > RECOVERIES) { this.retryLater(); return false }
    const trapped = new FatalError(`the Wasm instance trapped; the session was reopened: ${error.message}`, error)
    this.clientRef?.failAll(trapped, event.request)
    this.teardown(true)
    let host: HostSession
    this.reopening = true
    try { host = this.ensureOpen() } catch { this.retryLater(); return false } finally { this.reopening = false }
    if (event.request !== undefined) {
      // A second trap is final: the new session reported it and the caller of the failed send gets the first error.
      try { host.resend(event) } catch { return true }
      this.failures = 0
    }
    error.recovered = true
    return true
  }

  /** The object alarm retries the reopen after a failure: the delay doubles with each consecutive one. Only an alarm later than the retry is replaced. */
  private retryLater() {
    const storage = this.config.storage
    const at = (this.config.clock ?? Date.now)() + Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** Math.min(Math.max(this.failures - 1, 0), 16))
    void Promise.resolve(storage.getAlarm()).then(current => { if (current === null || current > at) return storage.setAlarm(at) }).catch(error => this.fail(error))
  }

  private fail(error: unknown) {
    this.config.onFatal?.(error instanceof Error ? error : new Error(String(error)))
  }

  private teardown(keepClient = false) {
    const host = this.hostSession
    this.hostSession = undefined
    this.timersRef?.stop()
    this.timersRef = undefined
    if (!keepClient) { this.clientRef?.close(); this.clientRef = undefined }
    const lease = this.lease
    this.lease = undefined
    if (host && !host.discarded) { this.quiet = true; try { host.discard(new FatalError("unloaded")) } finally { this.quiet = false } }
    lease?.release()
  }

  /** Called when the object goes idle: lets an over-mark instance ask its idle tenants to unload. */
  idled() { this.failures = 0; this.lease?.idled() }
}
