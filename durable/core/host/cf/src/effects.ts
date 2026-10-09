// Effect executors (ABI section 6): what the host does when a step asks for model calls, tools, hooks, prompt sections,
// environments, deferred polls, callbacks and custom task phases. Host code runs here; every outcome returns to the core as
// an event. A user-code throw is an outcome the core classifies; a host or protocol failure throws out of the handler and the
// HostSession discards the handle (ADR D16).
import { BACKGROUND_CONTEXT, withAbortSignal } from "@earendil-works/chord/context"
import type { Context } from "@earendil-works/chord"
import { EFFECT } from "./abi.ts"
import type { CoreClient, TxChannel } from "./client.ts"
import type { EffectContext, EffectHandler, HostSession } from "./host.ts"
import {
  HOOK_ARGUMENTS, wireError,
  type CallbackEffect, type DeferredEffect, type EnvEffect, type HookEffect, type ModelContextEffect, type PhaseEffect, type SectionEffect, type ToolEffect,
} from "./protocol.ts"
import type { HostRegistry } from "./registry.ts"
import type { Effect } from "./wire.ts"
import { documentReader, invocationApi, invocationCommit, type HostApiOptions } from "./facade/hostapi.ts"
import { runCommit, type TxProxy } from "./facade/tx.ts"

const dec = new TextDecoder()
const enc = new TextEncoder()
const parse = <T>(effect: Effect): T => JSON.parse(dec.decode(effect.payload)) as T

/** The subset of pi-ai `Models` the host calls (generation.ts:229,259,402). */
export interface ModelsLike {
  getModel(provider: string, modelId: string): unknown | undefined
  streamSimple(model: any, context: { messages: unknown[] }, options?: any): AsyncIterable<any> & { result(): Promise<any> }
  fetchDeferred?(model: any, handle: unknown, options?: { signal?: AbortSignal }): Promise<any>
  cancelDeferred?(model: any, handle: unknown, options?: { signal?: AbortSignal }): Promise<void>
}

export interface EffectRuntime {
  client: CoreClient
  registry: HostRegistry
  models: ModelsLike
  /** `HarnessOptions.env`; absent means tools and sections see `env: undefined`. */
  env?: (target: { conversationId: number; cwd?: string; read: unknown }, context: Context) => unknown | Promise<unknown>
  /** `HarnessOptions.onReport`: extension failures that do not fail the calling operation. */
  report(error: unknown): void
  /** Credentials for a wire-plane request (`model_http`'s `auth`): headers to add. */
  authorize?: (auth: unknown) => Record<string, string> | Promise<Record<string, string>>
  fetch?: typeof fetch
  /** Host code Pi runs inside an open commit callback, by `callback` effect name. */
  callbacks?: Record<string, (payload: any, tx: TxChannel, context: Context) => unknown | Promise<unknown>>
  /** A custom task's phase or abort handler: the Harness layer builds the TaskRuntime. */
  phase?: (effect: PhaseEffect, context: Context) => Promise<void>
  /** Hooks the host answers itself, by `hook` effect name: `resolveAgent` (wraps are host code), `conversationCreated`. They take no registry handler. */
  hostHooks?: Record<string, (payload: any, context: Context) => unknown | Promise<unknown>>
  /** The registry snapshot object tools and sections see as `api.registry` (the Harness layer's published state). */
  snapshot?: () => unknown
  hostApi: HostApiOptions
}

const thrown = (error: unknown) => wireError(error)

/** Batches the events a stream yields before the next macrotask (or MAX_BATCH events) into a single `model_event`. pi-ai mutates one partial message in place across events, so serializing every event of a batch would copy the whole answer once per delta. Only the last event of a batch carries its `partial`; an earlier event carries `partial: null`, unless the content was still empty when it arrived, the one case the core's partial throttle must see (CONTRACT section 3.9: partials with empty `content` are skipped). PROTOCOL.md records the rule. */
const MAX_BATCH = 64

