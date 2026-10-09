// The pi-durable public API over the Durable core (src/harness/harness.ts, src/session/session.ts). Every method is one channel
// of the ABI: a `submit` or `abort` event, an `api` request, a transaction, or a read of Pi's own tables, which the host serves
// directly (CONTRACT section 3.10). Extension code, resolution of agents and wraps, hooks, tools and custom tasks run here;
// admission, scheduling, context derivation and the store format run in the core.
import { BACKGROUND_CONTEXT, awaitWithContext } from "@earendil-works/chord/context"
import type { Context } from "@earendil-works/chord"
import { EVENT, NOTICE } from "../abi.ts"
import { CoreClient } from "../client.ts"
import { createEffectHandlers, type EffectRuntime, type ModelsLike } from "../effects.ts"
import { HostSession, RejectedError, type CoreHandle } from "../host.ts"
import { HostRegistry, type HostRegistrySource } from "../registry.ts"
import type { PublishedNotice } from "../protocol.ts"
import type { Store } from "../store.ts"
import { WasmCore } from "../wasm.ts"
import { AgentDoc, agentJSON, applyChange, configure, resolveAgent, resolveSettings, type AgentState } from "./agent.ts"
import { documentReader, invocationApi, invocationCommit } from "./hostapi.ts"
import { runCommit, TxProxy } from "./tx.ts"
import type { Storage } from "./storage.ts"
import { CompactionTask } from "./define.ts"
import { reviveError } from "../errors.ts"

export const ROOT_CONVERSATION_ID = 1
const decoder = new TextDecoder()

/** A compiled core module, or any object that serves ABI sessions: the Wasm core, the TypeScript control, a native binding. */
export type CoreLike = { newSession(): CoreHandle; readonly sqlTable: string[] }
export type CoreSource = WebAssembly.Module | CoreLike
let defaultModule: CoreSource | undefined
/** Choose the core every Harness opened without an explicit `core` option uses. A Worker calls this once at module scope with its imported Wasm module. */
export function useCore(module: CoreSource) { defaultModule = module }

export interface FacadeOptions {
  models: ModelsLike
  registry: HostRegistrySource
  settings?: any
  env?: EffectRuntime["env"]
  conversationCreated?: (tx: TxProxy, record: any) => unknown | Promise<unknown>
  now?: () => number
  onReport?: (error: unknown) => void
  /** PiG addition: the compiled core module (default: `useCore()`). */
  core?: CoreSource
  /** PiG addition: `session` opens the tables-and-documents Session without the Harness (createSession). */
  mode?: "session" | "harness"
  /** PiG addition: the sidecar mode of ADR D6. Default `index`. */
  sidecar?: "off" | "index" | "snapshot"
  /** PiG addition: model catalogue entries the core may name; default every model `models` lists. */
  modelsInUse?: (models: ModelsLike) => unknown[]
}

type Waiter = { match: (n: PublishedNotice) => boolean; resolve: () => void }

export class Harness {
  readonly host: HostSession
  readonly client: CoreClient
  readonly registry: HostRegistry
  private readonly options: FacadeOptions
  private readonly store: Store
  private readonly storage: Storage
  private readonly settings: ReturnType<typeof resolveSettings>
  private readonly waiters = new Set<Waiter>()
  private readonly commitListeners = new Set<(p: unknown, c: Context) => void>()
  private readonly closeListeners = new Set<() => void>()
  private resumed = false
  private closed = false
  private readonly now: () => number

  private constructor(storage: Storage, store: Store, host: HostSession, client: CoreClient, registry: HostRegistry, options: FacadeOptions) {
    this.storage = storage; this.store = store; this.host = host; this.client = client; this.registry = registry; this.options = options
    this.settings = resolveSettings(options.settings)
    this.now = options.now ?? Date.now
  }

