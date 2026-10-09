// The request side of the host: `api` requests (ABI section 8.1) and the extension transaction channel (section 8) as
// promises. Answers arrive as notices after the step that carried the request; every pending request fails when the handle
// is discarded.
import { EVENT, NOTICE } from "./abi.ts"
import { reviveError } from "./errors.ts"
import { FatalError, RejectedError, type HostSession } from "./host.ts"
import type { ApiAnswer, TxAnswer } from "./protocol.ts"
import type { Notice, Step } from "./wire.ts"

type Pending = { resolve: (value: any) => void; reject: (error: Error) => void }
const dec = new TextDecoder()

const split = (n: Notice): [number, unknown] => {
  const id = new DataView(n.payload.buffer, n.payload.byteOffset, 4).getUint32(0, true)
  return [id, JSON.parse(dec.decode(n.payload.subarray(4)))]
}

/** A step that came back rejected becomes the error Pi throws. */
const rejection = (step: Step | undefined): Error | undefined =>
  step && step.status === 1 ? reviveError({ name: step.error?.name, message: step.error?.message ?? step.error?.raw }) : undefined

export class CoreClient {
  readonly host: HostSession
  private nextRequest = 1
  private nextTx = 1
  private readonly requests = new Map<number, Pending>()
  private readonly txs = new Map<number, TxChannel>()
  private readonly ackWaiters = new Map<number, Pending>()
  private nextWait = 1
  private readonly off: () => void

  constructor(host: HostSession) {
    this.host = host
    this.off = host.onNotice(n => this.route(n))
  }

  private route(n: Notice) {
    switch (n.kind) {
      case NOTICE.api_result: {
        const [id, body] = split(n)
        const p = this.requests.get(id)
        if (!p) return
        this.requests.delete(id)
        const answer = body as ApiAnswer
        if ("error" in answer) p.reject(reviveError(answer.error))
        else p.resolve(answer.ok)
        return
      }
      case NOTICE.tx_result: {
        const [id, body] = split(n)
        this.txs.get(id)?.answer(body as TxAnswer)
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
        p.reject(reviveError(text ? JSON.parse(text) : { name: "StorageRejected", message: "the progress commit was rejected" }))
      }
    }
  }

  /** Requests, transactions and acks still waiting: a handle with any of them is not idle. */
  get pending(): number { return this.requests.size + this.txs.size + this.ackWaiters.size }

  /** Reserve a waitId for a tool details update: the promise settles at the progress_ack notice. */
  expectAck(): { waitId: number; settled: Promise<void> } {
    const waitId = this.nextWait++
    return { waitId, settled: new Promise((resolve, reject) => this.ackWaiters.set(waitId, { resolve, reject })) }
  }

