// The pi-durable public API over the Durable core (src/harness/harness.ts, src/session/session.ts). Every method is one channel
// of the ABI: a `submit` or `abort` event, an `api` request, a transaction, or a read of Pi's own tables, which the host serves
// directly (CONTRACT section 3.10). Extension code, resolution of agents and wraps, hooks, tools and custom tasks run here;
// admission, scheduling, context derivation and the store format run in the core.
import { admitWrites, adoptTables } from "./table-identity.ts"
import { forgetColdLoads, trackColdLoads } from "./coldload.ts"
import { BACKGROUND_CONTEXT, awaitWithContext, withoutAbortSignal } from "@earendil-works/chord/context"
import type { Context } from "@earendil-works/chord"
import { EVENT, NOTICE } from "../abi.ts"
import { CoreClient } from "../client.ts"
import { forgetSnapshots, substitutePublished, substituteWrite } from "./snapshots.ts"
import { createEffectHandlers, type EffectRuntime, type ModelsLike } from "../effects.ts"
import { FatalError, HostSession, RejectedError, type CoreHandle, type Renewal } from "../host.ts"
import { HostRegistry, type HostRegistrySource } from "../registry.ts"
import type { PublishedNotice } from "../protocol.ts"
import { Waiters } from "./waiters.ts"
import type { Store } from "../store.ts"
import { CorePool, type Lease } from "../pool.ts"
import { CoreTrap } from "../wasm.ts"
import { AgentDoc, agentJSON, applyChange, configure, resolveAgent, resolveSettings, type AgentState } from "./agent.ts"
import { SentSettings } from "./settings-watch.ts"
import { bindContext, documentReader, invocationApi, invocationCommit, invocationOf, type InvocationBinding } from "./hostapi.ts"
import { resolveAddress, wireAddress, type AnyDocToken } from "./documents.ts"
import { WatchHub } from "./watches.ts"
import { runCommit, TxProxy, wireDefinition } from "./tx.ts"
import { recordPublication } from "./publications.ts"
import { overriddenMethods, SqlStorage, type Storage, type StorageProfile } from "./storage.ts"
import { coreFromEnvironment } from "./core-env.ts"
import { customTaskRunner } from "./taskruntime.ts"
import { TimeoutTimers } from "../timers.ts"
import type { TimerDriver } from "../host.ts"
import { errorMessage, reviveError, SessionFailed, StorageRequestError } from "../errors.ts"
import { contained } from "./session/session.ts"
import { structuredOutputError } from "./structured.ts"
import { validateToolArguments } from "@earendil-works/pi-ai/utils/validation"
import { type ClientIdentity, mergeClientIdentity, PI_ATTRIBUTION } from "@earendil-works/pi-ai/utils/identity"

const errorText = errorMessage

/** Request identity of a Harness's model requests: pi-ai's defaults with provider attribution to pi (harness.ts DEFAULT_HARNESS_IDENTITY). */
export const DEFAULT_HARNESS_IDENTITY: ClientIdentity = { attribution: PI_ATTRIBUTION }

/** The request methods pi-ai's withClientIdentity gives the identity to (identity.ts); each takes its options last. */
const IDENTITY_METHODS = new Set<PropertyKey>(["stream", "complete", "streamSimple", "completeSimple", "streamDeferred", "fetchDeferred", "cancelDeferred", "generateImages", "classify"])

/**
 * pi-ai's `withClientIdentity(models, identity)` over the subset of `Models` the host holds (`ModelsLike` is partial, and a method the models lacks stays
 * absent): a request's options become `{ ...options, identity: mergeClientIdentity(identity, options?.identity) }`, so `identity` is their last key and
 * a request's own identity overrides the Harness's field by field. Everything else is delegated unchanged.
 */
function withHarnessIdentity(models: ModelsLike, identity: ClientIdentity): ModelsLike {
  return new Proxy(models, {
    get(target, key) {
      const value = Reflect.get(target, key, target)
      if (typeof value !== "function") return value
      if (!IDENTITY_METHODS.has(key)) return value.bind(target)
      return (model: unknown, context: unknown, options?: object) => value.call(target, model, context, { ...options, identity: mergeClientIdentity(identity, (options as { identity?: ClientIdentity } | undefined)?.identity) })
    },
  })
}

export const ROOT_CONVERSATION_ID = 1
const decoder = new TextDecoder()

/** A compiled core module, or any object that serves ABI sessions: the Wasm core, the TypeScript control, a native binding. */
export type CoreLike = { newSession(): CoreHandle; readonly sqlTable: string[] }
export type CoreSource = WebAssembly.Module | CoreLike
let defaultModule: CoreSource | undefined
/** Choose the core every Harness opened without an explicit `core` option uses. A Worker calls this once at module scope with its imported Wasm module. */
export function useCore(module: CoreSource) { defaultModule = module }

/** One pool per compiled module: the instance a module's Harnesses share (ADR-0001 D10). */
const pools = new WeakMap<WebAssembly.Module, CorePool>()
const poolOf = (module: WebAssembly.Module): CorePool => { let pool = pools.get(module); if (!pool) pools.set(module, pool = new CorePool(module)); return pool }

let profileOverride: Partial<Record<StorageProfile, StorageProfile>> | undefined
/** Map each Storage's own profile to the one the conformance gate's reference has for that adapter: the reference runs `MemoryStorage` as a capsql facade behind a lazy proxy (`gate`) and the node adapters on the same facade opened directly (`gate-capsql`); contract-setup.ts installs the map (S7: the test facade only). */
export function useStorageProfile(map: Partial<Record<StorageProfile, StorageProfile>> | undefined) { profileOverride = map }

export interface FacadeOptions {
  models: ModelsLike
  /**
   * How model requests identify the app to providers, merged field by field over `DEFAULT_HARNESS_IDENTITY` (provider attribution to pi).
   * It applies to every request through the Harness's models, including hooks and tools, and overrides the identity the `models` was
   * created with; a request's own `identity` overrides it. Pass `{ attribution: null }` to send no attribution headers (types.ts HarnessOptions.identity).
   */
  identity?: ClientIdentity
  registry: HostRegistrySource
  settings?: any
  env?: EffectRuntime["env"]
  conversationCreated?: (tx: TxProxy, record: any) => unknown | Promise<unknown>
  now?: () => number
  onReport?: (error: unknown) => void
  /** PiG addition: the timer driver (a Durable Object passes its alarm timers). Default: setTimeout timers, cancelled at close. */
  timers?: TimerDriver
  /** PiG addition: the compiled core module (default: `useCore()`). */
  core?: CoreSource
  /** PiG addition: the pool of Wasm instances this Harness's session lives in (default: the module's own, shared by every Harness of the isolate). Its high-water mark decides when an instance is recycled at a quiet point. */
  pool?: CorePool
  /** PiG addition: `session` opens the tables-and-documents Session without the Harness (createSession). */
  mode?: "session" | "harness"
  /** PiG addition: the sidecar mode of ADR D6. Default `index`. */
  sidecar?: "off" | "index" | "snapshot"
  /** The Storage adapter whose await structure the core models for Pi's microtask ordering (ABI.md 5.3 rule 6, dcore-trace S7). Sent in `open` only when set; the core picks its default otherwise. */
  storageProfile?: StorageProfile
  /** PiG addition: model catalogue entries the core may name; default every model `models` lists. */
  modelsInUse?: (models: ModelsLike) => unknown[]
}