  /** Open a Harness over a storage: a cold open of the core (ABI section 5, `open`). */
  static async open(storage: Storage, options: FacadeOptions, _context: Context = BACKGROUND_CONTEXT): Promise<Harness> {
    const module = options.core ?? defaultModule
    if (!module) throw new Error("Harness.open needs a core: pass options.core or call useCore(module) first")
    const store = await storage.open()
    const core: CoreLike = module instanceof WebAssembly.Module ? new WasmCore(module) : module
    const registry = new HostRegistry(options.registry)
    const handlers: Record<number, any> = {}
    const self: { harness?: Harness } = {}
    const host = new HostSession({
      core: core.newSession(), sqlTable: core.sqlTable, store, clock: options.now, handlers,
      onNotice: n => self.harness?.onNotice(n.kind, n.payload),
      onRejected: error => (options.onReport ?? (() => {}))(error),
    })
    const client = new CoreClient(host)
    const harness = self.harness = new Harness(storage, store, host, client, registry, options)
    const report = options.onReport ?? (() => {})
    const runtime: EffectRuntime = {
      client, registry, models: options.models, env: options.env, report,
      hostApi: { client, conversation: (id, context) => harness.conversation(id, context) },
      snapshot: () => options.registry.snapshot(),
      hostHooks: {
        resolveAgent: payload => agentJSON(resolveAgent(payload.state, options.registry.snapshot(), harness.settings, report), o => registry.id(o)),
        conversationCreated: async () => undefined,
      },
      callbacks: {
        conversationCreated: async (payload, channel) => options.conversationCreated?.(new TxProxy(channel), payload.conversation),
      },
    }
    Object.assign(handlers, createEffectHandlers(runtime))
    const step = host.sendJSON(EVENT.open, harness.openPayload())
    if (step?.status) throw new RejectedError(step.error?.name, step.error?.message, step.error?.raw ?? "")
    options.registry.subscribe(() => { if (!harness.closed) host.sendJSON(EVENT.registry, harness.registryPayload()) })
    return harness
  }

  private registryPayload() { return { registry: this.registry.snapshot(), ...(this.resumed ? { resume: true } : {}) } }

  private openPayload() {
    const models = this.options.modelsInUse ? this.options.modelsInUse(this.options.models) : []
    return {
      settings: { ...this.settings, extensions: this.settings.extensions?.map((e: { name: string }) => e.name) },
      mode: this.options.mode ?? "harness", models, registry: this.registry.snapshot(), sidecar: this.options.sidecar ?? "index", delta: true,
      hasEnv: this.options.env !== undefined, hasConversationCreated: this.options.conversationCreated !== undefined,
    }
  }

  private onNotice(kind: number, payload: Uint8Array) {
    if (kind !== NOTICE.published) return
    const body = JSON.parse(decoder.decode(payload)) as PublishedNotice
    for (const l of [...this.commitListeners]) l(body, BACKGROUND_CONTEXT)
    for (const w of [...this.waiters]) if (w.match(body)) { this.waiters.delete(w); w.resolve() }
  }

  private waitPublished(match: (n: PublishedNotice) => boolean, context: Context): Promise<void> {
    return awaitWithContext(new Promise<void>(resolve => this.waiters.add({ match, resolve })), context)
  }

  private assertOpen() { if (this.closed) throw new Error("Harness is closed") }

  // Session

  commit<T>(change: (tx: TxProxy) => T | Promise<T>, context: Context): Promise<T> { return this.commitWith(change, context) }

  async commitWith<T>(change: (tx: TxProxy) => T | Promise<T>, context: Context, binding: { conversationId?: number } = {}): Promise<T> {
    this.assertOpen()
    return invocationCommit({ client: this.client }, binding)(change, context)
  }

  async close(_context: Context): Promise<void> {
    if (this.closed) return
    this.closed = true
    for (const l of [...this.closeListeners]) l()
    this.host.close()
    this.client.close()
    this.storage.close?.()
  }
  subscribeCommits(listener: (publication: unknown, context: Context) => void) { this.commitListeners.add(listener); return () => { this.commitListeners.delete(listener) } }
  subscribeClose(listener: () => void) { this.closeListeners.add(listener); return () => { this.closeListeners.delete(listener) } }
  snapshot = (...args: unknown[]) => (documentReader(this.client).snapshot as (...a: unknown[]) => unknown)(...args)
  snapshotAsOf = (...args: unknown[]) => (documentReader(this.client).snapshotAsOf as (...a: unknown[]) => unknown)(...args)
  documentState(): never { return unsupported("documentState") }

