// The request side of the host: `api` requests (ABI section 8.1) and the extension transaction channel (section 8) as
// promises. Answers arrive as notices after the step that carried the request; every pending request fails when the handle
// is discarded.
import type { Context } from "@earendil-works/chord/context"
import { EVENT, NOTICE } from "./abi.ts"
import { reviveError, SessionFailed, type StorageFailure } from "./errors.ts"
import { FatalError, RecycledError, RecycledUncertainError, RejectedError, type HostSession } from "./host.ts"
import type { ApiAnswer, TxAnswer } from "./protocol.ts"
import type { Notice, Step } from "./wire.ts"

/**
 * `sent` is the event of a request the core has not answered. When the host replaces its handle, the request is sent again under the same
 * ID to the new core if that cannot apply it twice: the old core applied nothing of the event, or the event is `idempotent` (a read, a wait,
 * a submission with a request ID, which the core answers with the submission it admitted). A request of an invocation (`bound`) is not
 * sent again: the invocation ended with its handle.
 */
type Sent = { kind: number; bytes: Uint8Array; idempotent: boolean; bound: boolean }
type Pending = { resolve: (value: any) => void; reject: (error: Error) => void; sent?: Sent; waits?: boolean; signal?: AbortSignal }
/** The message of the core's rejection of a request whose invocation signal aborted (session ErrAborted). */
const ABORTED = "The operation was aborted"
const dec = new TextDecoder()

const split = (n: Notice): [number, unknown] => {
  const id = new DataView(n.payload.buffer, n.payload.byteOffset, 4).getUint32(0, true)
  return [id, JSON.parse(dec.decode(n.payload.subarray(4)))]
}

/** A step that came back rejected becomes the error Pi throws. */
const rejection = (step: Step | undefined, failure?: StorageFailure): { error: Error } | undefined =>
  step && step.status === 1 ? { error: reviveError({ name: step.error?.name, message: step.error?.message ?? step.error?.raw }, failure) } : undefined

/** An answer to a rejected `api` request: the extra arguments to repeat it with, or undefined to reject. */
export type ApiRetry = (error: Error) => Record<string, unknown> | undefined

export class CoreClient {
  /** The Storage error that failed the Session, as the object the host's Storage threw (errors.ts reviveError). */
  storageFailure?: StorageFailure
  host: HostSession
  private nextRequest = 1
  private nextTx = 1
  private readonly requests = new Map<number, Pending>()
  private readonly txs = new Map<number, TxChannel>()
  private readonly ackWaiters = new Map<number, Pending>()
  private nextWait = 1
  /** HarnessOptions.conversationCreated: run by the transaction that created or forked a conversation, last in the creation hook (harness.ts conversationCreated). */
  onConversationCreated?: (tx: unknown, record: unknown) => unknown
  /** The sizes of a session-mode core's document cache, as its last doc_cache notice said (session.ts documentCacheSizes is synchronous). */
  docCache: { readonly cached: number; readonly idle: number; readonly attached: number; readonly sweeps: number; readonly held: number; readonly taskDocuments: number; readonly tails: number } = { cached: 0, idle: 0, attached: 0, sweeps: 0, held: 0, taskDocuments: 0, tails: 0 }
  /** Told which documents the core's cache dropped, with the record IDs: the host forgets the values it holds of them (facade/harness.ts documentValues). */
  onDropped: ((documents: { id: number; address: string }[]) => void) | undefined
  /** The last wake the core published, and the host's callback for each (HarnessOptions.onWake); the core arms no timer for sleeping tasks when the host takes it. */
  wake: { active: boolean; at?: number } | undefined
  onWake: ((wake: { active: boolean; at?: number }) => void) | undefined
  /** The sizes of the scheduler's ownership indexes, as the core's last index_sizes notice said (scheduler.ts indexSizes is synchronous). */
  indexSizes: Readonly<Record<"live" | "settled" | "edges" | "below" | "roots" | "unloaded" | "dropQueue" | "pins", number>> = { live: 0, settled: 0, edges: 0, below: 0, roots: 0, unloaded: 0, dropQueue: 0, pins: 0 }
  private off: () => void