type Waiter = { match: (n: PublishedNotice) => boolean; resolve: () => void; reject: (error: Error) => void }

/** `onReport` that cannot fail: it is the last place an error goes, so its own throw or rejection is dropped (harness.ts safeReport). */
export function safeReport(onReport: ((error: unknown) => void) | undefined): (error: unknown) => void {
  return error => contained(() => onReport?.(error), () => {})
}

/** The host clock, falling back to `Date.now` when it throws; the first throw is reported (harness.ts safeNow). */
export function safeNow(now: (() => number) | undefined, report: (error: unknown) => void): () => number {
  if (now === undefined) return Date.now
  let reported = false
  return () => {
    try { return now() } catch (error) {
      // Set first: a report handler that reads the clock again must not report again.
      if (!reported) { reported = true; report(error) }
      return Date.now()
    }
  }
}

/** session.ts assertUsable: the error of a call on a Session that is closing. */
class ClosedSession extends Error { constructor() { super("Session is closed") } }

export class Harness {
  readonly host: HostSession
  readonly client: CoreClient
  private timers?: TimerDriver
  private readonly watches: WatchHub
  readonly registry: HostRegistry
  private readonly options: FacadeOptions
  private readonly store: Store
  private readonly storage: Storage
  /** The Storage the Harness runs on (the view mounts hand it to the observers they create). */
  storageOf(): Storage { return this.storage }
  /** The Pi Storage methods the caller's Storage overrides: the core asks the host's Storage first (PROTOCOL.md, "Storage gate"). */
  private gate: string[]
  /** Pi reads `options.settings` live (resolveSettings(options.settings) at each use). */
  private get settings(): ReturnType<typeof resolveSettings> { return resolveSettings(this.options.settings) }
  private readonly settingsSent = new SentSettings()
  private readonly waiters = new Set<Waiter>()
  /** Pending Submission.wait calls (submissions.ts #waiters). */
  private readonly submissionWaiters = new Waiters<number, any>()
  /** Published notices that carried task records: `idle` rereads when one arrives during its read. */
  private taskPublications = 0
  private readonly commitListeners = new Set<(p: unknown, c: Context) => void>()
  /** The Session's own components' commit listeners (session.ts observeCommits): a throw fails the Session. */
  private readonly internalCommitListeners = new Set<(p: unknown, c: Context) => void>()
  private readonly closeListeners = new Set<() => void>()
  private resumeQueued = false
  private shut = false
  /** The fatal error that discarded the core session (a trap, a failed commit); every later wait rejects with it. */
  private failed?: Error
  /** session.ts #failure: the first error a Storage call threw. The Session is closed with it; every later call rejects SessionFailed. */
  private failure?: { readonly error: unknown }
  private readonly end = Promise.withResolvers<{ reason: "closed" } | { reason: "failed"; error: unknown }>()
  /** Why the Session ended (types.ts Session.closed): `close()`, or the failed Storage call's error. Settles once the Storage is closed. */
  readonly closed: Promise<{ reason: "closed" } | { reason: "failed"; error: unknown }> = this.end.promise
  readonly now: () => number

  private constructor(storage: Storage, store: Store, host: HostSession, client: CoreClient, registry: HostRegistry, options: FacadeOptions) {
    this.storage = storage; this.gate = overriddenMethods(storage); this.store = store; this.host = host; this.client = client; this.registry = registry; this.options = options
    if (options.mode === "session") trackColdLoads(client, storage, this.gate)
    this.watches = new WatchHub(client, error => (options.onReport ?? (() => {}))(error), BACKGROUND_CONTEXT)
    this.now = options.now ?? Date.now
  }

