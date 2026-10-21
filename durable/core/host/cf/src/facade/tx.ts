// The host side of `Tx` (pi-durable src/types.ts): every operation is one request on the extension transaction channel
// (ABI section 8). Document drafts are Chord trackers over the value the core returns; their Op[] go back before `end`.
// The core enforces ReadAfterWrite and every other §4 rule and answers each operation, so this proxy forwards and never
// decides.
import { copyJson, type JsonValue } from "@earendil-works/chord"
import { track, type Change, type Tracker } from "@earendil-works/chord/delta"
import type { TxChannel } from "../client.ts"
import type { Revision } from "./snapshots.ts"
import { addressId, resolveAddress, wireAddress, type AnyDocToken, type DocumentAddress } from "./documents.ts"

/** The loaded Chord trackers of the Session (snapshots.ts): a document's tracker is reused while the core's value for it is the same content. */
export type LoadedTrackers = { load(id: string, value: unknown): Tracker<any> | undefined; keep(id: string, tracker: Tracker<any>): void; /** Reads the parent document a fork copy is made from (coldload.ts forkCopyHook). */ forkCopy?(address: unknown, parentConversationId: number, at: number): Promise<void>; /** Sees each table record handed to the callback (table-identity.ts). */ record?(record: object): void }
type Acquisition = { retire: boolean; promise: Promise<unknown> }
type OpenDoc = { entry?: number; id?: number; address: DocumentAddress; definition: AnyDocToken["definition"]; deltasSinceBase?: number; migrated?: boolean; tracker: Tracker<any>; change: Change<any>; retire: boolean }

/**
 * The definition fields the core needs to address and store a document: everything but its functions. `migrates` says the
 * definition has a migrate function (the core then asks the host to run it, `migrateRead`); the function itself rides along
 * as a property JSON drops, so the host side of a read can call it.
 */
export const wireDefinition = (d: AnyDocToken["definition"]) => ({
  kind: d.kind, version: d.version, scope: d.scope,
  ...(typeof d.migrate === "function" ? { migrates: true, migrate: d.migrate } : {}),
  ...(d.history === undefined ? {} : { history: d.history }), ...(d.fork === undefined ? {} : { fork: d.fork }), ...(d.family === true ? { family: true } : {}),
})

/** The strict-JSON copy table writes take: undefined properties are omitted, anything else outside JSON throws (transaction.ts TABLE_JSON_COPY_OPTIONS). */
const tableJson = (value: unknown): unknown => value === undefined ? undefined : copyJson(value as JsonValue, { omitUndefinedProperties: true })

export class TxProxy {
  readonly channel: TxChannel
  private readonly docs = new Map<string, OpenDoc>()
  private readonly forward: (op: string, args?: Record<string, unknown>) => Promise<any>

  private readonly trackers?: LoadedTrackers
  private readonly onWrite?: (address: unknown, definition: { kind?: unknown }, revision: Revision) => void

  constructor(channel: TxChannel, onWrite?: (address: unknown, definition: { kind?: unknown }, revision: Revision) => void, trackers?: LoadedTrackers) {
    this.channel = channel
    this.trackers = trackers
    this.onWrite = onWrite
    // The core answers an absent record with null; pi-durable returns undefined.
    this.send = async (op, args) => (await channel.op(op, args)) ?? undefined
    this.forward = (op, args) => this.track(this.send(op, args))
  }
  /** An operation that is not tracked: the synchronous setters (setTask, settleSubmission, placeSubmission) and the commit's own flush have no promise the callback holds. */
  private readonly send: (op: string, args?: Record<string, unknown>) => Promise<any>

  /** transaction.ts #track: every operation a callback started is observed until it settles; the callback's settlement waits for them. */
  private readonly pending = new Set<Promise<unknown>>()
  private track<T>(operation: Promise<T>): Promise<T> {
    this.pending.add(operation)
    const done = () => { this.pending.delete(operation) }
    operation.then(done, done)
    return operation
  }
  /** Set when the callback settled: an operation that finishes later rejects (transaction.ts #assertOpen). */
  settled = false
  private assertOpen(): void { if (this.settled) throw new Error("Transaction has settled") }
  hasPending(): boolean { return this.pending.size > 0 }
  /** transaction.ts settleFailure/settleSuccess: every pending operation settles before the commit does. */
  async drain(): Promise<void> { await Promise.allSettled([...this.pending]) }

