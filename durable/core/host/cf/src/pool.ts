// Wasm instances of one isolate. Every Durable Object has its own instance: a trap, an out-of-memory or a recycle takes only that
// object's core, and V8 shares the compiled code of the module between instances. Linear memory never shrinks, so an object that
// passes the high-water mark at a quiet point drops its instance (a cache drop: the next event cold-opens a new one).
import { WasmCore, WasmSession, type CoreOptions } from "./wasm.ts"

export interface PoolOptions {
  /** Linear memory in bytes after which an idle object drops its instance. Default 48 MiB. */
  highWater?: number
  core?: CoreOptions
  /** Called when an instance is created, for M1 accounting. */
  onInstance?: (core: WasmCore) => void
}

/** What the pool asks of the object that holds a session. */
export interface Tenant {
  /** True when nothing is running or waiting on the session, so its state can be dropped. */
  idle(): boolean
  /** Drop the session and its derived state; the next event cold-opens. */
  unload(): void
  /** The instance trapped: its session is gone. */
  broken(error: Error): void
}

export class Lease {
  readonly core: WasmCore
  readonly session: WasmSession
  readonly tenant: Tenant
  readonly instance: Instance
  private readonly pool: CorePool
  private released = false

  constructor(pool: CorePool, instance: Instance, session: WasmSession, tenant: Tenant) {
    this.pool = pool
    this.instance = instance
    this.core = instance.core
    this.session = session
    this.tenant = tenant
  }

  /** The tenant went idle: an instance over the mark is dropped through the tenant's unload. */
  idled() { this.pool.idled(this.instance) }

  /** The instance trapped. */
  broke(error: Error) { this.pool.broken(this.instance, error) }

  /** The tenant caught the trap itself and is replacing its session: the instance is dropped and the tenant is not told. */
  trapped() { this.pool.trapped(this.instance) }

  /** The instance's linear memory is over the pool's mark. */
  get full(): boolean { return this.core.memoryBytes > this.pool.highWater }

  /** Free the session. Safe to call twice. */
  release() {
    if (this.released) return
    this.released = true
    try { this.session.free() } catch { /* a trapped instance is already gone */ }
    this.pool.released(this.instance)
  }
}

export type Instance = { core: WasmCore; lease?: Lease; broken: boolean }

export class CorePool {
  /** Instances created, and how many were dropped (tests and M1). */
  created = 0
  recycled = 0
  private readonly all = new Set<Instance>()
  private readonly module: WebAssembly.Module
  private readonly options: PoolOptions

  constructor(module: WebAssembly.Module, options: PoolOptions = {}) {
    this.module = module
    this.options = options
  }

  get highWater(): number { return this.options.highWater ?? 48 * 1024 * 1024 }
  get instances(): number { return this.all.size }

  /** A new instance and a session on it for one object. */
  acquire(tenant: Tenant): Lease {
    const core = new WasmCore(this.module, this.options.core)
    this.created++
    this.options.onInstance?.(core)
    const instance: Instance = { core, broken: false }
    this.all.add(instance)
    const lease = new Lease(this, instance, core.newSession(), tenant)
    instance.lease = lease
    return lease
  }

  idled(instance: Instance) {
    const lease = instance.lease
    if (lease && !instance.broken && instance.core.memoryBytes > this.highWater && lease.tenant.idle()) lease.tenant.unload()
  }

  released(instance: Instance) { this.drop(instance) }

  /** A trap or a failed ABI check: discard the instance and its session (ADR D16). */
  broken(instance: Instance, error: Error) {
    if (instance.broken) return
    instance.broken = true
    instance.lease?.tenant.broken(error)
    this.drop(instance)
  }

  /** A trap its tenant recovers from: the instance goes, and the tenant takes a new one. */
  trapped(instance: Instance) {
    instance.broken = true
    this.drop(instance)
  }

  private drop(instance: Instance) {
    if (!this.all.delete(instance)) return
    this.recycled++
  }
}