  /** Open a Harness over a storage: a cold open of the core (ABI section 5, `open`). */
  static async open(storage: Storage, rawOptions: FacadeOptions, context: Context = BACKGROUND_CONTEXT): Promise<Harness> {
    context.abortSignal?.throwIfAborted()
    // harness.ts constructor: `onReport` cannot fail what reports to it, and a clock that throws falls back to Date.now (reported once).
    const reported = safeReport(rawOptions.onReport)
    const options: FacadeOptions = { ...rawOptions, onReport: reported, now: safeNow(rawOptions.now, reported) }
    // harness.ts currentModels: a Harness reaches its models through withClientIdentity, so each request carries `identity` as its last option. A Session has no models.
    if (options.mode !== "session") options.models = withHarnessIdentity(options.models, mergeClientIdentity(DEFAULT_HARNESS_IDENTITY, options.identity) ?? DEFAULT_HARNESS_IDENTITY)
    // A Session has no built-in tasks: only a Harness needs them in its registry.
    const missing = options.mode === "session" ? [] : ["pi.generation", "pi.tool", "pi.compaction"].filter(name => options.registry.snapshot().task(name) === undefined)
    if (missing.length > 0) throw new Error(`Registry lacks built-in tasks ${missing.join(", ")}; create it with createRegistry()`)
    const module = options.core ?? defaultModule ?? await coreFromEnvironment()
    if (!module) throw new Error("Harness.open needs a core: pass options.core, call useCore(module) or set DCORE_WASM")
    const store = await storage.open()
    // Every Harness has its own Wasm instance of the compiled module (a trap takes only this object's core; V8 shares the code).
    // The instance can be replaced: after a trap (the host reopens from the store and runs the pending work again, ADR D16) and at a
    // quiet point when its memory passed the pool's mark (the next event cold-opens a fresh one).
    let lease: Lease | undefined
    let session: CoreHandle
    let core: CoreLike
    const self: { harness?: Harness; host?: HostSession; phases?: ReturnType<typeof customTaskRunner> } = {}
    const track = (c: CoreLike) => {
      // The conformance gate bounds each core's linear memory per turn (durable/contract/lib/memory.mjs).
      ;(globalThis as Record<symbol, { cores?: Set<unknown> } | undefined>)[Symbol.for("pig.contract")]?.cores?.add(c)
    }
    let renewal: Renewal
    let quietChecked = false
    if (module instanceof WebAssembly.Module) {
      const pool = options.pool ?? poolOf(module)
      const tenant = {
        idle: () => self.harness?.quiet ?? false,
        unload: () => self.host?.recycle(),
        broken: (error: Error) => self.host?.discard(error instanceof FatalError ? error : new FatalError(error.message, error)),
      }
      lease = pool.acquire(tenant)
      core = lease.core
      session = lease.session
      renewal = {
        drop: trap => {
          if (lease === undefined) return
          if (trap) lease.trapped()
          lease.release()
          lease = undefined
        },
        acquire: () => {
          lease = pool.acquire(tenant)
          track(lease.core)
          return { core: lease.session, sqlTable: lease.core.sqlTable, open: self.harness!.reopenPayload() }
        },
      }
    } else {
      const source = module
      core = source
      session = source.newSession()
      let current: CoreHandle | undefined = session
      renewal = {
        drop: () => { try { current?.free() } catch { /* the handle is gone */ } current = undefined },
        acquire: () => { current = source.newSession(); return { core: current, sqlTable: source.sqlTable, open: self.harness!.reopenPayload() } },
      }
    }
    track(core)
    const registry = new HostRegistry(options.registry)
    const handlers: Record<number, any> = {}
    const timers = options.timers ?? new TimeoutTimers(id => host.timerFired(id), options.now)
    const host: HostSession = new HostSession({
      core: session, sqlTable: core.sqlTable, store, clock: options.now, handlers, timers,
      onNotice: n => self.harness?.onNotice(n.kind, n.payload),
      onRejected: error => (options.onReport ?? (() => {}))(error),
      onFatal: error => {
        // The host freed its session; a trap took the instance (pool.ts broken), and the lease is returned either way.
        if (error.cause2 instanceof CoreTrap) lease?.broke(error)
        lease?.release()
        self.harness?.fail(error)
      },
      onCancel: id => self.phases?.ended(id),
      renew: renewal,
      // Over the pool's mark, the host is asked at its next quiet point to give the instance up. The check waits one microtask so the caller's continuation, which may make the next request, runs first; under the mark nothing is scheduled.
      onQuiet: () => {
        if (quietChecked || lease === undefined || !lease.full) return
        quietChecked = true
        queueMicrotask(() => { quietChecked = false; lease?.idled() })
      },
    })
    // A Harness that fails to open gives its instance back: the pool would otherwise keep it for the life of the isolate.
    try {
      const client = new CoreClient(host)
      // After the client's own listener, which replays what the replaced handle had not answered.
      host.onRecycled(() => self.harness?.recycled())
      if (options.conversationCreated) client.onConversationCreated = (tx, record) => options.conversationCreated!(tx as TxProxy, record)
      self.host = host
      const harness = self.harness = new Harness(storage, store, host, client, registry, options)
      harness.timers = timers
      const report = options.onReport ?? (() => {})
      const runtimeHostApi = { client, agent: (id: number, context: Context) => harness.resolveAgent(id, undefined, context), watchDoc: (token: any, ...args: unknown[]) => harness.watchDoc(token, ...args), documentState: (token: any, ...args: unknown[]) => harness.documentState(token, ...args), conversation: async (id: number, binding: InvocationBinding, context: Context) => { const found = await harness.conversation(id, context); return found && boundConversation(found, binding) } }
      const runtime: EffectRuntime = {
        client, registry, models: options.models, env: options.env, report,
        hostApi: runtimeHostApi,
        readTarget: () => self.harness,
        snapshot: () => options.registry.snapshot(),
        phase: self.phases = customTaskRunner({
          registry, hostApi: runtimeHostApi, models: options.models, settings: () => harness.settings, now: () => (options.now ?? Date.now)(), report,
          snapshot: () => options.registry.snapshot(), env: options.env as never, readTarget: () => self.harness,
        }),
        hostHooks: {
          // scheduler.ts #resolve: a custom task's migrate(input, checkpoint, from) runs in the host when the scheduler reserves an older record.
          migrate: (payload: { kind: string; input: unknown; checkpoint: unknown; from: number }) => {
            const definition = registry.task(payload.kind).definition as { version?: number; migrate?: (input: unknown, checkpoint: unknown, from: number) => { input: unknown; checkpoint: unknown } }
            if (typeof definition.migrate !== "function") throw new Error(`Task ${payload.kind} version ${definition.version ?? 1} has no migration from ${payload.from}`)
            const migrated = definition.migrate(payload.input, payload.checkpoint, payload.from)
            return { input: migrated.input, checkpoint: migrated.checkpoint }
          },
          // scheduler.ts #decide (:955): the step line job calls registry.snapshot() synchronously; the reader may run user code (even close()). The core asks the host to call it at that point and refreshes from the answer.
          registrySnapshot: () => registry.snapshot(),
          resolveAgent: payload => agentJSON(resolveAgent(payload.state, options.registry.snapshot(), harness.settings, report), o => registry.id(o)),
          // harness/tool.ts prepare and validate: the arguments, or {error} with the text of what the repair or validation threw.
          prepareArguments: payload => {
            const tool = registry.tool(payload.toolName, payload.handler) as any
            if (tool.prepareArguments === undefined) return { arguments: payload.arguments }
            try { return { arguments: tool.prepareArguments(payload.arguments) } } catch (error) { return { error: errorText(error) } }
          },
          // harness/tool.ts structuredOutputError: the tool's schema is the host's; the answer is {error} or {}.
          checkStructuredOutput: (payload: { toolName: string; result: { structuredOutput?: unknown; isError?: boolean } }) => {
            const error = structuredOutputError(registry.tool(payload.toolName, undefined) as any, payload.result)
            return error === undefined ? {} : { error }
          },
          validateArguments: payload => {
            const tool = registry.tool(payload.toolName, payload.handler) as any
            try { return { arguments: validateToolArguments(tool, { ...payload.call, arguments: payload.arguments }) } } catch (error) { return { error: errorText(error) } }
          },
          // The Storage the caller gave overrides commit (PROTOCOL.md, "Storage gate"): the core applies a commit only after this resolves, and a rejection fails it.
          storageCommit: async (payload: { writes: unknown[] }) => {
            const gated = storage instanceof SqlStorage ? storage : undefined
            if (gated) gated.gating++
            for (const write of payload.writes) substituteWrite(client, write as never)
            admitWrites(client, payload.writes)
            try { await (storage as unknown as { commit(writes: unknown[], context: Context): Promise<unknown> }).commit(payload.writes, BACKGROUND_CONTEXT) } catch (error) { harness.storageFailed(error); throw error } finally { if (gated) gated.gating-- }
            return null
          },
          // A task runtime's context read reaches a Storage that overrides the Pi reads it makes (ctxread.go): one read of the bounds, and the range scan (context.ts scanRange).
          storageRead: async (payload: { method: string; args: unknown[] | Record<string, unknown> }) => {
            const method = (storage as unknown as Record<string, (...args: unknown[]) => Promise<unknown>>)[payload.method]!.bind(storage)
            // The context a handle's read passes to the Storage is its caller's; a read whose caller cancelled fails only that call.
            const first = Array.isArray(payload.args) ? payload.args[0] : undefined
            const conversation = payload.method === "findLatestHeadMarker" ? (first as number | undefined) : payload.method === "scanEntries" ? (first as { conversationId?: number } | undefined)?.conversationId : undefined
            const caller = (conversation === undefined ? undefined : harness.contextReaders.get(conversation)?.[0]) ?? BACKGROUND_CONTEXT
            // session.ts GuardedStorage #call: a read that throws after another call failed the Session gives its caller that failure.
            const read = async (...args: unknown[]) => { try { return await method(...args) } catch (error) { harness.assertHealthy(); if (caller.abortSignal?.aborted !== true) harness.storageReadFailed(error); throw error } }
            // The core's adapter reads send the Storage method's arguments in order (ctxread.go gateRead); a host transaction operation's reads name them (tx.go txStorageRead).
            if (Array.isArray(payload.args)) { await read(...payload.args.map(v => v === null ? undefined : v), caller); return null }
            const a = payload.args
            // session.ts #acquire: the document load of a transaction's first access, and the ID a created document, conversation, entry, task or submission takes.
            if (payload.method === "findDocument") await read(a.address, "current", BACKGROUND_CONTEXT)
            else if (payload.method === "mintId") await read()
            else throw new Error(`storageRead ${payload.method} needs positional arguments`)
            return null
          },
          storageScanRange: async (payload: { query: Record<string, unknown> }) => {
            // context.ts scanRange pages through the Session's guarded Storage (session.ts GuardedStorage #call): a failed or closing Session starts no
            // further page; a page that ends after the Session failed gives its caller the failure, while one underway when close began completes.
            let cursor: unknown
            // The caller's context of this read when a handle made it; a task runtime's read has no caller waiting here.
            const readers = harness.contextReaders.get(payload.query.conversationId as number)
            const caller = readers?.[0] ?? BACKGROUND_CONTEXT
            try {
              do {
                harness.assertReadable()
                cursor = ((await (storage as unknown as { scanEntries(query: unknown, limit: number, cursor: unknown, context: Context): Promise<{ next?: unknown }> }).scanEntries(payload.query, 256, cursor, caller)).next)
                harness.assertHealthy()
              } while (cursor !== undefined)
            } catch (error) {
              if (error instanceof SessionFailed || error instanceof ClosedSession) throw error
              harness.assertHealthy()
              // A read its caller cancelled fails only that call (session.ts GuardedStorage #call).
              if (caller.abortSignal?.aborted !== true) harness.storageReadFailed(error)
              throw error
            }
            return null
          },
          // compaction.ts summarize: runtime.models.completeSimple.
          complete: (payload, context) => {
            const model = options.models.getModel(payload.model.provider, payload.model.modelId)
            if (model === undefined) throw new Error(`Model ${payload.model.provider}/${payload.model.modelId} is not available`)
            return (options.models as any).completeSimple(model, payload.context, { ...payload.options, signal: context.abortSignal })
          },
        },
      }
      Object.assign(handlers, createEffectHandlers(runtime))
      const openPayload = harness.openPayload()
      harness.settingsSent.set(JSON.stringify(openPayload.settings))
      const step = host.sendJSON(EVENT.open, openPayload)
      if (step?.status) throw new RejectedError(step.error?.name, step.error?.message, step.error?.raw ?? "")
      // The scheduler's open commit is a line job and its completion waits for the job's COMMIT hops (and for the host's Storage when it gates commits): Harness.open resolves when it does, and a failure closes the Harness (harness.ts Harness.open :427).
      if (options.mode !== "session") {
        try { await client.api("opened") } catch (error) {
          // harness.ts open: the storage failure that failed open is the cause; the close that fails after it adds no report.
          await harness.close(BACKGROUND_CONTEXT).catch(() => {})
          throw error
        }
      }
      host.beforeSend = harness.syncSettings
      options.registry.subscribe(() => { if (!harness.shut) host.sendJSON(EVENT.registry, harness.registryPayload()) })
      return harness
    } catch (error) {
      host.discard(new FatalError(`Harness.open failed: ${(error as Error)?.message ?? error}`, error))
      throw error
    }
  }