  /** One `api` request. Rejects with Pi's error when the core answers `{error}` or rejects the event, and with the discard error when the handle dies first. */
  api<T = unknown>(op: string, args: Record<string, unknown> = {}): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const id = this.nextRequest++
      this.requests.set(id, { resolve, reject })
      let step: Step | undefined
      try { step = this.host.send(EVENT.api, apiBytes(id, { op, ...args })) } catch (e) { this.requests.delete(id); return reject(e as Error) }
      const rejected = rejection(step)
      if (rejected) { this.requests.delete(id); reject(rejected) }
    })
  }

  /** An `inspect` event (ABI section 5, kind 16): reads core state the api table does not name. The core echoes `requestId` in an api_result notice. */
  inspect<T = unknown>(query: Record<string, unknown>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const id = this.nextRequest++
      this.requests.set(id, { resolve, reject })
      let step: Step | undefined
      try { step = this.host.sendJSON(EVENT.inspect, { ...query, requestId: id }) } catch (e) { this.requests.delete(id); return reject(e as Error) }
      const rejected = rejection(step)
      if (rejected) { this.requests.delete(id); reject(rejected) }
    })
  }

  /** An `abort` event that expects an answer (`abortSubmission`, `abortTask`): the core echoes `requestId` in an api_result notice. */
  abort<T = unknown>(body: Record<string, unknown>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const id = this.nextRequest++
      this.requests.set(id, { resolve, reject })
      let step: Step | undefined
      try { step = this.host.sendJSON(EVENT.abort, { ...body, requestId: id }) } catch (e) { this.requests.delete(id); return reject(e as Error) }
      const rejected = rejection(step)
      if (rejected) { this.requests.delete(id); reject(rejected) }
    })
  }

  /** Open a transaction on the Session line. Resolves at `ready`; `waiting` only means the line is held and `ready` follows. */
  async begin(options: { invocation?: number; conversationId?: number } = {}): Promise<TxChannel> {
    const id = this.nextTx++
    const channel = new TxChannel(this, id)
    this.txs.set(id, channel)
    try { await channel.begin(options) } catch (e) { this.txs.delete(id); throw e }
    return channel
  }

  /** Bind a channel the core already opened: a `callback` effect carries its txId (ABI effect 13). */
  bind(txId: number): TxChannel {
    const channel = new TxChannel(this, txId)
    this.txs.set(txId, channel)
    return channel
  }

  release(txId: number) { this.txs.delete(txId) }

  /** Fail everything in flight: the handle was discarded. */
  failAll(error: Error) {
    for (const p of this.requests.values()) p.reject(error)
    this.requests.clear()
    for (const c of this.txs.values()) c.fail(error)
    this.txs.clear()
    for (const p of this.ackWaiters.values()) p.reject(error)
    this.ackWaiters.clear()
  }

  close() { this.off() }
}

const apiBytes = (id: number, body: unknown): Uint8Array => {
  const json = new TextEncoder().encode(JSON.stringify(body))
  const out = new Uint8Array(4 + json.length)
  new DataView(out.buffer).setUint32(0, id, true)
  out.set(json, 4)
  return out
}

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

  constructor(client: CoreClient, id: number) { this.client = client; this.id = id }

  answer(body: TxAnswer) {
    if ("waiting" in body) { this.queued = true; return }
    if ("ready" in body) { this.queued = false; const r = this.ready; this.ready = undefined; r?.resolve(undefined); return }
    const w = this.waiting
    this.waiting = undefined
    if (!w) return
    if ("rejected" in body) w.reject(reviveError(body.rejected))
    else w.resolve(body.result)
  }

  fail(error: Error) {
    this.done = true
    this.ready?.reject(error); this.ready = undefined
    this.waiting?.reject(error); this.waiting = undefined
  }

  private send(op: Record<string, unknown>, expect: "ready" | "result"): Promise<unknown> {
    return new Promise((resolve, reject) => {
      if (expect === "ready") this.ready = { resolve, reject }
      else this.waiting = { resolve, reject }
      let step: Step | undefined
      try { step = this.client.host.send(EVENT.tx, apiBytes(this.id, op)) } catch (e) { this.ready = this.waiting = undefined; return reject(e as Error) }
      const rejected = rejection(step)
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

  /** Commit: the callback's returned next task state goes with `end`. Resolves once the commit is applied; a rejected step is Pi's rejection. */
  async end(state?: unknown): Promise<void> {
    await this.chain
    this.done = true
    try {
      await new Promise<void>((resolve, reject) => {
        let step: Step | undefined
        try { step = this.client.host.send(EVENT.tx, apiBytes(this.id, { op: "end", ...(state === undefined ? {} : { state }) })) } catch (e) { return reject(e as Error) }
        const rejected = rejection(step)
        if (rejected) reject(rejected)
        else resolve()
      })
    } finally { this.client.release(this.id) }
  }

  /** Roll back: the callback threw `error`. */
  async abort(error: unknown): Promise<void> {
    await this.chain
    this.done = true
    const body = { name: (error as Error)?.name ?? "Error", message: (error as Error)?.message ?? String(error) }
    try { this.client.host.send(EVENT.tx, apiBytes(this.id, { op: "abort", error: body })) } finally { this.client.release(this.id) }
  }
}

export { FatalError, RejectedError }