  constructor(host: HostSession) {
    this.host = host
    this.off = host.onNotice(n => this.route(n))
    // Only a host that replaces its own handle (HostOptions.renew) has anything to tell: a DoSession recovers through its own path.
    this.offRecycled = host.canRecycle ? host.onRecycled((cause, undelivered) => this.recycled(cause, undelivered)) : () => {}
  }

  private readonly offRecycled: () => void

  /**
   * The host replaced its core handle (a trap, or a recycle at a quiet point). What lived only in the old handle is gone: the
   * open transactions (nothing of them was committed), the progress acknowledgements, and the requests that ran for an
   * invocation (the invocation's handler was cancelled with its handle). They fail with a RecycledError, which a caller may
   * retry. An unanswered request is sent again under its ID when the new core cannot apply it twice (`Sent`). One the old
   * core ran that could take effect twice (a submission without a request ID, the withdrawal of a submission) fails with a
   * RecycledUncertainError: the old core may have committed it before the trap, and nothing in the store names the request.
   */
  private recycled(cause: Error | undefined, undelivered: ReadonlySet<Uint8Array>) {
    const after = cause ? ` after a trap (${cause.message})` : ""
    const error = new RecycledError(`the Durable core was recycled${after}: nothing of this operation was committed; it can be repeated`, cause)
    const uncertain = new RecycledUncertainError(`the Durable core was recycled${after} after it took this operation and before it answered: the operation may or may not have been applied; read the store before repeating it`, cause)
    for (const c of [...this.txs.values()]) {
      // A transaction that has not yet taken the line has no state in the core: its `begin` is sent again. One that has holds uncommitted work only the old core knew.
      if (c.fresh) { c.replayBegin(); continue }
      // A transaction whose `end` already reached the store has committed: only the core's acknowledgement is lost, and the caller's commit resolves.
      if (c.committed) { c.answer({ result: null }); continue }
      c.fail(error)
      this.release(c.id)
    }
    for (const p of this.ackWaiters.values()) p.reject(error)
    this.ackWaiters.clear()
    for (const [id, p] of [...this.requests]) {
      const sent = p.sent
      const ran = sent !== undefined && !undelivered.has(sent.bytes)
      if (sent === undefined || sent.bound || (ran && !sent.idempotent)) {
        this.requests.delete(id)
        p.reject(ran && !sent!.idempotent ? uncertain : error)
        continue
      }
      this.host.replay(sent.kind, sent.bytes, step => {
        const rejected = rejection(step, this.storageFailure)
        if (rejected && this.requests.get(id) === p) { this.requests.delete(id); p.reject(rejected.error) }
      })
      // The new core knows nothing of a context that aborted while the old one had the request.
      if (p.signal?.aborted && !sent.bound) this.cancel(id)
    }
  }

  private route(n: Notice) {
    switch (n.kind) {
      case NOTICE.api_result: {
        const [id, body] = split(n)
        const p = this.requests.get(id)
        if (!p) return
        this.requests.delete(id)
        const answer = body as ApiAnswer
        if ("error" in answer) p.reject(reviveError(answer.error, this.storageFailure))
        else p.resolve(this.expand(answer.ok))
        return
      }
      case NOTICE.tx_result: {
        const [id, body] = split(n)
        this.txs.get(id)?.answer(body as TxAnswer)
        return
      }
      case NOTICE.doc_cache: {
        const body = JSON.parse(dec.decode(n.payload)) as typeof this.docCache & { dropped?: { id: number; address: string }[] }
        // Documents the core's cache dropped (core/sweep.go): the values this host holds of them go too.
        if (body.dropped !== undefined) { this.onDropped?.(body.dropped); delete body.dropped }
        this.docCache = body
        return
      }
      case NOTICE.index_sizes: {
        this.indexSizes = JSON.parse(dec.decode(n.payload))
        return
      }
      case NOTICE.wake: {
        // The core's Harness.wake() after a change (HarnessOptions.onWake): the host arms its alarm from it.
        this.wake = JSON.parse(dec.decode(n.payload))
        this.onWake?.(this.wake!)
        return
      }
      case NOTICE.progress_ack: {
        const id = new DataView(n.payload.buffer, n.payload.byteOffset, 4).getUint32(0, true)
        const outcome = n.payload[4]
        const p = this.ackWaiters.get(id)
        if (!p) return
        this.ackWaiters.delete(id)
        if (outcome === 0) return p.resolve(undefined)
        const text = dec.decode(n.payload.subarray(5))
        p.reject(reviveError(text ? JSON.parse(text) : { name: "Error", message: "the progress commit was rejected" }, this.storageFailure))
      }
    }
  }

