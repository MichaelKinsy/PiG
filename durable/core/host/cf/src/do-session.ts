// One Durable Object's session on the core: a lease on a pooled Wasm instance, the host loop over the object's SQL, the
// client for api and transaction requests, the effect executors, and the alarm. The object owns a DoSession and nothing
// else that outlives a request: everything here is a cache of SQLite (ADR D9), so dropping it costs one cold open.
import { EVENT } from "./abi.ts"
import { CoreClient } from "./client.ts"
import { createEffectHandlers, type EffectRuntime } from "./effects.ts"
import { FatalError, HostSession, RejectedError, type HostOptions } from "./host.ts"
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
}

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
    this.teardown()
    const cfg = this.config
    const lease = this.lease = cfg.pool.acquire(this)
    const store = doStore(cfg.storage)
    const timers = this.timersRef = new AlarmTimers(cfg.storage, id => this.hostSession?.timerFired(id), cfg.clock, error => this.fail(error))
    const handlers: NonNullable<HostOptions["handlers"]> = {}
    const host = this.hostSession = new HostSession({
      core: lease.session, sqlTable: lease.core.sqlTable, store, clock: cfg.clock, timers, handlers,
      onNotice: cfg.onNotice, onRejected: cfg.onRejected, trace: cfg.trace,
      onFatal: error => this.discarded(error),
    })
    const client = this.clientRef = new CoreClient(host)
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
    if (error.cause2 instanceof CoreTrap) this.lease?.broke(error)
    this.clientRef?.failAll(error)
    if (!this.quiet) this.config.onFatal?.(error)
    this.teardown()
  }

  private fail(error: unknown) {
    this.config.onFatal?.(error instanceof Error ? error : new Error(String(error)))
  }

  private teardown() {
    const host = this.hostSession
    this.hostSession = undefined
    this.timersRef?.stop()
    this.timersRef = undefined
    this.clientRef?.close()
    this.clientRef = undefined
    const lease = this.lease
    this.lease = undefined
    if (host && !host.discarded) { this.quiet = true; try { host.discard(new FatalError("unloaded")) } finally { this.quiet = false } }
    lease?.release()
  }

  /** Called when the object goes idle: lets an over-mark instance ask its idle tenants to unload. */
  idled() { this.lease?.idled() }
}