  private settingsWire() {
    const settings = this.settings
    return { ...settings, extensions: settings.extensions?.map((e: { name: string }) => e.name) }
  }

  /** The core reads settings from what it was last sent: send them again before an event when the caller changed them. */
  private syncSettings = () => {
    // A Storage method assigned after open (an instance override, a test's counting wrapper) joins the gate: Pi calls it dynamically.
    const gate = overriddenMethods(this.storage)
    if (gate.length !== this.gate.length || gate.some((name, i) => name !== this.gate[i])) {
      this.gate = gate
      this.host.beforeSend = undefined
      try { this.host.sendJSON(EVENT.registry, { storageGate: gate }) } finally { this.host.beforeSend = this.syncSettings }
    }
    // The field-by-field check proves "unchanged" without serialising; anything it cannot prove falls through to the exact text comparison.
    if (this.settingsSent.unchanged(this.options.settings)) return
    // A settings getter that throws is the consumer's to see (resolveAgent reads it); it must not fail an unrelated event.
    let wire: string
    try { wire = JSON.stringify(this.settingsWire()) } catch { return }
    if (wire === this.settingsSent.text) return
    this.settingsSent.set(wire)
    this.host.beforeSend = undefined
    try { this.host.sendJSON(EVENT.registry, { settings: JSON.parse(wire) }) } finally { this.host.beforeSend = this.syncSettings }
  }

  private registryPayload() { return { registry: this.registry.snapshot() } }

  /** True when no request, wait, transaction or effect depends on the core's memory: the points at which the host may recycle the instance and lose only the cache. */
  get quiet(): boolean { return !this.shut && this.failed === undefined && !this.resumeQueued && this.host.quiet && !this.hasPending }

  /** Requests, transactions and waits the core has not answered. */
  get hasPending(): boolean { return this.client.pending > 0 || this.waiters.size > 0 }

  /** The `open` payload for a replaced handle: the registry and settings as they are now. */
  reopenPayload() {
    const payload = this.openPayload()
    this.settingsSent.set(JSON.stringify(payload.settings))
    return payload
  }

  /**
   * The host replaced the core handle and the new core is open: tell it what only the old one was told, and make up for what the
   * old one still owed. A commit the host applied is in the store, but the `published` notice that announces it follows the commit
   * by a hops chain, and a chain the trapped core had open is gone: the waits that end on such a notice are woken and read the
   * store again (both wait loops do), and a watch's first frame from the new core resyncs the value it holds. The publication
   * stream of `subscribeCommits` has no such repair: a commit applied just before the trap and published just after it reaches
   * its listeners never.
   */
  recycled() {
    for (const w of [...this.waiters]) { this.waiters.delete(w); w.resolve() }
    if (this.commitListeners.size > 0 || this.internalCommitListeners.size > 0) this.setCommitChanges(true)
    this.watches.reattach()
  }

  /** The explicit option, else the contract hooks' override, else the Storage's own profile. */
  private profile(): StorageProfile | undefined { const own = this.storage.profile
    return this.options.storageProfile ?? (own === undefined ? undefined : profileOverride?.[own] ?? own) }

  private openPayload() {
    // The catalogue entries the core may name: by default every model of the `models` object, as {provider, id, contextWindow, maxTokens}.
    const models = this.options.modelsInUse ? this.options.modelsInUse(this.options.models)
      : (this.options.models.getModels?.() ?? []).map(({ provider, id, contextWindow, maxTokens }) => ({ provider, id, contextWindow, maxTokens }))
    return {
      settings: this.settingsWire(),
      mode: this.options.mode ?? "harness", models, registry: this.registry.snapshot(), sidecar: this.options.sidecar ?? "index", delta: true,
      hasEnv: this.options.env !== undefined,
      ...(this.gate.length === 0 ? {} : { storageGate: this.gate }),
      ...(this.profile() === undefined ? {} : { storageProfile: this.profile() }),

    }
  }

  private onNotice(kind: number, payload: Uint8Array) {
    // A failure the core absorbed (a throwing section or environment, a rejected hook) reaches HarnessOptions.onReport.
    if (kind === NOTICE.report) {
      ;(this.options.onReport ?? (() => {}))(reviveError(JSON.parse(decoder.decode(payload)), this.client.storageFailure))
      this.closeAfterFailure()
      return
    }
    if (kind !== NOTICE.published) return
    const body = JSON.parse(decoder.decode(payload)) as PublishedNotice
    if ((body.tasks?.length ?? 0) > 0) this.taskPublications++
    if (body.changes !== undefined && body.seq !== undefined) recordPublication(this.client, body.seq, body.changes)
    if (body.watchId !== undefined) this.watches.deliver(body.watchId, body.frame as { value: unknown; ops: never[] })
    if (body.changes !== undefined) { for (const change of body.changes as never[]) substitutePublished(this.client, change); adoptTables(this.client, body.changes as never) }
    if (body.changes !== undefined) {
      const publication = { seq: body.seq, changes: body.changes }
      // session.ts #publish: the Session's own components run first, and a throw leaves their state behind storage, so it fails the Session; a host listener's throw is reported.
      for (const l of [...this.internalCommitListeners]) { try { l(publication, BACKGROUND_CONTEXT) } catch (error) { this.failedByListener(error) } }
      for (const l of [...this.commitListeners]) contained(() => l(publication, BACKGROUND_CONTEXT), error => this.report(error))
    }
    for (const w of [...this.waiters]) if (w.match(body)) { this.waiters.delete(w); w.resolve() }
    // submissions.ts #observe: a settled submission resolves the waits registered for it.
    for (const s of body.submissions ?? []) if (s.id !== undefined && s.status !== "queued" && s.status !== "placed") { const record = this.submissionRecord(s.id); if (record !== undefined) this.submissionWaiters.resolve(s.id, record) }
  }