  /** A context read the core named instead of building (`{$context:{conversationId, head, tail}}`) is derived here from storage. */
  private expand(ok: unknown): unknown {
    const named = (ok as { $context?: { conversationId: number; head: number; tail: number } } | null)?.$context
    return named === undefined ? ok : this.host.contexts.view(named.conversationId, named.head, named.tail)
  }

  /** Requests, transactions and acks still waiting: a handle with any of them is not idle. */
  get pending(): number { return this.requests.size + this.txs.size + this.ackWaiters.size }

  /** Reserve a waitId for a tool details update: the promise settles at the progress_ack notice. */
  expectAck(): { waitId: number; settled: Promise<void> } {
    const waitId = this.nextWait++
    return { waitId, settled: new Promise((resolve, reject) => this.ackWaiters.set(waitId, { resolve, reject })) }
  }

  /** session.ts fail: the waits underway (waitForIdle, waitForTask, ...) reject with the Session's failure before anything the failure ends can cancel them, but for the `answered` ones. */
  failWaits(error: Error, answered: readonly number[] = []): void {
    // A wait the core already answered, whose answer is on its way (the continuation of Pi's resolved promise), stays resolved.
    for (const [id, request] of [...this.requests]) if (request.waits && !answered.includes(id)) { this.requests.delete(id); request.reject(error) }
  }

  /**
   * awaitWithContext for a wait (waitForIdle, waitForTask): the caller's context cancels it, until the Session fails. The Session's failure rejects the wait
   * itself (failWaits; Pi's Waiters reject at the failure and no longer listen to the signal), so the abort of the signal the failure then ends the task
   * with, which arrives before the rejection reaches this wrapper, does not turn it into an AbortError.
   */
  awaitWait<T>(promise: Promise<T>, context: Context): Promise<T> {
    const signal = context.abortSignal
    if (signal === undefined) return promise
    const aborted = (): Error => signal.reason instanceof Error ? signal.reason : new DOMException("The operation was aborted", "AbortError")
    if (signal.aborted) return Promise.reject(aborted())
    return new Promise<T>((resolve, reject) => {
      const onAbort = (): void => { if (this.storageFailure === undefined) reject(aborted()) }
      signal.addEventListener("abort", onAbort, { once: true })
      void promise.then(
        value => { signal.removeEventListener("abort", onAbort); resolve(value) },
        (error: unknown) => { signal.removeEventListener("abort", onAbort); reject(error) },
      )
    })
  }

  /** One `api` request. Rejects with Pi's error when the core answers `{error}` or rejects the event, and with the discard error when the handle dies first. */
  api<T = unknown>(op: string, args: Record<string, unknown> = {}, revive?: (value: T) => T, retry?: ApiRetry): Promise<T> { return this.apiNow<T>(op, args, revive, retry).promise }