  // Harness

  resume(): void {
    this.assertOpen()
    if (this.resumed) return
    this.resumed = true
    this.host.sendJSON(EVENT.registry, this.registryPayload())
  }

  async root(context: Context, options: { agent?: unknown; init?: (tx: TxProxy, id: number) => unknown } = {}): Promise<Conversation> {
    return this.create({ kind: "root" }, options, context)
  }

  async createConversation(options: { ownership: unknown; agent?: unknown; init?: (tx: TxProxy, id: number) => unknown }, context: Context): Promise<Conversation> {
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

  /** Task and submission reads need no core state: they read Pi's tables (CONTRACT section 3.10). */
  async getTask(id: number, _context?: Context): Promise<any | undefined> {
    const rows = this.store.all("SELECT record FROM tasks WHERE id = ?", [id])
    return rows[0] ? JSON.parse(String(rows[0][0])) : undefined
  }

  submissionRecord(id: number): any | undefined {
    const rows = this.store.all("SELECT record FROM submissions WHERE id = ?", [id])
    return rows[0] ? JSON.parse(String(rows[0][0])) : undefined
  }

  async submission(id: number, _context?: Context): Promise<Submission | undefined> {
    const record = this.submissionRecord(id)
    return record === undefined ? undefined : new Submission(this, id)
  }

  async inspect(context: Context): Promise<unknown> {
    return awaitWithContext(this.client.inspect({ query: "harness" }), context)
  }

  async abortSubmission(id: number, context: Context, conversationId?: number) {
    this.resume()
    return awaitWithContext(this.client.abort<"aborted" | "already_placed" | "settled" | "not_found">({ submissionId: id, ...(conversationId === undefined ? {} : { conversationId }) }), context)
  }

  async abortTask(id: number, context: Context) {
    this.resume()
    return awaitWithContext(this.client.abort<"marked" | "terminal">({ taskId: id }), context)
  }

  async waitForTask(id: number, context: Context) { this.resume(); return awaitWithContext(this.client.api("waitForTask", { taskId: id }), context) }
  async waitForIdle(context: Context): Promise<void> { this.resume(); await this.idle(undefined, context) }
  async usage(context: Context) { return awaitWithContext(this.client.api("usage"), context) }
  taskGraph(): never { return unsupported("taskGraph") }
  watchTaskGraph(): never { return unsupported("watchTaskGraph") }

  /** Resolve when the ordinary ownership scope of `conversationId` (or of every ownerless conversation) has no live non-background task. */
  async idle(conversationId: number | undefined, context: Context): Promise<void> {
    for (;;) {
      const graph = await awaitWithContext(this.client.api<{ tasks: Record<string, { conversationId: number; owner?: number; background: boolean; conversations: number[] }> }>("taskGraph"), context)
      if (!liveInScope(graph.tasks, conversationId)) return
      await this.waitPublished(n => (n.tasks?.length ?? 0) > 0, context)
    }
  }

  /** Agent of a conversation, resolved in the host against the live registry (agent.ts). */
  async resolveAgent(conversationId: number, at: number | undefined, context: Context) {
    const state = at === undefined ? await this.snapshot(AgentDoc, conversationId, context) : await this.snapshotAsOf(AgentDoc, conversationId, at, context)
    return resolveAgent(state as AgentState | undefined, this.options.registry.snapshot(), this.settings, this.options.onReport ?? (() => {}))
  }

  // Submissions (spec section 6): the core admits.
  async submit(conversationId: number, draft: any, context: Context): Promise<Submission> {
    this.assertOpen()
    this.resume()
    const requestId: string = draft.requestId ?? crypto.randomUUID()
    const placed = new Promise<number>((resolve, reject) => {
      const off = this.host.onNotice(n => {
        if (n.kind !== NOTICE.published) return
        const body = JSON.parse(decoder.decode(n.payload)) as PublishedNotice
        const hit = body.submissions?.find(s => s.requestId === requestId && s.id !== undefined)
        if (hit) { off(); resolve(hit.id!) }
      })
      let step
      try { step = this.host.sendJSON(EVENT.submit, { conversationId, ...draft, requestId }) } catch (e) { off(); return reject(e as Error) }
      if (step?.status) { off(); reject(reviveError({ name: step.error?.name, message: step.error?.message ?? step.error?.raw })) }
    })
    return new Submission(this, await awaitWithContext(placed, context))
  }

  compactTask(conversationId: number, instructions: string | undefined, context: Context): Promise<number> {
    this.resume()
    const input = { reason: "manual", ...(instructions === undefined ? {} : { instructions }) }
    return this.commitWith(tx => tx.createTask(CompactionTask as any, input, { ownership: { kind: "conversation" }, conversationId, background: false }), context)
  }

  waitSubmission(id: number, context: Context): Promise<any> {
    const settled = (): any | undefined => { const r = this.submissionRecord(id); return r && r.status !== "queued" && r.status !== "placed" ? r : undefined }
    const have = settled()
    if (have) return Promise.resolve(have)
    this.resume()
    return (async () => {
      for (;;) {
        await this.waitPublished(n => n.submissions?.some(s => s.id === id) ?? false, context)
        const r = settled()
        if (r) return r
      }
    })()
  }
}

function liveInScope(tasks: Record<string, { conversationId: number; owner?: number; background: boolean; conversations: number[] }>, root: number | undefined): boolean {
  const nodes = Object.values(tasks)
  const scope = new Set<number>()
  const visit = (conversationId: number) => {
    if (scope.has(conversationId)) return
    scope.add(conversationId)
    for (const t of nodes) if (t.conversationId === conversationId && !t.background) for (const c of t.conversations) visit(c)
  }
  if (root !== undefined) visit(root)
  else {
    const owned = new Set(nodes.flatMap(t => t.conversations))
    for (const t of nodes) if (!owned.has(t.conversationId)) visit(t.conversationId)
  }
  return nodes.some(t => !t.background && scope.has(t.conversationId))
}

const unsupported = (what: string): never => { throw new Error(`${what} is not supported by the Durable core host yet`) }

export class Submission {
  readonly id: number
  private readonly harness: Harness
  constructor(harness: Harness, id: number) { this.harness = harness; this.id = id }
  async status(_context?: Context) { return this.harness.submissionRecord(this.id) }
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
    const model = handoff === undefined ? {} : { model: [{ role: "user", content: handoff, timestamp: Date.now() }] }
    await this.harness.submit(this.id, { type: "write", entry: { kind: "pi.reset", head: "self", ...model } }, context)
  }
  commit<T>(change: (tx: TxProxy) => T | Promise<T>, context: Context) { return this.harness.commitWith(change, context, { conversationId: this.id }) }
  context(context: Context) { return awaitWithContext(this.harness.client.api("context", { conversationId: this.id }), context) }
  entries(query: Record<string, unknown>, limit: number, cursor: unknown, context: Context) {
    return this.harness.commitWith(tx => tx.scanEntries({ ...query, conversationId: this.id }, limit, cursor), context)
  }
  fork(at: number, options: { ownership: unknown; agent?: unknown; init?: (tx: TxProxy, id: number) => unknown }, context: Context) {
    return this.harness.create({ kind: "fork", parentId: this.id, at, ownership: options.ownership }, options, context)
  }
  async abort(context: Context, options?: { background?: boolean }) {
    this.harness.resume()
    await awaitWithContext(this.harness.client.abort({ conversationId: this.id, ...(options?.background ? { background: true } : {}) }), context)
    await this.harness.idle(this.id, context)
  }
  waitForIdle(context: Context) { this.harness.resume(); return this.harness.idle(this.id, context) }
  viewState(): never { return unsupported("Conversation.viewState") }
  watch(): never { return unsupported("Conversation.watch") }
}

export { invocationApi, applyChange, runCommit }