  /**
   * session.ts GuardedStorage #call: a Storage method of the caller's threw, so the Session fails. The first error wins; the call that
   * hit it rejects with it (errors.ts reviveError), later calls and waits reject SessionFailed, watches end session_failed, and the
   * Session closes, ending `closed` with `failed`. A read the caller's request got wrong (StorageRequestError) leaves the Session open.
   */
  private storageFailed(error: unknown) {
    if (this.failure !== undefined) return
    this.failure = { error }
    this.client.storageFailure = error instanceof Error ? error : new Error(errorMessage(error))
    const failed = new SessionFailed(error)
    this.failed = failed
    for (const w of [...this.waiters]) w.reject(failed)
    this.waiters.clear()
    this.submissionWaiters.rejectAll(failed)
    this.client.failWaits(failed)
    // session.ts fail: close listeners run at the failure, so a watch ends session_failed before its invocation's signal aborts it.
    this.watches.closeSession(this.failure)
    // The Session closes once the core has taken the failure (its report notice): sealing it first would answer what is in
    // flight with "Harness is closed" where Pi answers SessionFailed. A Session has no scheduler to report, so it closes after the failing step.
    if (this.options.mode === "session") this.closeAfterFailure()
  }

  private closeAfterFailure() {
    if (this.failure === undefined || this.closing !== undefined) return
    // After the step that carried the report has delivered all of its notices, so the waits the core ended with SessionFailed reach their callers.
    // A Storage that cannot close adds no report: the failure that closes the Session stays the cause (session.ts close).
    setTimeout(() => this.close(BACKGROUND_CONTEXT).catch(() => {}), 0)
  }

  /** session.ts assertUsable: SessionFailed once the Session failed, else an error once it is closing. */
  assertReadable() {
    this.assertHealthy()
    if (this.closing !== undefined || this.shut) throw new ClosedSession()
  }

  /** session.ts GuardedStorage #assertHealthy: SessionFailed once the Session failed. */
  assertHealthy() {
    if (this.failure !== undefined) throw new SessionFailed(this.failure.error)
  }

  /**
   * A Storage read the host made off the Session line threw (a context read's scan, session.ts GuardedStorage #call): the Session fails, reports the error
   * once and closes. A read the caller's request got wrong (StorageRequestError) leaves it open. The core learns of a failed commit through its own report
   * notice; a read it does not make leaves it to the host.
   */
  private storageReadFailed(error: unknown) {
    if (error instanceof StorageRequestError) return
    const first = this.failure === undefined
    this.storageFailed(error)
    if (!first) return
    ;(this.options.onReport ?? (() => {}))(error as Error)
    this.closeAfterFailure()
  }

  /** The host discarded the core session: waits for publications can never end, so they reject with the cause. */
  private fail(error: Error) {
    if (this.failed) return
    this.failed = error
    for (const w of [...this.waiters]) w.reject(error)
    this.waiters.clear()
    this.submissionWaiters.rejectAll(error)
    // The discard that ends an orderly close is no failure to report (host.ts closeAndJoin); anything else the host discarded for is.
    if (this.shut && error.message === "Harness is closed") return
    ;(this.options.onReport ?? (() => {}))(error)
  }

  private waitPublished(match: (n: PublishedNotice) => boolean, context: Context): Promise<void> {
    if (this.failed) return Promise.reject(this.failed)
    return awaitWithContext(new Promise<void>((resolve, reject) => this.waiters.add({ match, resolve, reject })), context)
  }

  /** What an operation on a shut Harness rejects with: SessionFailed once the Session failed (session.ts assertUsable checks the failure before the close), else the closed error. */
  private closedError(): Error {
    return this.failure !== undefined ? new SessionFailed(this.failure.error) : new Error("Harness is closed")
  }

  assertOpen() {
    if (this.failure !== undefined) throw new SessionFailed(this.failure.error)
    if (this.shut) throw new Error("Harness is closed")
  }

  // Session

  commit<T>(change: (tx: TxProxy) => T | Promise<T>, context: Context): Promise<T> { return this.commitWith(change, context) }
  /** Internal, for tests that bound the scheduler's memory: the ownership index sizes the core last reported (harness.ts SCHEDULER_INDEX_SIZES). */
  get [Symbol.for("pi-durable.schedulerIndexSizes")]() { return this.client.indexSizes }
  /** Internal, for tests: the document cache sizes the core last reported (session.ts documentCacheSizes is synchronous). */
  get documentCacheSizes() { return this.client.docCache }
  /** Internal: run a read-only job on the mutation line (session.ts readOnLine): a commit that writes nothing holds the line for it. */
  async readOnLine<T>(job: () => Promise<T>): Promise<T> {
    let result!: T
    await this.commitWith(async () => { result = await job() }, BACKGROUND_CONTEXT)
    return result
  }

  // Not async: session.ts commitWith :161-168 rejects an unusable Session with Promise.reject and otherwise returns #enqueue's promise itself.
  commitWith<T>(change: (tx: TxProxy) => T | Promise<T>, context: Context, binding: { conversationId?: number } = {}): Promise<T> {
    try { this.assertOpen() } catch (error) { return Promise.reject(error) }
    return invocationCommit({ client: this.client, failed: () => { if (this.failure !== undefined) throw new SessionFailed(this.failure.error) } }, binding)(change, context)
  }

  /**
   * SessionImpl.unloadDocuments: Pi drops the loaded trackers on the mutation line. The call waits its turn on the line (an empty commit
   * queues behind earlier work), the core drops its loaded documents, and the host forgets the latest document values it shares
   * between reads, so the next read is a fresh load.
   */
  async unloadDocuments(): Promise<void> {
    await this.commitWith(() => undefined as never, BACKGROUND_CONTEXT)
    await this.client.api("unloadDocuments", {})
    forgetSnapshots(this.client)
    forgetColdLoads(this.client)
  }