  /** `api`, and whether the core answered inside the send (the promise is already settled): a caller that mirrors a Pi call which returns an already-resolved promise returns this one as it is. */
  apiNow<T = unknown>(op: string, args: Record<string, unknown> = {}, revive?: (value: T) => T, retry?: ApiRetry): { promise: Promise<T>; settled: boolean } {
    let settled = false
    const answer = new Promise<T>((resolve, reject) => {
      // session.ts assertUsable: a failed Session answers every call with SessionFailed. The writer's own protocol (a Storage's receipt, its publication scope's publish) still reaches the core: the call it serves is underway.
      if (this.storageFailure !== undefined && op !== "publish" && op !== "receive") { settled = true; return reject(new SessionFailed(this.storageFailure.error)) }
      const id = this.nextRequest++
      const bytes = apiBytes(id, { op, ...args })
      // Every api op the host sends is a read, a wait, or a registration the new core takes again; the storage-mode writes run on hosts without a renewal.
      this.requests.set(id, { waits: op.startsWith("waitFor"), sent: { kind: EVENT.api, bytes, idempotent: !op.startsWith("storage.") && op !== "adopt", bound: args.invocation !== undefined }, resolve: value => { settled = true; resolve(revive ? revive(value as T) : value as T) }, reject: error => {
        // A rejection the caller answers by repeating the request (a read that needs the definition's migration) is settled by the repeat's answer, so a success spends no pass of its own.
        let again: Record<string, unknown> | undefined
        try { again = retry?.(error) } catch (e) { settled = true; return reject(e as Error) }
        if (again === undefined) { settled = true; return reject(error) }
        this.apiNow<T>(op, { ...args, ...again }, revive).promise.then(value => { settled = true; resolve(value) }, e => { settled = true; reject(e) })
      } })
      let step: Step | undefined
      try { step = this.host.send(EVENT.api, bytes, id) } catch (e) { this.requests.delete(id); settled = true; return reject(e as Error) }
      const rejected = rejection(step, this.storageFailure)
      if (rejected) { this.requests.delete(id); settled = true; reject(rejected.error) }
    })
    // A caller that stopped waiting (awaitWithContext settled on its context's abort) leaves this answer unobserved; its
    // later rejection (the core rejects an aborted invocation's wait) is not an unhandled rejection. Awaiters still see it.
    answer.catch(() => {})
    return { promise: answer, settled }
  }

