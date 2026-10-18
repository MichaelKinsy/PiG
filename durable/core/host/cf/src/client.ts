// The request side of the host: `api` requests (ABI section 8.1) and the extension transaction channel (section 8) as
// promises. Answers arrive as notices after the step that carried the request; every pending request fails when the handle
// is discarded.
import { EVENT, NOTICE } from "./abi.ts"
import { reviveError, SessionFailed } from "./errors.ts"
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
type Pending = { resolve: (value: any) => void; reject: (error: Error) => void; sent?: Sent; waits?: boolean }
const dec = new TextDecoder()

const split = (n: Notice): [number, unknown] => {
  const id = new DataView(n.payload.buffer, n.payload.byteOffset, 4).getUint32(0, true)
  return [id, JSON.parse(dec.decode(n.payload.subarray(4)))]
}

/** A step that came back rejected becomes the error Pi throws. */
const rejection = (step: Step | undefined, failure?: Error): Error | undefined =>
  step && step.status === 1 ? reviveError({ name: step.error?.name, message: step.error?.message ?? step.error?.raw }, failure) : undefined

/** An answer to a rejected `api` request: the extra arguments to repeat it with, or undefined to reject. */
export type ApiRetry = (error: Error) => Record<string, unknown> | undefined

export class CoreClient {
  /** The Storage error that failed the Session, as the object the host's Storage threw (errors.ts reviveError). */
  storageFailure?: Error
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
  docCache: { readonly cached: number; readonly idle: number; readonly attached: number; readonly sweeps: number } = { cached: 0, idle: 0, attached: 0, sweeps: 0 }
  /** The last wake the core published, and the host's callback for each (HarnessOptions.onWake); the core arms no timer for sleeping tasks when the host takes it. */
  wake: { active: boolean; at?: number } | undefined
  onWake: ((wake: { active: boolean; at?: number }) => void) | undefined
  /** The sizes of the scheduler's ownership indexes, as the core's last index_sizes notice said (scheduler.ts indexSizes is synchronous). */
  indexSizes: Readonly<Record<"live" | "settled" | "edges" | "below" | "roots" | "unloaded" | "dropQueue", number>> = { live: 0, settled: 0, edges: 0, below: 0, roots: 0, unloaded: 0, dropQueue: 0 }
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
        if (rejected && this.requests.get(id) === p) { this.requests.delete(id); p.reject(rejected) }
      })
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
        this.docCache = JSON.parse(dec.decode(n.payload))
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

  /** session.ts fail: the waits underway (waitForIdle, waitForTask, ...) reject with the Session's failure before anything the failure ends can cancel them. */
  failWaits(error: Error): void {
    for (const [id, request] of [...this.requests]) if (request.waits) { this.requests.delete(id); request.reject(error) }
  }

  /** One `api` request. Rejects with Pi's error when the core answers `{error}` or rejects the event, and with the discard error when the handle dies first. */
  api<T = unknown>(op: string, args: Record<string, unknown> = {}, revive?: (value: T) => T, retry?: ApiRetry): Promise<T> { return this.apiNow<T>(op, args, revive, retry).promise }

  /** `api`, and whether the core answered inside the send (the promise is already settled): a caller that mirrors a Pi call which returns an already-resolved promise returns this one as it is. */
  apiNow<T = unknown>(op: string, args: Record<string, unknown> = {}, revive?: (value: T) => T, retry?: ApiRetry): { promise: Promise<T>; settled: boolean } {
    let settled = false
    const answer = new Promise<T>((resolve, reject) => {
      // session.ts assertUsable: a failed Session answers every call with SessionFailed.
      if (this.storageFailure !== undefined) { settled = true; return reject(new SessionFailed(this.storageFailure)) }
      const id = this.nextRequest++
      const bytes = apiBytes(id, { op, ...args })
      // Every api op the host sends is a read, a wait, or a registration the new core takes again; the storage-mode writes run on hosts without a renewal.
      this.requests.set(id, { waits: op.startsWith("waitFor"), sent: { kind: EVENT.api, bytes, idempotent: !op.startsWith("storage."), bound: args.invocation !== undefined }, resolve: value => { settled = true; resolve(revive ? revive(value as T) : value as T) }, reject: error => {
        // A rejection the caller answers by repeating the request (a read that needs the definition's migration) is settled by the repeat's answer, so a success spends no pass of its own.
        let again: Record<string, unknown> | undefined
        try { again = retry?.(error) } catch (e) { settled = true; return reject(e as Error) }
        if (again === undefined) { settled = true; return reject(error) }
        this.apiNow<T>(op, { ...args, ...again }, revive).promise.then(value => { settled = true; resolve(value) }, e => { settled = true; reject(e) })
      } })
      let step: Step | undefined
      try { step = this.host.send(EVENT.api, bytes, id) } catch (e) { this.requests.delete(id); settled = true; return reject(e as Error) }
      const rejected = rejection(step, this.storageFailure)
      if (rejected) { this.requests.delete(id); settled = true; reject(rejected) }
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
      if (rejected) { this.requests.delete(id); reject(rejected) }
    })
  }

  /** A `submit` event (ABI section 5, kind 3): resolves with the admitted submission's ID once the core has committed it, after the commit's `published` notice. `id` is the api_result key the core answers on. */
  submit<T = { id: number }>(body: Record<string, unknown>, revive?: (placed: { id: number }) => T): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const id = this.nextRequest++
      const bytes = jsonBytes({ ...body, id })
      // A request ID makes a second admission return the first (inbox.Admit); without one a second admission is a second submission.
      // `revive` runs as the answer routes, so the caller's promise resolves at the tick Pi's awaiting method resumes.
      this.requests.set(id, { sent: { kind: EVENT.submit, bytes, idempotent: typeof body.requestId === "string", bound: body.invocation !== undefined }, resolve: value => resolve(revive ? revive(value as { id: number }) : value as T), reject })
      let step: Step | undefined
      try { step = this.host.send(EVENT.submit, bytes, id) } catch (e) { this.requests.delete(id); return reject(e as Error) }
      const rejected = rejection(step, this.storageFailure)
      if (rejected) { this.requests.delete(id); reject(rejected) }
    })
  }

  /** An `abort` event that expects an answer (`abortSubmission`, `abortTask`): the core echoes `requestId` in an api_result notice. */
  abort<T = unknown>(body: Record<string, unknown>, revive?: (answer: T) => T): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const id = this.nextRequest++
      const bytes = jsonBytes({ ...body, requestId: id })
      // Aborting a task or a conversation again is harmless; withdrawing a submission again answers `settled` where the first answered `aborted`.
      this.requests.set(id, { sent: { kind: EVENT.abort, bytes, idempotent: body.submissionId === undefined, bound: body.invocation !== undefined }, resolve: value => resolve(revive ? revive(value as T) : value as T), reject })
      let step: Step | undefined
      try { step = this.host.send(EVENT.abort, bytes, id) } catch (e) { this.requests.delete(id); return reject(e as Error) }
      const rejected = rejection(step, this.storageFailure)
      if (rejected) { this.requests.delete(id); reject(rejected) }
    })
  }

  /** Open a transaction on the Session line. Resolves at `ready`; `waiting` only means the line is held and `ready` follows. */
  begin(options: { invocation?: number; conversationId?: number } = {}): Promise<TxChannel> {
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
    const error = this.storageFailure !== undefined ? new SessionFailed(this.storageFailure) : discarded
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
  private done = false
  /** True while the line is held by someone else: `begin` answered `waiting`. */
  queued = false
  /** The core answered `end` with `gated`: the commit waits for the host's Storage. */
  private gated = false
  /** The task record a transaction begun for an invocation read when it took the line (`ready.current`). */
  current: unknown
  /** Microtask passes since the line was granted (`ready`), counted by one self-requeuing microtask until `end`: the core holds the commit for Pi's job duration minus the passes the host's callback already spent. */
  private elapsed = 0
  private counting = false

  constructor(client: CoreClient, id: number) { this.client = client; this.id = id }

  /** Run the host's conversationCreated callback for a record this transaction created. */
  async created(tx: unknown, record: unknown): Promise<void> { await this.client.onConversationCreated?.(tx, record) }

  answer(body: TxAnswer) {
    if ("waiting" in body) { this.queued = true; return }
    // The host's Storage holds this transaction's commit (PROTOCOL.md, "Storage gate"): the end is answered as a result or a rejection once the Storage settles it.
    if ("gated" in body) { this.gated = true; return }
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
      if (rejected) { const r = this.ready; this.ready = undefined; r?.reject(rejected) }
    })
  }

  private send(op: Record<string, unknown>, expect: "ready" | "result"): Promise<unknown> {
    return new Promise((resolve, reject) => {
      if (this.failure) return reject(this.failure)
      if (expect === "ready") this.ready = { resolve, reject }
      else this.waiting = { resolve, reject }
      const bytes = apiBytes(this.id, op)
      if (expect === "ready" && op.invocation === undefined) this.beginBytes = bytes
      let step: Step | undefined
      try { step = this.client.host.send(EVENT.tx, bytes, this.beginBytes === bytes ? true : undefined) } catch (e) { this.ready = this.waiting = undefined; return reject(e as Error) }
      const rejected = rejection(step, this.client.storageFailure)
      if (rejected) { this.ready = this.waiting = undefined; reject(rejected) }
    })
  }

  begin(options: { invocation?: number; conversationId?: number }) {
    return this.send({ op: "begin", ...options }, "ready")
  }

  /** One `Tx` operation (`conversation`, `doc`, `appendEntry`, `createTask`, ...). */
  op<T = unknown>(op: string, args: Record<string, unknown> = {}): Promise<T> {
    const run = this.chain.then(() => {
      if (this.done) throw new Error("the transaction has ended")
      return this.send({ op, ...args }, "result")
    })
    this.chain = run.catch(() => undefined)
    return run as Promise<T>
  }

  /** Wait for the operations queued so far: `end` is sent only after them. */
  quiesce(): Promise<unknown> { return this.chain }

  /**
   * Commit: the callback's returned next task state goes with `end`. The core answers with `tx_result {result: null}` when the commit's line
   * job completes, after the hops chain of its duration, and this promise settles on that ack with `value`: a caller's `api.commit` then resumes
   * as it does in Pi after the job. It is not async: the one reaction on the ack is the only host hop before the caller's promise adopts it.
   * A rejected `end` rejects at once; `onError` runs in that reaction and may replace the error.
   */
  end<T>(state: unknown, value: T, onError?: (error: unknown) => void): Promise<T> {
    this.done = true
    this.endMark = this.client.host.counters.commits
    this.counting = false
    return this.send({ op: "end", elapsed: this.elapsed, ...(state === undefined ? {} : { state }) }, "result").then(
      () => { this.client.release(this.id); return value },
      error => { this.client.release(this.id); onError?.(error); throw error },
    )
  }

  /** Roll back: the callback threw `error`. */
  async abort(error: unknown): Promise<void> {
    await this.chain
    this.done = true
    const body = { name: (error as Error)?.name ?? "Error", message: (error as Error)?.message ?? String(error) }
    try { if (!this.failure) this.client.host.send(EVENT.tx, apiBytes(this.id, { op: "abort", error: body })) } finally { this.client.release(this.id) }
  }
}

export { FatalError, RejectedError }