  /** The first close starts the shutdown; later closes await that same shutdown (harness.ts close). */
  close(context: Context): Promise<void> {
    // A cancelled caller stops waiting; the shutdown goes on without its signal (session.ts close: the Storage close takes the context minus its abort signal).
    return awaitWithContext(this.closing ??= this.shutdown(withoutAbortSignal(context)), context)
  }
  private closing?: Promise<void>
  /** The callers' contexts of the handle context reads underway, by conversation, oldest first: the range scan the core asks for runs with its caller's context (context.ts readContextFrom). */
  readonly contextReaders = new Map<number, Context[]>()
  private async shutdown(context: Context): Promise<void> {
    this.shut = true
    // Pi seals admission synchronously in close() (session.ts:352-366: close listeners, scheduler #seal); the terminal close event follows once caller transactions end.
    this.host.seal()
    for (const l of [...this.closeListeners]) contained(l, error => this.report(error))
    this.watches.closeSession(this.failure)
    for (const w of [...this.waiters]) w.reject(this.failed ?? new Error("Harness is closed"))
    this.waiters.clear()
    this.submissionWaiters.rejectAll(this.failed ?? new Error("Harness is closed"))
    // Admitted work settles before the store closes (session.ts close enqueues the Storage close behind the line); the
    // close event is final, so open transactions end first.
    if (this.client.busy) await this.client.idle()
    await this.host.closeAndJoin()
    this.client.close()
    if (this.timers instanceof TimeoutTimers) this.timers.dispose()
    // Session.close awaits the Storage close with the caller's context; a close that fails rejects this one. A failing close of the Storage
    // fails the Session unless it failed already (session.ts close), and `closed` settles after the Storage is closed.
    try { await (this.storage.close as ((context: Context) => unknown) | undefined)?.call(this.storage, context) } catch (error) {
      // A Storage that cannot close fails the Session and reports it, unless an earlier failure already did: that one stays the cause.
      if (this.failure === undefined) {
        this.failure = { error }
        ;(this.options.onReport ?? (() => {}))(error as Error)
      }
      throw error
    } finally {
      this.end.resolve(this.failure === undefined ? { reason: "closed" } : { reason: "failed", error: this.failure.error })
    }
  }
  /** session.ts report: a failure or a listener's error goes to HarnessOptions.onReport, which never fails what ran it. */
  report(error: unknown) { contained(() => this.options.onReport?.(error as Error), () => {}) }

  /** A Session component's listener threw: the Session fails, reports the error once and closes. */
  private failedByListener(error: unknown) {
    const first = this.failure === undefined
    this.storageFailed(error)
    if (!first) return
    this.report(error)
    this.closeAfterFailure()
  }

  /** session.ts observeCommits: `subscribeCommits` for the Session's own components, run before the host's listeners. */
  observeCommits(listener: (publication: unknown, context: Context) => void) {
    this.internalCommitListeners.add(listener)
    this.setCommitChanges(true)
    return () => { this.internalCommitListeners.delete(listener) }
  }

  /** The core attaches the complete publication to each commit notice while at least one listener is subscribed. */
  subscribeCommits(listener: (publication: unknown, context: Context) => void) {
    this.commitListeners.add(listener)
    if (this.commitListeners.size === 1) this.setCommitChanges(true)
    return () => {
      if (this.commitListeners.delete(listener) && this.commitListeners.size === 0 && this.internalCommitListeners.size === 0) this.setCommitChanges(false)
    }
  }
  private setCommitChanges(on: boolean) { if (!this.shut) this.client.api("commits", { on }).catch(() => {}) }
  subscribeClose(listener: () => void) { this.closeListeners.add(listener); return () => { this.closeListeners.delete(listener) } }
  snapshot = (...args: unknown[]) => (documentReader(this.client).snapshot as (...a: unknown[]) => unknown)(...args)
  snapshotAsOf = (...args: unknown[]) => (documentReader(this.client).snapshotAsOf as (...a: unknown[]) => unknown)(...args)
  /** `documentState(token, ...owner, [key], context)`: disposable read-only Chord state of the committed document, or undefined when absent. */
  documentState(token: AnyDocToken, ...args: unknown[]) {
    const { address, nextArgument } = resolveAddress(token.definition, args)
    return this.watches.state({ kind: "doc", address: wireAddress(address), def: wireDefinition(token.definition) }, args[nextArgument] as Context)
  }
  /** `watchDoc(token, ...owner, [key], context)`: the serialized exact-frame watch of a document, or undefined when absent. */
  watchDoc(token: AnyDocToken, ...args: unknown[]) {
    const { address, nextArgument } = resolveAddress(token.definition, args)
    return this.watches.watch({ kind: "doc", address: wireAddress(address), def: wireDefinition(token.definition) }, args[nextArgument] as Context)
  }

  // Harness

  resume(): void {
    this.assertOpen()
    // Every call kicks: scheduler.ts resume() :267-270 sets dirty and, when no drain is queued or running, queues a drain whose reserve pass is a Session line job. Calls in one synchronous section share the drain queued by the first (#kick returns while #draining), so only the first sends the event; skipping later sections would leave the line idle where Pi's holds it.
    if (this.resumeQueued) return
    this.resumeQueued = true
    // scheduler.ts #kick drains in a microtask: what the caller does synchronously after resume() reads the state before any task starts.
    queueMicrotask(() => { this.resumeQueued = false; if (!this.shut && this.host.discarded === undefined) this.host.send(EVENT.resume) })
  }

  /** harness.ts wake :282-285: assertUsable, then scheduler.ts wake(), synchronous and from memory. The core answers api `wake` inside the send, so the reply is read as it arrives. */
  wake(): { active: boolean; at?: number } {
    this.assertOpen()
    let wake: { active: boolean; at?: number } | undefined
    this.client.apiNow<{ active: boolean; at?: number }>("wake", {}, value => (wake = value))
    if (wake === undefined) throw this.failure !== undefined ? new SessionFailed(this.failure.error) : new Error("the core did not answer wake at once")
    return wake
  }

  /** harness.ts retryTask :324-327: assertUsable (a synchronous throw), then scheduler.ts retry(): a commit on the Session line that clears a parked task's restart count (an error for an unknown task), then resume(). The promise is the core's answer as it is. */
  retryTask(id: number, context: Context): Promise<void> {
    this.assertOpen()
    return awaitWithContext(this.client.api<void>("retryTask", { taskId: id }, () => undefined), context)
  }

  // Not async: harness.ts root :314-319 and createConversation :326-329 return #create's promise itself.
  root(context: Context, options: { agent?: unknown; init?: (tx: TxProxy, id: number) => unknown } = {}): Promise<Conversation> {
    return this.create({ kind: "root" }, options, context)
  }

  createConversation(options: { ownership: unknown; agent?: unknown; init?: (tx: TxProxy, id: number) => unknown }, context: Context): Promise<Conversation> {
    return this.create({ kind: "independent", ownership: options.ownership }, options, context)
  }

  async conversation(id: number, context: Context): Promise<Conversation | undefined> {
    this.assertOpen()
    const record = await awaitWithContext(this.client.api("conversation", { conversationId: id }), context)
    return record === null || record === undefined ? undefined : new Conversation(this, id)
  }

  async create(target: { kind: "root" } | { kind: "independent"; ownership: unknown } | { kind: "fork"; parentId: number; at: number; ownership: unknown }, options: { agent?: unknown; init?: (tx: TxProxy, id: number) => unknown }, context: Context): Promise<Conversation> {
    this.assertOpen()
    const id = await this.commitWith(async tx => {
      if (target.kind === "root" && (await tx.conversation(ROOT_CONVERSATION_ID)) !== undefined) return ROOT_CONVERSATION_ID
      const record: { id: number } = target.kind === "root" ? await tx.createRootConversation() : target.kind === "fork"
        ? await tx.forkConversation(target.parentId, target.at, { ownership: target.ownership })
        : await tx.createConversation({ ownership: target.ownership })
      if (options.agent !== undefined) await configure(tx, record.id, options.agent)
      if (options.init !== undefined) await options.init(tx, record.id)
      return record.id
    }, context)
    return new Conversation(this, id)
  }