  /** An `inspect` event (ABI section 5, kind 16): reads core state the api table does not name. The core echoes `requestId` in an api_result notice. */
  inspect<T = unknown>(query: Record<string, unknown>, revive?: (value: T) => T): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const id = this.nextRequest++
      const bytes = jsonBytes({ ...query, requestId: id })
      // `revive` runs as the answer arrives: a `.then` or an async wrapper would add the caller's resumption a pass later than Pi's readOnLine promise.
      this.requests.set(id, { sent: { kind: EVENT.inspect, bytes, idempotent: true, bound: false }, resolve: revive ? (value: unknown) => { try { resolve(revive(value as T)) } catch (error) { reject(error as Error) } } : resolve, reject })
      let step: Step | undefined
      try { step = this.host.send(EVENT.inspect, bytes, id) } catch (e) { this.requests.delete(id); return reject(e as Error) }
      const rejected = rejection(step, this.storageFailure)
      if (rejected) { this.requests.delete(id); reject(rejected.error) }
    })
  }

  /** An `inspect` the core answers in the step that carries it (`lineSizes`, `idleState`): the answer, read synchronously, for the getters Pi's tests read without awaiting. */
  inspectNow<T = unknown>(query: Record<string, unknown>): T {
    const answer: { done: boolean; value?: T; error?: Error } = { done: false }
    const id = this.nextRequest++
    this.requests.set(id, { resolve: value => { answer.done = true; answer.value = value as T }, reject: error => { answer.done = true; answer.error = error } })
    let step: Step | undefined
    try { step = this.host.send(EVENT.inspect, jsonBytes({ ...query, requestId: id }), id) } finally { this.requests.delete(id) }
    const rejected = rejection(step, this.storageFailure)
    if (rejected) throw rejected.error
    if (answer.error !== undefined) throw answer.error
    if (!answer.done) throw new Error(`the core did not answer the ${String(query.query)} query in its step`)
    return answer.value as T
  }

  /**
   * The abort of a request's context, as Pi runs it: the request's line job checks the context once, when it starts (session.ts
   * #runCommit throwIfAborted), and a job that started ignores a later abort. A request of an invocation-bound handle carries
   * `invocation`, and the core checks that invocation's signal; any other request with a `signal` is `cancellable`, and the
   * core aborts the request's own signal on `cancel {request}`, which is sent here when `signal` aborts. A rejection by that
   * check carries the signal's reason, as Pi's throw does.
   */
  private cancellable(id: number, body: Record<string, unknown>, signal: AbortSignal | undefined) {
    const watch = signal !== undefined && body.invocation === undefined
    let off: (() => void) | undefined
    return {
      fields: watch ? { cancellable: true } : {},
      /** After the event was sent: the cancel of a context that aborted already, or that aborts while the request is unanswered. */
      start: (pending: Pending) => {
        pending.signal = signal
        if (!watch) return
        if (signal.aborted) return this.cancel(id)
        const cancel = () => this.cancel(id)
        signal.addEventListener("abort", cancel, { once: true })
        off = () => signal.removeEventListener("abort", cancel)
      },
      /** The request was answered: the signal is not watched any more. */
      done: () => off?.(),
      reason: (error: Error): Error => signal?.aborted === true && (error.name === "AbortError" || error.message === ABORTED) ? signal.reason : error,
    }
  }

  /** `cancel {request}` of the request `id`, when it is still unanswered. */
  private cancel(id: number) {
    if (this.requests.has(id)) void this.api("cancel", { request: id })
  }

  /** A `submit` event (ABI section 5, kind 3): resolves with the admitted submission's ID once the core has committed it, after the commit's `published` notice. `id` is the api_result key the core answers on; `signal` is the caller's context signal (`cancellable`). */
  submit<T = { id: number }>(body: Record<string, unknown>, revive?: (placed: { id: number }) => T, signal?: AbortSignal): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const id = this.nextRequest++
      const cancelled = this.cancellable(id, body, signal)
      const bytes = jsonBytes({ ...body, ...cancelled.fields, id })
      // A request ID makes a second admission return the first (inbox.Admit); without one a second admission is a second submission.
      // `revive` runs as the answer routes, so the caller's promise resolves at the tick Pi's awaiting method resumes.
      const pending: Pending = { sent: { kind: EVENT.submit, bytes, idempotent: typeof body.requestId === "string", bound: body.invocation !== undefined }, resolve: value => { cancelled.done(); resolve(revive ? revive(value as { id: number }) : value as T) }, reject: error => { cancelled.done(); reject(cancelled.reason(error)) } }
      this.requests.set(id, pending)
      let step: Step | undefined
      try { step = this.host.send(EVENT.submit, bytes, id) } catch (e) { this.requests.delete(id); return reject(e as Error) }
      const rejected = rejection(step, this.storageFailure)
      if (rejected) { this.requests.delete(id); return reject(rejected.error) }
      cancelled.start(pending)
    })
  }

  /** An `abort` event that expects an answer (`abortSubmission`, `abortTask`): the core echoes `requestId` in an api_result notice. `signal` is the caller's context signal (`cancellable`). */
  abort<T = unknown>(body: Record<string, unknown>, revive?: (answer: T) => T, signal?: AbortSignal): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const id = this.nextRequest++
      const cancelled = this.cancellable(id, body, signal)
      const bytes = jsonBytes({ ...body, ...cancelled.fields, requestId: id })
      // Aborting a task or a conversation again is harmless; withdrawing a submission again answers `settled` where the first answered `aborted`.
      const pending: Pending = { sent: { kind: EVENT.abort, bytes, idempotent: body.submissionId === undefined, bound: body.invocation !== undefined }, resolve: value => { cancelled.done(); resolve(revive ? revive(value as T) : value as T) }, reject: error => { cancelled.done(); reject(cancelled.reason(error)) } }
      this.requests.set(id, pending)
      let step: Step | undefined
      try { step = this.host.send(EVENT.abort, bytes, id) } catch (e) { this.requests.delete(id); return reject(e as Error) }
      const rejected = rejection(step, this.storageFailure)
      if (rejected) { this.requests.delete(id); return reject(rejected.error) }
      cancelled.start(pending)
    })
  }

  /** Open a transaction on the Session line. Resolves at `ready`; `waiting` only means the line is held and `ready` follows. */
  begin(options: { invocation?: number; conversationId?: number; span?: number; trace?: unknown; persistence?: { kind: "deferred"; withinMs?: number } } = {}): Promise<TxChannel> {
    const id = this.nextTx++
    const channel = new TxChannel(this, id)
    this.txs.set(id, channel)
    if (options.invocation === undefined) this.callerTxs.add(id)
    // Not async: the caller resumes one tick after the grant, as Pi's job starts one tick after `#tail.then(job)` (session.ts:531). A refused begin releases the
    // handle in a side branch that runs before the caller's own reaction.
    const ready = channel.begin(options) as Promise<TxChannel>
    ready.catch(() => this.release(id))
    return ready
  }

  /** Bind a channel the core already opened: a `callback` effect carries its txId (ABI effect 13). */
  bind(txId: number): TxChannel {
    const channel = new TxChannel(this, txId)
    this.txs.set(txId, channel)
    return channel
  }

  release(txId: number) {
    this.txs.delete(txId)
    this.callerTxs.delete(txId)
    if (this.callerTxs.size === 0) for (const resolve of this.drained.splice(0)) resolve()
  }

  /** Transactions a caller opened (commit, createConversation...), as opposed to those of a task invocation, which the close event aborts. */
  private readonly callerTxs = new Set<number>()
  private readonly drained: (() => void)[] = []
  /** Resolves when no caller transaction is open: the mutation line has drained the work it admitted. */
  idle(): Promise<void> { return new Promise(resolve => this.drained.push(resolve)) }
  get busy(): boolean { return this.callerTxs.size > 0 }

  /** Move to a new session of the same store: the answers of the request `keep` (fed again to it) arrive through it. */
  rebind(host: HostSession) {
    this.off()
    this.host = host
    this.off = host.onNotice(n => this.route(n))
  }

  /** Fail everything in flight: the handle was discarded. The request `keep` waits for its event to be fed again to a new session. */
  failAll(discarded: Error, keep?: number) {
    // Once the Session failed, what was in flight ends with SessionFailed, as the core would answer it (harness util closedError).
    const error = this.storageFailure !== undefined ? new SessionFailed(this.storageFailure.error) : discarded
    for (const [id, p] of this.requests) if (id !== keep) { this.requests.delete(id); p.reject(error) }
    for (const c of this.txs.values()) c.fail(error)
    this.txs.clear()
    for (const p of this.ackWaiters.values()) p.reject(error)
    this.ackWaiters.clear()
  }

  close() { this.off(); this.offRecycled() }
}