class EventBatch {
  private readonly events: any[] = []
  private armed = false
  private readonly deliver: (json: string) => void
  constructor(deliver: (json: string) => void) { this.deliver = deliver }
  push(event: any) {
    const terminal = event.type === "done" || event.type === "error"
    const partial = event.partial
    const slim = partial === undefined ? event : partial.content?.length === 0 ? { ...event, partial: { ...partial, content: [] } } : { ...event, partial: null, __partial: partial }
    this.events.push(slim)
    if (terminal || this.events.length >= MAX_BATCH) return this.flush()
    if (!this.armed) { this.armed = true; setTimeout(() => { this.armed = false; this.flush() }, 0) }
  }
  flush() {
    if (!this.events.length) return
    const last = this.events.length - 1
    const json = JSON.stringify(this.events.map((event, i) => {
      if (event.__partial === undefined) return event
      const { __partial, ...rest } = event
      return i === last ? { ...rest, partial: __partial } : rest
    }))
    this.events.length = 0
    this.deliver(json)
  }
}

export function createEffectHandlers(rt: EffectRuntime): Partial<Record<number, EffectHandler>> {
  const kept = new Map<number, unknown[]>()
  const envs = new Map<number, unknown>()
  const contextOf = (signal: AbortSignal): Context => withAbortSignal(signal, BACKGROUND_CONTEXT)
  const bytes = (v: Uint8Array | string) => typeof v === "string" ? enc.encode(v) : v

  const modelContext: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<ModelContextEffect>(effect)
    let messages: unknown[]
    if (p.extends !== undefined) {
      const base = kept.get(p.extends)
      if (!base) throw new Error(`model_context ${effect.id} extends effect ${p.extends}, whose message list the host no longer holds`)
      kept.delete(p.extends)
      for (const message of p.append ?? []) base.push(message)
      messages = base
    } else messages = p.context?.messages ?? []
    kept.set(effect.id, messages)
    // A list nobody extends would stay; the oldest go first, and the core never extends an effect this far back.
    if (kept.size > MAX_KEPT_LISTS) kept.delete(kept.keys().next().value!)
    const model = rt.models.getModel(p.model.provider, p.model.modelId)
    const fail = (message: string) => host.modelEvents(effect.id, [{ type: "error", reason: "error", error: { role: "assistant", content: [], api: "", provider: p.model.provider, model: p.model.modelId, usage: emptyUsage(), stopReason: "error", errorMessage: message, timestamp: host.now() } }])
    if (model === undefined) return fail(`Model ${p.model.provider}/${p.model.modelId} is not available`)
    const batch = new EventBatch(json => host.modelEventsRaw(effect.id, json))
    try {
      const stream = rt.models.streamSimple(model, { messages }, { ...p.options, signal })
      for await (const event of stream) batch.push(event)
      batch.flush()
    } catch (error) {
      batch.flush()
      if (!signal.aborted) fail((error as Error)?.message ?? String(error))
    }
  }

  const modelHttp: EffectHandler = async (effect, { host, signal }) => {
    const view = new DataView(effect.payload.buffer, effect.payload.byteOffset, effect.payload.byteLength)
    const headLength = view.getUint32(0, true)
    const head = JSON.parse(dec.decode(effect.payload.subarray(4, 4 + headLength))) as { method: string; url: string; headers: Record<string, string>; auth?: unknown }
    const body = assembleRecipe(effect.payload.subarray(4 + headLength))
    const doFetch = rt.fetch ?? fetch
    try {
      const credentials = rt.authorize && head.auth !== undefined ? await rt.authorize(head.auth) : {}
      const response = await doFetch(head.url, { method: head.method, headers: { ...head.headers, ...credentials }, body: head.method === "GET" ? undefined : body, signal })
      host.modelBytes(effect.id, 0, JSON.stringify({ status: response.status, statusText: response.statusText, headers: Object.fromEntries(response.headers) }))
      if (response.body) {
        const reader = response.body.getReader()
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          host.modelBytes(effect.id, 1, value)
        }
      }
      host.modelBytes(effect.id, 2, "")
    } catch (error) {
      if (signal.aborted) return
      host.modelBytes(effect.id, 3, JSON.stringify(thrown(error)))
    }
  }

  const tool: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<ToolEffect>(effect)
    const context = contextOf(signal)
    const registration = rt.registry.tool(p.toolName, p.handler)
    let ended = false
    const live = () => { if (ended) throw new Error(`Tool call ${p.callId} has settled`) }
    const who = { taskId: p.taskId, conversationId: p.conversationId }
    const base = invocationApi(rt.hostApi, who)
    const api = {
      ...base,
      callId: p.callId,
      registry: rt.snapshot?.(),
      env: envs.get(p.taskId),
      outputWindow: p.outputWindow ? { ...p.outputWindow, minIntervalMs: 0, bytesPerSecond: 0 } : undefined,
      output: (chunk: string | Uint8Array, skipped?: { bytes: number; newlines: number; endsWithNewline: boolean }) => {
        live()
        const data = bytes(chunk)
        if (skipped === undefined || skipped.bytes === 0) return host.toolProgress(effect.id, 0, 0, data)
        const meta = enc.encode(JSON.stringify(skipped))
        const framed = new Uint8Array(4 + meta.length + data.length)
        new DataView(framed.buffer).setUint32(0, meta.length, true)
        framed.set(meta, 4)
        framed.set(data, 4 + meta.length)
        host.toolProgress(effect.id, 0, skipped.bytes, framed)
      },
      diagnostic: (diagnostic: unknown) => { live(); host.toolProgress(effect.id, 2, 0, JSON.stringify(diagnostic)) },
      details: async (value: unknown, detailsContext: Context) => {
        live()
        detailsContext.abortSignal?.throwIfAborted()
        const { waitId, settled } = rt.client.expectAck()
        host.toolProgress(effect.id, 1, 0, JSON.stringify(value), waitId)
        await Promise.race([settled, abortion(detailsContext)])
      },
      commit: invocationCommit(rt.hostApi, { conversationId: p.conversationId }),
      createTask: async (task: any, input: unknown, options: unknown, taskContext: Context) => {
        let id: unknown
        await invocationCommit(rt.hostApi, { conversationId: p.conversationId })(async (tx: TxProxy) => { id = await tx.createTask(task, input, options); return undefined }, taskContext)
        return id
      },
    }
    try {
      const result = await registration.execute(p.arguments, api, context)
      ended = true
      host.toolDone(effect.id, 0, result ?? null)
    } catch (error) {
      ended = true
      if (signal.aborted) return
      host.toolDone(effect.id, 1, thrown(error))
    }
  }

  const hook: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<HookEffect>(effect)
    const context = contextOf(signal)
    const own = rt.hostHooks?.[p.name]
    if (own) {
      try { host.hookDone(effect.id, 0, await own(p.payload, context)) } catch (error) { if (!signal.aborted) host.hookDone(effect.id, 1, thrown(error)) }
      return
    }
    const fn = rt.registry.at<(...args: unknown[]) => unknown>(p.handler)
    const args = (HOOK_ARGUMENTS[p.name] ?? (() => [p.payload]))(p.payload)
    const api = invocationApi(rt.hostApi, { taskId: p.taskId, conversationId: p.conversationId })
    try {
      const value = await fn(...args, { taskId: api.taskId, conversationId: api.conversationId, memo: api.memo, snapshot: api.snapshot, snapshotAsOf: api.snapshotAsOf }, context)
      host.hookDone(effect.id, 0, value)
    } catch (error) {
      if (signal.aborted) return
      host.hookDone(effect.id, 1, thrown(error))
    }
  }

  const section: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<SectionEffect & { handler?: number }>(effect)
    const context = contextOf(signal)
    try {
      const target = p.handler !== undefined ? rt.registry.at<{ render: (...a: unknown[]) => unknown }>(p.handler) : await sectionOf(rt, p)
      const input = { ...p.input, conversationId: p.conversationId, env: envs.get(p.taskId), read: documentReader(rt.client) }
      host.hookDone(effect.id, 0, (await target.render(input, context)) ?? null)
    } catch (error) {
      if (signal.aborted) return
      host.hookDone(effect.id, 1, thrown(error))
    }
  }

  const env: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<EnvEffect>(effect)
    const context = contextOf(signal)
    try {
      const built = rt.env ? await rt.env({ conversationId: p.conversationId, ...(p.cwd === undefined ? {} : { cwd: p.cwd }), read: documentReader(rt.client) }, context) : undefined
      if (p.taskId !== undefined) { if (built === undefined) envs.delete(p.taskId); else envs.set(p.taskId, built) }
      host.hookDone(effect.id, 0, null)
    } catch (error) {
      if (signal.aborted) return
      host.hookDone(effect.id, 1, thrown(error))
    }
  }

  const deferred: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<DeferredEffect>(effect)
    try {
      const model = rt.models.getModel(p.model.provider, p.model.modelId)
      if (model === undefined) throw new Error(`Model ${p.model.provider}/${p.model.modelId} is not available`)
      if (p.op === "cancel") {
        await rt.models.cancelDeferred?.(model, p.handle, { signal })
        return host.hookDone(effect.id, 0, null)
      }
      if (!rt.models.fetchDeferred) throw new Error("the models object has no fetchDeferred")
      const message = await rt.models.fetchDeferred(model, p.handle, { signal })
      host.hookDone(effect.id, 0, message)
    } catch (error) {
      if (signal.aborted) return
      host.hookDone(effect.id, 1, thrown(error))
    }
  }

  const callback: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<CallbackEffect>(effect)
    const context = contextOf(signal)
    const channel = rt.client.bind(p.txId)
    try {
      const run = rt.callbacks?.[p.name]
      if (!run) throw new Error(`no host callback named ${p.name}`)
      host.hookDone(effect.id, 0, (await run(p.payload, channel, context)) ?? null)
    } catch (error) {
      if (signal.aborted) return
      host.hookDone(effect.id, 1, thrown(error))
    }
  }

  const phase: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<PhaseEffect>(effect)
    if (!rt.phase) throw new Error("the host has no custom task runtime")
    try {
      await rt.phase(p, contextOf(signal))
      host.phaseDone(effect.id, 0, null)
    } catch (error) {
      if (signal.aborted) return
      host.phaseDone(effect.id, 1, thrown(error))
    }
  }

  return {
    [EFFECT.model_context]: modelContext,
    [EFFECT.model_http]: modelHttp,
    [EFFECT.tool]: tool,
    [EFFECT.hook]: hook,
    [EFFECT.env]: env,
    [EFFECT.section]: section,
    [EFFECT.deferred]: deferred,
    [EFFECT.callback]: callback,
    [EFFECT.phase]: phase,
  }
}