  /** harness.ts getTask :243: `readOnLine(() => storage.task(id))`, a Session line job (api getTask) whose promise the caller gets as it is. */
  getTask(id: number, context: Context = BACKGROUND_CONTEXT): Promise<any | undefined> {
    try { this.assertOpen() } catch (error) { return Promise.reject(error) }
    return awaitWithContext(this.client.api<any>("getTask", { taskId: id }, record => record ?? undefined), context)
  }

  submissionRecord(id: number): any | undefined {
    const rows = this.store.all("SELECT record FROM submissions WHERE id = ?", [id])
    return rows[0] ? JSON.parse(String(rows[0][0])) : undefined
  }

  /** submissions.ts get :63-66: `record = await readOnLine(() => storage.submission(id))`, a Session line job (api submission). */
  async submission(id: number, context: Context = BACKGROUND_CONTEXT): Promise<Submission | undefined> {
    this.assertOpen()
    const record = await awaitWithContext(this.client.api<any>("submission", { submissionId: id }), context)
    return record === null || record === undefined ? undefined : new Submission(this, id)
  }

  /** harness.ts inspect :249: the readOnLine promise itself, so the caller resumes when it settles. HarnessInspection; a blocked task's `error` crosses as {name, message} and is an Error here. */
  inspect(context: Context): Promise<unknown> {
    return awaitWithContext(this.client.inspect<{ tasks: { state: { error?: { name: string; message: string } } }[] }>({ query: "harness" }, inspection => {
      for (const { state } of inspection.tasks) if (state.error !== undefined) state.error = reviveError(state.error)
      return inspection
    }), context)
  }

  abortSubmission(id: number, context: Context, conversationId?: number): Promise<"aborted" | "already_placed" | "settled" | "not_found"> {
    // Submissions.abort does not start scheduling (submissions.ts abort).
    return awaitWithContext(this.client.abort<"aborted" | "already_placed" | "settled" | "not_found">({ submissionId: id, ...(conversationId === undefined ? {} : { conversationId }), ...(invocationOf(context) === undefined ? {} : { invocation: invocationOf(context) }) }), context)
  }

  async abortTask(id: number, context: Context) {
    // Harness.abortTask does not start scheduling (harness.ts abortTask); Conversation.abort does.
    return awaitWithContext(this.client.abort<"marked" | "terminal">({ taskId: id }), context)
  }

  /** harness.ts :275-278: the scheduler's own resume never fails (a closed Harness is rejected by the core's answer) and the promise is returned as it is. */
  waitForTask(id: number, context: Context) { try { this.resume() } catch { /* rejected by the core's answer */ } return awaitWithContext(this.client.api("waitForTask", { taskId: id }), context) }
  waitForIdle(context: Context): Promise<void> { return this.waitIdle(undefined, context) }

  /** scheduler.ts waitForIdle: idle returns an already-resolved promise (the caller's await takes one pass), a closed Harness rejects, otherwise the core's waiter settles with the scope's last live task and the caller's context cancels only this wait. */
  waitIdle(conversationId: number | undefined, context: Context): Promise<void> {
    // The scheduler's own resume never fails (scheduler.ts resume); only Harness.resume asserts open.
    try { this.resume() } catch { /* a closed Harness is rejected by the core's answer below */ }
    const { promise, settled } = this.client.apiNow<void>("waitForIdle", conversationId === undefined ? {} : { conversationId })
    return settled ? promise : awaitWithContext(promise, context)
  }
  async usage(context: Context) { return awaitWithContext(this.client.api("usage"), context) }
  taskGraph(context: Context) { return this.watches.state({ kind: "graph" }, context) }
  watchTaskGraph(context: Context) { return this.watches.watch({ kind: "graph" }, context) }
  /** The view state and watch of one conversation. */
  /** The core's view of a conversation with a custom observer (watchEvents): every commit that touches it reaches `publication` with the commit's records. */
  attachView(conversationId: number, context: Context, make: Parameters<WatchHub["attach"]>[2]) {
    return this.watches.attach({ kind: "view", conversationId, changes: true, completing: true }, context, make)
  }

  /** Generation tasks of a conversation in `completing`: the turns whose end watchEvents already counted. */
  completingGenerations(conversationId: number): number[] {
    return this.store.all("SELECT id FROM tasks WHERE conversation_id = ? AND kind = '\"pi.generation\"' AND status = 'completing'", [conversationId]).map(row => Number(row[0]))
  }

  conversationState(conversationId: number, context: Context) { return this.watches.state({ kind: "view", conversationId }, context) }
  watchConversation(conversationId: number, context: Context) { return this.watches.watch({ kind: "view", conversationId }, context) }

  /** Resolve when the ordinary ownership scope of `conversationId` (or of every ownerless conversation) has no live non-background task. */
  async idle(conversationId: number | undefined, context: Context): Promise<void> {
    for (;;) {
      // A task publication that lands while the graph read is in flight must not be missed: read it again instead of waiting.
      const seen = this.taskPublications
      const graph = await awaitWithContext(this.client.api<{ tasks: Record<string, { conversationId: number; owner?: number; background: boolean; conversations: number[] }> }>("taskGraph"), context)
      if (!liveInScope(graph.tasks, conversationId)) return
      if (this.taskPublications !== seen) continue
      await this.waitPublished(n => (n.tasks?.length ?? 0) > 0, context)
    }
  }

  /** Agent of a conversation, resolved in the host against the live registry (agent.ts). */
  async resolveAgent(conversationId: number, at: number | undefined, context: Context) {
    const state = at === undefined ? await this.snapshot(AgentDoc, conversationId, context) : await this.snapshotAsOf(AgentDoc, conversationId, at, context)
    return resolveAgent(state as AgentState | undefined, this.options.registry.snapshot(), this.settings, this.options.onReport ?? (() => {}))
  }

  // Submissions (spec section 6): the core admits.
  submit(conversationId: number, draft: any, context: Context): Promise<Submission> {
    // submissions.ts submit :54-60 is `async`: a closed Harness rejects.
    try { this.assertOpen(); this.resume() } catch (error) { return Promise.reject(error) }
    // The core answers the submit by the client's request id; a requestId reaches the record only when the caller gave one, as in pi-durable.
    // The handle is built as the answer routes: that is the tick Pi's `submit` resumes from `await commitWith` and returns, so the caller resumes one tick later as it does in Pi.
    const invocation = invocationOf(context)
    return awaitWithContext(this.client.submit({ conversationId, ...draft, ...(invocation === undefined ? {} : { invocation }) }, placed => new Submission(this, placed.id)), context)
  }

  compactTask(conversationId: number, instructions: string | undefined, context: Context): Promise<number> {
    // A closed Harness rejects (a promise), it does not throw: resume() is the only synchronous part that can.
    try { this.resume() } catch (error) { return Promise.reject(error) }
    const input = { reason: "manual", ...(instructions === undefined ? {} : { instructions }) }
    return this.commitWith(tx => tx.createCompaction(conversationId, input), context)
  }

  private async waitSubmissionThrough(id: number, context: Context): Promise<any> {
    this.resume()
    const record = await (this.storage as unknown as { submission(id: number, context: Context): Promise<unknown> }).submission(id, context)
    if (record === undefined) throw new Error(`Submission ${id} does not exist`)
    // Close rejects registered waiters synchronously and may begin during the read.
    if (this.shut) throw this.closedError()
    return this.waitSubmission(id, context, true)
  }