const jsonBytes = (value: unknown): Uint8Array => new TextEncoder().encode(JSON.stringify(value))

const apiBytes = (id: number, body: unknown): Uint8Array => {
  const json = new TextEncoder().encode(JSON.stringify(body))
  const out = new Uint8Array(4 + json.length)
  new DataView(out.buffer).setUint32(0, id, true)
  out.set(json, 4)
  return out
}

const TX_COUNT_CAP = 512

/** One extension transaction: the line is held from `begin` to `end` or `abort`, across host awaits. Operations run one at a time. */
export class TxChannel {
  readonly id: number
  private readonly client: CoreClient
  private waiting?: Pending
  private ready?: Pending
  private chain: Promise<unknown> = Promise.resolve()
  /** Operations queued on the chain that have not been sent yet. */
  private pendingOps = 0
  private done = false
  /** True while the line is held by someone else: `begin` answered `waiting`. */
  queued = false
  /** The task record a transaction begun for an invocation read when it took the line (`ready.current`). */
  current: unknown
  /** Microtask passes since the line was granted (`ready`), counted by one self-requeuing microtask until `end`: the core holds the commit for Pi's job duration minus the passes the host's callback already spent. */
  private elapsed = 0
  private counting = false

  constructor(client: CoreClient, id: number) { this.client = client; this.id = id }