/** Message lists the host keeps for the delta form of model_context (ABI section 6, effect 3). */
const MAX_KEPT_LISTS = 1024

/** Reject when the context is cancelled: a details wait that gives up leaves the update in place (tool.ts details). */
const abortion = (context: Context): Promise<never> => new Promise((_, reject) => {
  const signal = context.abortSignal
  if (!signal) return
  if (signal.aborted) return reject(signal.reason)
  signal.addEventListener("abort", () => reject(signal.reason), { once: true })
})

const emptyUsage = () => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } })

/** A body recipe (ABI section 10.2) with literal segments only: the core-held lanes never reference host records. */
function assembleRecipe(recipe: Uint8Array): Uint8Array {
  const view = new DataView(recipe.buffer, recipe.byteOffset, recipe.byteLength)
  const n = view.getUint32(0, true)
  const parts: Uint8Array[] = []
  let at = 4
  for (let i = 0; i < n; i++) {
    const tag = view.getUint8(at)
    if (tag !== 0) throw new Error(`body recipe segment ${i} references a host-held record, which this host does not keep`)
    const length = view.getUint32(at + 1, true)
    parts.push(recipe.subarray(at + 5, at + 5 + length))
    at += 5 + length
  }
  const out = new Uint8Array(parts.reduce((s, p) => s + p.length, 0))
  let off = 0
  for (const part of parts) { out.set(part, off); off += part.length }
  return out
}

/** Fallback when a section effect carries no handler index: the agent the core resolved names its sections. */
async function sectionOf(rt: EffectRuntime, p: SectionEffect): Promise<{ render: (...a: unknown[]) => unknown }> {
  const agent = await rt.client.api<{ sections?: { handler: number }[] }>("agent", { conversationId: p.conversationId })
  const entry = agent.sections?.[p.section]
  if (!entry) throw new Error(`section ${p.section} is not in the agent of conversation ${p.conversationId}`)
  return rt.registry.at(entry.handler)
}

export { runCommit }
export type { HostSession, EffectContext }