  waitSubmission(id: number, context: Context, read = false): Promise<any> {
    if (!read && this.shut) return Promise.reject(this.closedError())
    // A Storage that overrides `submission` is read through it on the line, and a close that begins during the read rejects the wait (submissions.ts wait).
    if (!read && this.gate.includes("submission")) return this.waitSubmissionThrough(id, context)
    return this.waitSubmissionOnLine(id, context)
  }

  /** submissions.ts wait :75-86: `resume(); found = await readOnLine(read the record; settled -> {promise: Promise.resolve(record)}; else register); return found.promise`. The read is a Session line job in the core (api submission); the facade code keeps Pi's shape so the passes after the job are Pi's. */
  private async waitSubmissionOnLine(id: number, context: Context): Promise<any> {
    this.resume()
    const record = await awaitWithContext(this.client.api<any>("submission", { submissionId: id, wait: true }), context)
    if (record === null || record === undefined) throw new Error(`Submission ${id} does not exist`)
    const isSettled = (r: any) => r.status !== "queued" && r.status !== "placed"
    if (isSettled(record)) return { promise: Promise.resolve(record) }.promise
    // Close rejects registered waiters synchronously and may begin during the read.
    if (this.shut) throw this.closedError()
    // A settling publication between the read and this registration is in the host's store already.
    const settledSince = this.submissionRecord(id)
    if (settledSince !== undefined && isSettled(settledSince)) return settledSince
    return this.submissionWaiters.add(id, context)
  }
}

function liveInScope(tasks: Record<string, { conversationId: number; owner?: number; background: boolean; conversations: number[] }>, root: number | undefined): boolean {
  const nodes = Object.values(tasks)
  // A background task and everything it owns is outside every ordinary scope (scheduler.ts #inScope stops at a background task).
  const background = (task: { owner?: number; background: boolean } | undefined): boolean => {
    for (let t = task; t !== undefined; t = t.owner === undefined ? undefined : tasks[String(t.owner)]) if (t.background) return true
    return false
  }
  const scope = new Set<number>()
  const visit = (conversationId: number) => {
    if (scope.has(conversationId)) return
    scope.add(conversationId)
    for (const t of nodes) if (t.conversationId === conversationId && !background(t)) for (const c of t.conversations) visit(c)
  }
  if (root !== undefined) visit(root)
  else {
    const owned = new Set(nodes.flatMap(t => t.conversations))
    for (const t of nodes) if (!owned.has(t.conversationId)) visit(t.conversationId)
  }
  return nodes.some(t => !background(t) && scope.has(t.conversationId))
}


export class Submission {
  readonly id: number
  private readonly harness: Harness
  constructor(harness: Harness, id: number) { this.harness = harness; this.id = id }
  /** submissions.ts status :68-72: `record = await readOnLine(() => storage.submission(id))`, then Pi's error when it is absent. */
  async status(context: Context = BACKGROUND_CONTEXT) {
    this.harness.assertOpen()
    const record = await awaitWithContext(this.harness.client.api<any>("submission", { submissionId: this.id }), context)
    if (record === null || record === undefined) throw new Error(`Submission ${this.id} does not exist`)
    return record
  }
  wait(context: Context) { return this.harness.waitSubmission(this.id, context) }
  abort(context: Context) { return this.harness.abortSubmission(this.id, context) }
}

export class Conversation {
  readonly id: number
  private readonly harness: Harness
  constructor(harness: Harness, id: number) { this.harness = harness; this.id = id }
  agent(context: Context) { return this.harness.resolveAgent(this.id, undefined, context) }
  configure(change: unknown, context: Context) { return this.harness.commitWith(tx => configure(tx, this.id, change), context) }
  submit(draft: unknown, context: Context) { return this.harness.submit(this.id, draft, context) }
  compact(instructions: string | undefined, context: Context) { return this.harness.compactTask(this.id, instructions, context) }
  async reset(handoff: string | undefined, context: Context) {
    const model = handoff === undefined ? {} : { model: [{ role: "user", content: handoff, timestamp: this.harness.now() }] }
    await this.harness.submit(this.id, { type: "write", entry: { kind: "pi.reset", head: "self", ...model } }, context)
  }
  commit<T>(change: (tx: TxProxy) => T | Promise<T>, context: Context) { return this.harness.commitWith(change, context, { conversationId: this.id }) }
  /** The model context; `{at}` reads it as of that entry, as a fork at it would start. */
  context(context: Context, options?: { at?: number }) {
    const readers = this.harness.contextReaders
    const list = readers.get(this.id) ?? []
    readers.set(this.id, list)
    list.push(context)
    const done = () => { list.splice(list.indexOf(context), 1); if (list.length === 0) readers.delete(this.id) }
    const read = this.harness.client.api("context", { conversationId: this.id, ...(options?.at === undefined ? {} : { at: options.at }) })
    read.then(done, done)
    return awaitWithContext(read, context)
  }
  /** harness.ts :127-139: `readOnLine(() => storage.scanEntries(bounded, limit, cursor, context))`, a Session line job (api scanEntries) whose promise the caller gets as it is. */
  entries(query: Record<string, unknown>, limit: number, cursor: unknown, context: Context) {
    return awaitWithContext(this.harness.client.api("scanEntries", { query: { ...query, conversationId: this.id }, limit, ...(cursor === undefined ? {} : { cursor }) }), context)
  }
  fork(at: number, options: { ownership: unknown; agent?: unknown; init?: (tx: TxProxy, id: number) => unknown }, context: Context) {
    return this.harness.create({ kind: "fork", parentId: this.id, at, ownership: options.ownership }, options, context)
  }
  async abort(context: Context, options?: { background?: boolean }) {
    this.harness.resume()
    await awaitWithContext(this.harness.client.abort({ conversationId: this.id, ...(options?.background ? { background: true } : {}), ...(invocationOf(context) === undefined ? {} : { invocation: invocationOf(context) }) }), context)
    await this.harness.idle(this.id, context)
  }
  waitForIdle(context: Context) { return this.harness.waitIdle(this.id, context) }
  viewState(context: Context) { return this.harness.conversationState(this.id, context) }
  watch(context: Context) { return this.harness.watchConversation(this.id, context) }
}

/**
 * The handle an invocation gets for a conversation (harness.ts boundConversation): every operation, and every operation of
 * a submission it returns, first checks the invocation and runs under its signal, so it rejects once the invocation ends;
 * admitted work stays durable.
 */
function boundConversation(conversation: Conversation, binding: InvocationBinding) {
  const bind = (context: Context): Context => bindContext(binding, context)
  const bound = <T>(operation: (context: Context) => Promise<T>) => async (context: Context): Promise<T> => { binding.check(); return operation(bind(context)) }
  return {
    id: conversation.id,
    submit: async (draft: unknown, context: Context) => {
      binding.check()
      const submission = await conversation.submit(draft, bind(context))
      return {
        id: submission.id,
        status: bound(callContext => submission.status(callContext)),
        wait: bound(callContext => submission.wait(callContext)),
        abort: bound(callContext => submission.abort(callContext)),
      }
    },
    abort: async (context: Context, options?: { background?: boolean }) => { binding.check(); return conversation.abort(bind(context), options) },
    waitForIdle: bound(callContext => conversation.waitForIdle(callContext)),
  }
}

export { invocationApi, applyChange, runCommit }