  /** The core client this transaction runs on: the Session that owns it. */
  get owner(): CoreClient { return this.client }

  /** Run the host's conversationCreated callback for a record this transaction created. */
  async created(tx: unknown, record: unknown): Promise<void> { await this.client.onConversationCreated?.(tx, record) }

  answer(body: TxAnswer) {
    if ("waiting" in body) { this.queued = true; return }
    if ("ready" in body) { this.queued = false; this.current = body.current; this.startCount(); const r = this.ready; this.ready = undefined; r?.resolve(this); return }
    // A begin the core refuses (the invocation's task is terminal, the Harness is closing) is answered as a rejection while the host waits for `ready`.
    const w = this.waiting ?? ("rejected" in body ? this.ready : undefined)
    this.waiting = this.waiting === w ? undefined : this.waiting
    if (w === this.ready) this.ready = undefined
    if (!w) return
    if ("rejected" in body) w.reject(reviveError(body.rejected, this.client.storageFailure))
    else w.resolve(body.result)
  }

  /** The handle this transaction ran on is gone: every later operation, `end` and `abort` included, fails with the same error and sends nothing to the core that replaced it. */
  private failure?: Error

  /** Count the passes the callback spends while it holds the line, up to TX_COUNT_CAP: a callback that waits on real I/O stops the count instead of spinning the event loop. */
  private startCount() {
    this.elapsed = 0; this.counting = true
    const pass = () => { if (!this.counting) return; if (++this.elapsed >= TX_COUNT_CAP) { this.counting = false; return } queueMicrotask(pass) }
    queueMicrotask(pass)
  }

  fail(error: Error) {
    this.counting = false
    this.done = true
    this.failure = error
    this.ready?.reject(error); this.ready = undefined
    this.waiting?.reject(error); this.waiting = undefined
  }

  /** The `begin` event while the line has not been granted. A recycled handle gets it again; a transaction of an invocation does not (the invocation ended with its handle). */
  private beginBytes?: Uint8Array

  /** True from `begin` until the core grants the line. */
  get fresh(): boolean { return this.ready !== undefined && this.beginBytes !== undefined }

  /** The host's commit count when `end` was sent: a commit applied since then is this transaction's (it holds the Session line until the core acknowledges). */
  private endMark?: number

  /** True when this transaction's commit was applied to the store and only the core's acknowledgement is outstanding. Only the old handle's commits count: the reopened core's `open` may commit too. */
  get committed(): boolean { return this.endMark !== undefined && this.client.host.commitsAtDrop > this.endMark }

  /** The host replaced the core handle before this transaction took the line: ask the new core for it. */
  replayBegin() {
    this.client.host.replay(EVENT.tx, this.beginBytes, step => {
      const rejected = rejection(step, this.client.storageFailure)
      if (rejected) { const r = this.ready; this.ready = undefined; r?.reject(rejected.error) }
    })
  }

  /** `settle` replaces the promise's own resolution: the answer settles the caller's promise directly, with no reaction in between (`end`). */
  private send(op: Record<string, unknown>, expect: "ready" | "result", settle?: { resolve: (value: unknown) => void; reject: (error: Error) => void }): Promise<unknown> {
    return new Promise((resolveOwn, rejectOwn) => {
      const resolve = settle?.resolve ?? resolveOwn, reject = settle?.reject ?? rejectOwn
      if (this.failure) return reject(this.failure)
      if (expect === "ready") this.ready = { resolve, reject }
      else this.waiting = { resolve, reject }
      const bytes = apiBytes(this.id, op)
      if (expect === "ready" && op.invocation === undefined) this.beginBytes = bytes
      let step: Step | undefined
      try { step = this.client.host.send(EVENT.tx, bytes, this.beginBytes === bytes ? true : undefined) } catch (e) { this.ready = this.waiting = undefined; return reject(e as Error) }
      const rejected = rejection(step, this.client.storageFailure)
      if (rejected) { this.ready = this.waiting = undefined; reject(rejected.error) }
    })
  }

