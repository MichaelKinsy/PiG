// Wasm instances of one isolate (ADR D10). One instance serves many Durable Objects through session handles, so the per-instance
// floor is paid once. Linear memory never shrinks, so an instance whose memory passes the high-water mark stops taking new
// sessions, its idle sessions unload (a cache drop: the next event cold-opens), and it is dropped when the last session leaves.
import { WasmCore, WasmSession, type CoreOptions } from "./wasm.ts"

export interface PoolOptions {
  /** Linear memory in bytes after which an instance is recycled. Default 48 MiB: a 128 MiB isolate holds two before the runtime replaces it. */
  highWater?: number
  /** One instance per object (`false`) instead of one per isolate. */
  shared?: boolean
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
  /** The instance trapped: every session on it is gone. */
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

  /** The tenant went idle: an instance over the mark stops taking sessions and asks its idle tenants to unload. */
  idled() { this.pool.idled(this.instance) }

  /** The instance trapped. */
  broke(error: Error) { this.pool.broken(this.instance, error) }

  /** Free the session. Safe to call twice. */
  release() {
    if (this.released) return
    this.released = true
    try { this.session.free() } catch { /* a trapped instance is already gone */ }
    this.pool.released(this.instance, this)
  }
}

export type Instance = { core: WasmCore; leases: Set<Lease>; retiring: boolean; broken: boolean }

export class CorePool {
  /** Instances created, and how many were recycled for memory or a trap (tests and M1). */
  created = 0
  recycled = 0
  private current?: Instance
  private readonly all = new Set<Instance>()
  private readonly module: WebAssembly.Module
  private readonly options: PoolOptions

  constructor(module: WebAssembly.Module, options: PoolOptions = {}) {
    this.module = module
    this.options = options
  }

  get highWater(): number { return this.options.highWater ?? 48 * 1024 * 1024 }
  get instances(): number { return this.all.size }

  acquire(tenant: Tenant): Lease {
    const instance = this.pick()
    const lease = new Lease(this, instance, instance.core.newSession(), tenant)
    instance.leases.add(lease)
    return lease
  }

  private pick(): Instance {
    const shared = this.options.shared ?? true
    const current = this.current
    if (shared && current && !current.broken) {
      if (current.core.memoryBytes <= this.highWater) return current
      this.retire(current)
    }
    return this.create(shared)
  }

  private create(shared: boolean): Instance {
    const core = new WasmCore(this.module, this.options.core)
    this.created++
    this.options.onInstance?.(core)
    const instance: Instance = { core, leases: new Set(), retiring: false, broken: false }
    this.all.add(instance)
    if (shared) this.current = instance
    return instance
  }

  /** Stop sending new sessions to an instance over the mark and ask its idle tenants to unload. */
  private retire(instance: Instance) {
    instance.retiring = true
    if (this.current === instance) this.current = undefined
    for (const lease of [...instance.leases]) if (lease.tenant.idle()) lease.tenant.unload()
    if (instance.leases.size === 0) this.drop(instance)
  }

  idled(instance: Instance) {
    if (!instance.retiring && instance.core.memoryBytes > this.highWater) this.retire(instance)
  }

  released(instance: Instance, lease: Lease) {
    instance.leases.delete(lease)
    if (instance.leases.size > 0) return
    if (instance.retiring || instance.broken || this.options.shared === false || instance.core.memoryBytes > this.highWater) this.drop(instance)
  }

  /** A trap or a failed ABI check: discard the instance and every session on it (ADR D16). */
  broken(instance: Instance, error: Error) {
    if (instance.broken) return
    instance.broken = true
    if (this.current === instance) this.current = undefined
    for (const other of [...instance.leases]) other.tenant.broken(error)
    this.drop(instance)
  }

  private drop(instance: Instance) {
    if (!this.all.delete(instance)) return
    if (this.current === instance) this.current = undefined
    this.recycled++
  }
}
