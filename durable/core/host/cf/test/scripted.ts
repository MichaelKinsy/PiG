// A core that is a function: the test says what each event produces. It drives the host loop, the client and the effect
// executors without Wasm, so each test names exactly the step it needs.
import { EFFECT, EVENT, NOTICE } from "../src/abi.ts"
import { HostSession, type CoreHandle, type HostOptions } from "../src/host.ts"
import { createEffectHandlers, type EffectRuntime } from "../src/effects.ts"
import { CoreClient } from "../src/client.ts"
import { nodeStore } from "../src/store-node.ts"
import type { Commit, Effect, Notice, Rows, Step } from "../src/wire.ts"

const enc = new TextEncoder()
const u32 = (n: number) => { const b = new Uint8Array(4); new DataView(b.buffer).setUint32(0, n, true); return b }
const cat = (...parts: Uint8Array[]) => { const out = new Uint8Array(parts.reduce((s, p) => s + p.length, 0)); let o = 0; for (const p of parts) { out.set(p, o); o += p.length } return out }

/** A notice or effect a test writes by hand: its origin (ABI section 5.4) is 0 unless it says. */
type Bare<T> = Omit<T, "origin"> & { origin?: number }
export type Spec = { commits?: Commit[]; notices?: Bare<Notice>[]; effects?: Bare<Effect>[]; order?: Array<[number, number]>; status?: 0 | 1 | 2; error?: { name: string; message: string } }
export type Seen = { kind: number; payload: Uint8Array; id?: number; body?: any; origin?: number }

/** A model_context payload is a recipe (ABI section 10.2); these are one text segment. */
export const textRecipe = (text: Uint8Array): Uint8Array => {
  const out = new Uint8Array(4 + 1 + 4 + text.length)
  const dv = new DataView(out.buffer)
  dv.setUint32(0, 1, true)
  dv.setUint32(5, text.length, true)
  out.set(text, 9)
  return out
}
export const effect = (id: number, kind: number, json: unknown): Effect => {
  const text = enc.encode(JSON.stringify(json))
  return { id, kind, origin: 0, payload: kind === EFFECT.model_context ? textRecipe(text) : text }
}
export const apiResult = (id: number, body: unknown): Notice => ({ origin: 0, kind: NOTICE.api_result, payload: cat(u32(id), enc.encode(JSON.stringify(body))) })
export const txResult = (id: number, body: unknown): Notice => ({ origin: 0, kind: NOTICE.tx_result, payload: cat(u32(id), enc.encode(JSON.stringify(body))) })
export const progressAck = (waitId: number, outcome: 0 | 1, error?: unknown): Notice => ({ origin: 0, kind: NOTICE.progress_ack, payload: cat(u32(waitId), Uint8Array.of(outcome), enc.encode(error ? JSON.stringify(error) : "")) })
export const published = (body: unknown): Notice => ({ origin: 0, kind: NOTICE.published, payload: enc.encode(JSON.stringify(body)) })

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
  step(kind: number, payload: Uint8Array | undefined, _now: number, origin = 0): Step {
    const ev: Seen = { kind, payload: payload ?? new Uint8Array(0), origin, ...decode(payload ?? new Uint8Array(0)) }
    this.seen.push(ev)
    const spec = this.respond(ev)
    // What the real core always answers (PROTOCOL.md): `opened` when the scheduler's open job finished, and the close it ran behind its line as the api_result of request 0.
    if (spec === undefined && kind === EVENT.api && ev.body?.op === "opened") return toStep({ notices: [apiResult(ev.id!, { ok: null })] })
    if (spec === undefined && kind === EVENT.close) return toStep({ notices: [apiResult(0, { ok: null })] })
    return toStep(spec)
  }
  rows(_answers: Rows[], _now: number): Step { return toStep(undefined) }
  free() { this.freed = true }
  /** The events of a kind; the `opened` request every Harness.open makes is not one a script asserts on. */
  ofKind(kind: number) { return this.seen.filter(e => e.kind === kind && !(kind === EVENT.api && e.body?.op === "opened")) }
}

const toStep = (spec: Spec | undefined): Step => ({
  status: spec?.status ?? 0, commits: spec?.commits ?? [], reads: [], notices: (spec?.notices ?? []).map(n => ({ ...n, origin: n.origin ?? 0 })), effects: (spec?.effects ?? []).map(e => ({ ...e, origin: e.origin ?? 0 })), bytes: 0,
  ...(spec?.order ? { order: spec.order } : {}),
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

/** The api requests the test's own calls made: the Harness's open-time `opened` request is not one of them. */
export const apis = (core: ScriptedCore): Seen[] => core.ofKind(EVENT.api).filter(e => e.body?.op !== "opened")