  // Table reads.
  conversation(id: number) { return this.forward("conversation", { id }) }
  entry(...args: unknown[]) {
    // entry(id) or entry(token, id): the token only types the data; the core checks the kind.
    const [first, second] = args
    return this.forward("entry", typeof first === "number" ? { id: first } : { kind: (first as { kind?: string })?.kind, id: second })
  }
  task(id: number) { return this.forward("task", { id }) }
  scanConversations(query: unknown, limit: number, cursor?: unknown) { return this.forward("scanConversations", { query, limit, cursor }) }
  scanEntries(query: unknown, limit: number, cursor?: unknown) { return this.forward("scanEntries", { query, limit, cursor }) }
  latestHeadMarker(conversationId: number) { return this.forward("latestHeadMarker", { conversationId }) }
  scanTasks(query: unknown, limit: number, cursor?: unknown) { return this.forward("scanTasks", { query, limit, cursor }) }
  submissionByRequest(conversationId: number, requestId: string) { return this.forward("submissionByRequest", { conversationId, requestId }) }

  // Table writes.
  createRootConversation() { return this.created(this.forward("createRootConversation")) }
  createConversation(options: { ownership: unknown }) { return this.created(this.forward("createConversation", { ownership: options.ownership })) }
  async forkConversation(parentConversationId: number, at: number, options: { ownership: unknown }) {
    const created = await this.created(this.forward("forkConversation", { parentConversationId, at, ownership: options.ownership }))
    if (typeof created?.id === "number") this.forks.set(created.id, { parent: parentConversationId, at })
    return created
  }
  private readonly forks = new Map<number, { parent: number; at: number }>()

  /** The core ran the built-in creation hook (documents, agent) inside the operation; HarnessOptions.conversationCreated runs last, in this transaction, and a throw fails it. */
  private async created(op: Promise<any>) {
    const record = await op
    await this.channel.created(this, record)
    this.handed(record)
    return record
  }
  appendEntry(...args: unknown[]) {
    // appendEntry(conversationId, draft) or appendEntry(token, conversationId, draft).
    if (typeof args[0] === "number") return this.tableWrite(() => this.forward("appendEntry", { conversationId: args[0], value: tableJson(args[1]) })).then(entry => this.handed(entry))
    const token = args[0] as { kind?: string; definition?: { kind?: string } }
    return this.tableWrite(() => this.forward("appendEntry", { kind: token.kind ?? token.definition?.kind, conversationId: args[1], value: tableJson(args[2]) })).then(entry => this.handed(entry))
  }
  private handed<T>(record: T): T { if (typeof record === "object" && record !== null) this.trackers?.record?.(record); return record }
  /** A table write whose JSON is not strict rejects (transaction.ts copyJson with TABLE_JSON_COPY_OPTIONS): the promise rejects, nothing throws. */
  private tableWrite<T>(send: () => Promise<T>): Promise<T> {
    try { return send() } catch (error) { return Promise.reject(error) }
  }
  /** compaction.ts createCompaction: the compaction task and its pi.live status, in the core. */
  createCompaction(conversationId: number, input: unknown) { return this.forward("createCompaction", { conversationId, input }) }

  /** `initial(input)` runs here, in the host: it is task-definition code (ABI section 8, createTask). */
  createTask(task: { definition: { name: string; version?: number; initial?(input: unknown): unknown } }, input: unknown, options: unknown) {
    // A built-in task (generation, tool, compaction) is core code and has no `initial` here: the core builds its first checkpoint.
    const state = typeof task.definition.initial === "function" ? { state: task.definition.initial(copyJson(input as JsonValue)) } : {}
    return this.tableWrite(() => this.forward("createTask", { task: task.definition.name, version: task.definition.version ?? 1, ...state, input: tableJson(input), options }))
  }
  createSubmission(create: unknown) { return this.tableWrite(() => this.forward("createSubmission", { create: tableJson(create) })) }
  settleSubmission(id: number, settlement: unknown): void { void this.send("settleSubmission", { id, settlement }).catch(() => {}) }
  placeSubmission(id: number, entry: number): void { void this.send("placeSubmission", { id, entry }).catch(() => {}) }