  begin(options: { invocation?: number; conversationId?: number; span?: number; trace?: unknown; persistence?: { kind: "deferred"; withinMs?: number } }) {
    return this.send({ op: "begin", ...options }, "ready")
  }

  /** One `Tx` operation (`conversation`, `doc`, `appendEntry`, `createTask`, ...). */
  op<T = unknown>(op: string, args: Record<string, unknown> = {}): Promise<T> {
    this.pendingOps++
    const run = this.chain.then(() => {
      this.pendingOps--
      if (this.done) throw new Error("the transaction has ended")
      return this.send({ op, ...args }, "result")
    })
    this.chain = run.catch(() => undefined)
    return run as Promise<T>
  }

  /**
   * session.ts touch from this transaction's line job: the wait for `key`'s unpublished writer, or `undefined` when there is none (the core
   * answered in the step that carried the op). A touch behind queued operations waits its turn on the chain.
   */
  touch(key: string): Promise<void> | undefined {
    if (this.pendingOps > 0 || this.waiting !== undefined) return this.op("touch", { key }).then(() => undefined)
    if (this.done) return Promise.reject(new Error("the transaction has ended"))
    if (this.failure) return Promise.reject(this.failure)
    let answered = false
    const { promise, resolve, reject } = Promise.withResolvers<void>()
    this.waiting = { resolve: () => { answered = true; resolve() }, reject }
    let step: Step | undefined
    try { step = this.client.host.send(EVENT.tx, apiBytes(this.id, { op: "touch", key })) } catch (e) { this.waiting = undefined; reject(e as Error); return promise }
    const rejected = rejection(step, this.client.storageFailure)
    if (rejected) { this.waiting = undefined; reject(rejected.error) }
    return answered ? undefined : promise
  }

  /** Wait for the operations queued so far: `end` is sent only after them. */
  quiesce(): Promise<unknown> { return this.chain }

  /**
   * Commit: the callback's returned next task state goes with `end`. The core answers with `tx_result {result: null}` (or `{rejected}`) when the
   * commit's job outcome is delivered, 2 passes before Pi's caller resumes (PROTOCOL.md, `end`): this promise is settled by the answer itself with
   * `value`, and the one pass `runCommit`'s return adopting it takes and the caller's are the 2. It is not async; `onError` runs as the answer
   * (or a rejection of `end`) arrives.
   */
  end<T>(state: unknown, value: T, onError?: (error: unknown) => void): Promise<T> {
    this.done = true
    this.endMark = this.client.host.counters.commits
    this.counting = false
    return new Promise<T>((resolve, reject) => {
      void this.send({ op: "end", elapsed: this.elapsed, ...(state === undefined ? {} : { state }) }, "result", {
        resolve: () => { this.client.release(this.id); resolve(value) },
        reject: error => { this.client.release(this.id); try { onError?.(error) } finally { reject(error) } },
      })
    })
  }

  /** Roll back: the callback threw `error`. Settles with the job's outcome, which the core delivers in line order (session.ts #enqueue fences). */
  async abort(error: unknown): Promise<void> {
    await this.chain
    this.done = true
    this.counting = false
    const body = { name: (error as Error)?.name ?? "Error", message: (error as Error)?.message ?? String(error) }
    if (this.failure) { this.client.release(this.id); return }
    await new Promise<void>(resolve => {
      const settled = () => { this.client.release(this.id); resolve() }
      void this.send({ op: "abort", error: body }, "result", { resolve: settled, reject: settled })
    })
  }
}

export { FatalError, RejectedError }
