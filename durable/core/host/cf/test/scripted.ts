// A core that is a function: the test says what each event produces. It drives the host loop, the client and the effect
// executors without Wasm, so each test names exactly the step it needs.
import { EFFECT, NOTICE } from "../src/abi.ts"
import { HostSession, type CoreHandle, type HostOptions } from "../src/host.ts"
import { createEffectHandlers, type EffectRuntime } from "../src/effects.ts"
import { CoreClient } from "../src/client.ts"
import { nodeStore } from "../src/store-node.ts"
import type { Commit, Effect, Notice, Rows, Step } from "../src/wire.ts"

const enc = new TextEncoder()
const u32 = (n: number) => { const b = new Uint8Array(4); new DataView(b.buffer).setUint32(0, n, true); return b }
const cat = (...parts: Uint8Array[]) => { const out = new Uint8Array(parts.reduce((s, p) => s + p.length, 0)); let o = 0; for (const p of parts) { out.set(p, o); o += p.length } return out }

export type Spec = { commits?: Commit[]; notices?: Notice[]; effects?: Effect[]; status?: 0 | 1 | 2; error?: { name: string; message: string } }
export type Seen = { kind: number; payload: Uint8Array; id?: number; body?: any }

export const effect = (id: number, kind: number, json: unknown): Effect => ({ id, kind, payload: enc.encode(JSON.stringify(json)) })
export const apiResult = (id: number, body: unknown): Notice => ({ kind: NOTICE.api_result, payload: cat(u32(id), enc.encode(JSON.stringify(body))) })
export const txResult = (id: number, body: unknown): Notice => ({ kind: NOTICE.tx_result, payload: cat(u32(id), enc.encode(JSON.stringify(body))) })
export const progressAck = (waitId: number, outcome: 0 | 1, error?: unknown): Notice => ({ kind: NOTICE.progress_ack, payload: cat(u32(waitId), Uint8Array.of(outcome), enc.encode(error ? JSON.stringify(error) : "")) })
export const published = (body: unknown): Notice => ({ kind: NOTICE.published, payload: enc.encode(JSON.stringify(body)) })

const dec = new TextDecoder()
/** Decode the events whose first four bytes are an ID and the rest JSON (api, tx, model_event). */
const decode = (payload: Uint8Array): { id?: number; body?: any } => {
  try { return { id: new DataView(payload.buffer, payload.byteOffset, 4).getUint32(0, true), body: JSON.parse(dec.decode(payload.subarray(4))) } } catch { /* not that shape */ }
  try { return { body: JSON.parse(dec.decode(payload)) } } catch { return {} }
}

export class ScriptedCore implements CoreHandle {
  readonly seen: Seen[] = []
  freed = false
  respond: (ev: Seen) => Spec | undefined
  constructor(respond: (ev: Seen) => Spec | undefined = () => undefined) { this.respond = respond }
  step(kind: number, payload: Uint8Array | undefined, _now: number): Step {
    const ev: Seen = { kind, payload: payload ?? new Uint8Array(0), ...decode(payload ?? new Uint8Array(0)) }
    this.seen.push(ev)
    return toStep(this.respond(ev))
  }
  rows(_answers: Rows[], _now: number): Step { return toStep(undefined) }
  free() { this.freed = true }
  ofKind(kind: number) { return this.seen.filter(e => e.kind === kind) }
}

const toStep = (spec: Spec | undefined): Step => ({
  status: spec?.status ?? 0, commits: spec?.commits ?? [], reads: [], notices: spec?.notices ?? [], effects: spec?.effects ?? [], bytes: 0,
  ...(spec?.error ? { error: { ...spec.error, raw: JSON.stringify(spec.error) } } : {}),
})

export function rig(respond: (ev: Seen) => Spec | undefined, runtime: Partial<EffectRuntime> & Pick<EffectRuntime, "registry" | "models">, options: Partial<HostOptions> = {}) {
  const core = new ScriptedCore(respond)
  const store = nodeStore(":memory:", { wal: false })
  let host!: HostSession
  const client = () => new CoreClient(host)
  const handlers: Partial<Record<number, any>> = {}
  host = new HostSession({ core, sqlTable: [], store, handlers, clock: () => 1000, ...options })
  const c = client()
  const full: EffectRuntime = { client: c, report: () => {}, hostApi: { client: c }, ...runtime }
  Object.assign(handlers, createEffectHandlers(full))
  return { core, host, client: c, store }
}

export { EFFECT, NOTICE }