  // Documents.
  async doc(token: AnyDocToken, ...args: unknown[]) {
    const { definition } = token
    const resolved = resolveAddress(definition, args)
    const scope = resolved.address.scope as { kind: string; taskId?: number }
    if (scope.kind === "task" && scope.taskId !== undefined && this.terminalCandidates.has(scope.taskId)) throw new Error(`Task ${scope.taskId} is terminal`)
    const seed = definition.family === true ? args[resolved.nextArgument] : undefined
    const open = this.docs.get(resolved.id)
    if (open) return open.change.state
    // A second call that races the first joins its acquisition (transaction.ts memoizes the draft promise): the document initializes and mints once.
    const joined = this.acquiring.get(resolved.id)
    if (joined) return joined.promise
    // A retirement still on its way to the core goes first: the new incarnation is acquired behind it.
    const barrier = this.barriers.get(resolved.id)
    const entry: Acquisition = { retire: false, promise: undefined as never }
    entry.promise = barrier === undefined ? this.acquire(definition, resolved, seed, entry) : barrier.then(() => this.acquire(definition, resolved, seed, entry))
    this.acquiring.set(resolved.id, entry)
    const forget = () => { if (this.acquiring.get(resolved.id) === entry) this.acquiring.delete(resolved.id) }
    entry.promise.then(forget, forget)
    return entry.promise
  }
  /** The acquisitions in flight, and the retirements not yet sent to the core, by address. */
  private readonly acquiring = new Map<string, Acquisition>()
  private readonly barriers = new Map<string, Promise<unknown>>()

  private async acquire(definition: AnyDocToken["definition"], resolved: ReturnType<typeof resolveAddress>, seed: unknown, acquisition: Acquisition) {
    // The core asks for the values only definition code can compute: initial(seed) for an absent document, migrate(value,
    // from) for an older stored version (durable/core/host/PROTOCOL.md, `doc`).
    const request: Record<string, unknown> = { address: wireAddress(resolved.address), def: wireDefinition(definition), version: definition.version, ...(seed === undefined ? {} : { seed }) }
    // A document of a conversation this transaction forked is a copy of the parent's at the fork point, read through the Storage (transaction.ts #acquireForkCopy).
    const wire = request.address as { scope: string; conversationId?: number }
    const fork = wire.scope === "conversation" && wire.conversationId !== undefined ? this.forks.get(wire.conversationId) : undefined
    if (fork !== undefined) await this.trackers?.forkCopy?.(request.address, fork.parent, fork.at)
    let answer = await this.forward("doc", request)
    this.assertOpen()
    for (let i = 0; answer?.need !== undefined && i < 2; i++) {
      if (answer.need === "initial") request.initial = copyJson((definition.family === true ? definition.initial(seed as JsonValue) : definition.initial()) as JsonValue)
      else if (answer.need === "migrate") {
        if (typeof definition.migrate !== "function") throw new Error(`Document ${definition.kind} requires migration from version ${answer.from}`)
        request.migrated = copyJson(definition.migrate(answer.value, answer.from) as JsonValue)
      } else break
      answer = await this.forward("doc", request)
      this.assertOpen()
    }
    const tracker = this.trackers?.load(resolved.id, answer.value) ?? track(copyJson(answer.value as JsonValue) as object)
    this.trackers?.keep(resolved.id, tracker)
    const change = tracker.beginChange()
    const open: OpenDoc = { entry: answer.entry, id: answer.id, address: resolved.address, definition, deltasSinceBase: answer.deltasSinceBase, ...(request.migrated === undefined ? {} : { migrated: true }), tracker, change, retire: false }
    // A retirement that overtook this acquisition keeps the draft readable until settlement without taking the address.
    if (acquisition.retire) this.retiring.push(open)
    else this.docs.set(resolved.id, open)
    return change.state
  }

  async retireDoc(token: AnyDocToken, ...args: unknown[]) {
    const resolved = resolveAddress(token.definition, args)
    // The retirement is decided when it is called (transaction.ts latest.retireOnCommit): a pending acquisition and an open draft stop being the address's latest incarnation now.
    const acquisition = this.acquiring.get(resolved.id)
    if (acquisition) { acquisition.retire = true; this.acquiring.delete(resolved.id) }
    const open = this.docs.get(resolved.id)
    // The retiring incarnation's draft stays readable until the callback settles (Pi revokes drafts at settlement): its operations go to the core then,
    // naming the incarnation, because a new incarnation can take the address meanwhile.
    if (open) { this.retiring.push(open); this.docs.delete(resolved.id) }
    const sent = (async () => {
      // A retirement that races a pending acquisition follows it: the acquired draft's content is persisted before the retirement.
      if (acquisition) await acquisition.promise
      this.assertOpen()
      await this.forward("retireDoc", { address: wireAddress(resolved.address), def: wireDefinition(token.definition) })
    })()
    const previous = this.barriers.get(resolved.id)
    const barrier = (previous === undefined ? sent : sent.then(() => previous)).catch(() => {})
    this.barriers.set(resolved.id, barrier)
    const clear = () => { if (this.barriers.get(resolved.id) === barrier) this.barriers.delete(resolved.id) }
    barrier.then(clear)
    await sent
  }
  private readonly retiring: OpenDoc[] = []

  /** Internal (session/transaction.ts setTask): replace one task record completely. Pi throws synchronously; here the core's answer is awaited by `flush`. */
  setTask(value: unknown): void {
    const record = value as { id: number; state?: { status?: string } }
    if (this.terminalCandidates.has(record.id)) throw new Error(`Task ${record.id} already has a terminal candidate`)
    // A terminal candidate is judged at settlement (transaction.ts: the task's documents retire with it, the ones this transaction created
    // included): the core hears it after this transaction's document writes, and later documents of the task are refused here.
    if (record.state?.status === "terminal") { this.terminalCandidates.add(record.id); this.terminal.push(value); return }
    this.sent.push(this.send("setTask", { value }))
  }
  private readonly sent: Promise<unknown>[] = []
  private readonly terminal: unknown[] = []
  private readonly terminalCandidates = new Set<number>()

  /** Send every changed draft's operations: called by the commit driver before `end`. */
  async flush(): Promise<void> {
    await Promise.all(this.sent)
    for (const open of this.retiring.splice(0)) await this.writeDoc(undefined, open, false)
    for (const [id, open] of [...this.docs]) await this.writeDoc(id, open)
    for (const value of this.terminal.splice(0)) await this.send("setTask", { value })
  }

  /** Send one draft's operations to the core and close it. */
  private async writeDoc(id: string | undefined, open: OpenDoc, adopt = true) {
    const prepared = open.change.prepare()
    if (prepared.ops.length > 0) {
      // Pi runs checkpointWhen at settlement on the prepared value and ops; the host has both here. A migration writes the required
      // version base without asking the predicate (session/documents.ts: the required base resets the count).
      const checkpoint = open.migrated !== true && open.deltasSinceBase !== undefined && open.definition.checkpointWhen(prepared.value, prepared.ops, { deltasSinceBase: open.deltasSinceBase }) === true
      await this.send("doc.write", { address: wireAddress(open.address), def: wireDefinition(open.definition), ops: prepared.ops, baseRevision: prepared.baseRevision, ...(open.entry === undefined ? {} : { entry: open.entry }), ...(checkpoint ? { checkpoint } : {}) })
    }
    // The revision the Storage write, the publication and the next snapshot carry (snapshots.ts Revision): a document with operations, a created one, and a migration's required base.
    if (this.onWrite !== undefined && (prepared.ops.length > 0 || open.id === undefined || open.migrated === true)) {
      this.onWrite(wireAddress(open.address), open.definition, {
        address: wireAddress(open.address), addressId: addressId(open.address), definition: open.definition, ...(open.id === undefined ? {} : { id: open.id }),
        value: prepared.value, ops: prepared.ops, adopt: adopt && (prepared.ops.length > 0 || open.id === undefined), tracker: open.tracker, prepared,
      })
    } else prepared.abort()
    if (id !== undefined) this.docs.delete(id)
    return prepared
  }

  discard() {
    this.terminal.length = 0
    for (const open of this.retiring.splice(0)) open.change.abort()
    for (const open of this.docs.values()) open.change.abort()
    this.docs.clear()
  }

  static addressId = addressId
}

/** Run `change` as one Session commit: begin (waiting for the line), callback, flush drafts, end with the next task state. */
const SYNC_SETTERS = new Set(["setTask", "settleSubmission", "placeSubmission"])

export async function runCommit<T>(
  begin: () => Promise<TxChannel>,
  change: (tx: TxProxy, current?: unknown) => T | Promise<T>,
  interpret?: (result: T) => unknown,
  onError?: (error: unknown) => void,
  onWrite?: (address: unknown, definition: { kind?: unknown }, revision: Revision) => (committed: boolean) => void,
  trackers?: LoadedTrackers,
  failed?: () => void,
): Promise<T> {
  const channel = await begin()
  // A document's write counts as a new revision only once the commit succeeded (a rolled-back commit leaves every snapshot as it was).
  const wrote: ((committed: boolean) => void)[] = []
  const tx = new TxProxy(channel, onWrite && ((address, definition, revision) => { wrote.push(onWrite(address, definition, revision)) }), trackers)
  let sealed = false
  // transaction.ts #assertOpen: once the callback settles, the Tx handed to it refuses every use with 'Transaction has settled' (a
  // rejection for the async operations, a throw for the synchronous setters). The commit's own flush runs on the unsealed proxy target.
  const handle = new Proxy(tx, {
    get(target, key) {
      const value = Reflect.get(target, key, target)
      if (!sealed || typeof value !== "function" || typeof key !== "string") return typeof value === "function" ? value.bind(target) : value
      const settled = new Error("Transaction has settled")
      return SYNC_SETTERS.has(key) ? () => { throw settled } : () => Promise.reject(settled)
    },
  })
  let result: T
  try {
    result = await change(handle, channel.current)
    // A Session that failed while the callback ran commits nothing the callback did afterwards (session.ts fail aborts the open transaction).
    failed?.()
    sealed = true
    tx.settled = true
    // transaction.ts settleSuccess: an operation the callback left pending is aborted and drained, and fails the commit.
    if (tx.hasPending()) {
      tx.discard()
      await tx.drain()
      throw new Error("Session commit callback settled before its pending Tx operations")
    }
    await tx.flush()
  } catch (error) {
    sealed = true
    tx.settled = true
    for (const settle of wrote) settle(false)
    tx.discard()
    // settleFailure: the callback's own error is reported once every pending operation has settled.
    await tx.drain()
    await channel.abort(error)
    onError?.(error)
    throw error
  }
  await channel.quiesce()
  // Returned, not awaited: the caller's promise adopts the ack's promise, the host's counterpart of Pi's `run` adopting the line job (session.ts:531).
  const ended = channel.end(interpret ? interpret(result) : undefined, result, onError)
  // A side branch: the caller's promise is `ended` itself, so its timing is unchanged.
  if (onWrite && wrote.length > 0) ended.then(() => { for (const settle of wrote) settle(true) }, () => { for (const settle of wrote) settle(false) })
  return ended
}

/** `migrateRead` as the repeat of a rejected `api` request (Client.api `retry`): the request is repeated with the migrated value in the same promise. */
export const migrateRetry = (def: { migrate?: unknown }) => (error: Error): { migrated: JsonValue } | undefined => {
  if (error?.name !== "NeedMigrate") return undefined
  const need = JSON.parse(error.message) as { value: Record<string, JsonValue>; from: number }
  return { migrated: copyJson((def.migrate as (value: Record<string, JsonValue>, from: number) => JsonValue)(need.value, need.from)) }
}

/**
 * A read-only document access (snapshot, snapshotAsOf, watch.open) whose stored version is older than the definition's: the core
 * rejects `NeedMigrate` with {value, from}, and the host repeats the request with the result of the definition's migrate (the
 * same exchange `Tx.doc` has as {need: "migrate"}). `migrate` rides on the wire definition.
 */
export async function migrateRead<T>(def: { migrate?: unknown }, send: (migrated: JsonValue | undefined) => Promise<T>): Promise<T> {
  try { return await send(undefined) } catch (error) {
    if ((error as Error)?.name !== "NeedMigrate") throw error
    const need = JSON.parse((error as Error).message) as { value: Record<string, JsonValue>; from: number }
    return send(copyJson((def.migrate as (value: Record<string, JsonValue>, from: number) => JsonValue)(need.value, need.from)))
  }
}
