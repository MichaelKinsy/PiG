// Effect executors (ABI section 6): what the host does when a step asks for model calls, tools, hooks, prompt sections,
// environments, deferred polls, callbacks and custom task phases. Host code runs here; every outcome returns to the core as
// an event. A user-code throw is an outcome the core classifies; a host or protocol failure throws out of the handler and the
// HostSession discards the handle (ADR D16).
import { errorMessage } from "./errors.ts"
import { expandRecipe } from "./recipe.ts"
import { BACKGROUND_CONTEXT, withAbortSignal } from "@earendil-works/chord/context"
import { copyJson, type Context, type JsonValue } from "@earendil-works/chord"
import { EFFECT } from "./abi.ts"
import type { CoreClient, TxChannel } from "./client.ts"
import type { EffectContext, EffectHandler, HostSession } from "./host.ts"
import {
  HOOK_ARGUMENTS, wireError,
  type CallbackEffect, type DeferredEffect, type EnvEffect, type HookEffect, type ModelContextEffect, type PhaseEffect, type SectionEffect, type ToolEffect,
} from "./protocol.ts"
import { KeptLists } from "./kept.ts"
import { nestedExecutor } from "./nested.ts"
import { DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES, OutputBuffer, type OutputLimits } from "./output.ts"
import type { HostRegistry } from "./registry.ts"
import type { Effect } from "./wire.ts"
import { documentReader, invocationApi, invocationBinding, invocationCommit, type HostApiOptions } from "./facade/hostapi.ts"
import { runCommit, type TxProxy } from "./facade/tx.ts"

const dec = new TextDecoder()
const enc = new TextEncoder()
const parse = <T>(effect: Effect): T => JSON.parse(dec.decode(effect.payload)) as T

/** The subset of pi-ai `Models` the host calls (generation.ts:229,259,402). */
export interface ModelsLike {
  /** The catalogue; the open payload names the entries the core may use (`models`: provider, id, contextWindow, maxTokens). */
  getModels?(provider?: string): readonly { provider: string; id: string; contextWindow: number; maxTokens: number }[]
  getModel(provider: string, modelId: string): unknown | undefined
  /** The catalogue: the models the core may name (open's `models`). */
  getModels?(provider?: string): readonly { provider: string; id: string; contextWindow: number; maxTokens: number }[]
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
  phase?: (effect: PhaseEffect, context: Context, effectId: number, settled?: (failure?: { error: unknown }) => void) => Promise<void>
  /** Hooks the host answers itself, by `hook` effect name: `resolveAgent` (wraps are host code), `conversationCreated`. They take no registry handler. */
  hostHooks?: Record<string, (payload: any, context: Context) => unknown | Promise<unknown>>
  /** The registry snapshot object tools and sections see as `api.registry` (the Harness layer's published state). */
  snapshot?: () => unknown
  /** The Harness itself: harness.ts buildEnv passes `read: this` to the environment builder. */
  readTarget?: () => unknown
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
    // Pi consumes `for await (event of events)` in microtasks (generation.ts:415-420), so the batch is flushed in a microtask, not a timer.
    if (!this.armed) { this.armed = true; queueMicrotask(() => { this.armed = false; this.flush() }) }
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

/**
 * The request options with the signal where Pi puts it: generation.ts builds `{ ...streamOptions, signal, sessionId, ...reasoning }`, and
 * ConversationStreamOptions holds neither key, so the core's options end with sessionId (and reasoning) and the signal goes just before
 * sessionId. A signal already in the options keeps its place (spread semantics).
 */
export function withSignal(options: Record<string, unknown> | undefined, signal: AbortSignal): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (options === undefined) { out.signal = signal; return out }
  const keys = Object.keys(options)
  const has = keys.includes("signal")
  for (const key of keys) {
    if (key === "sessionId" && !has) out.signal = signal
    out[key] = key === "signal" ? signal : options[key]
  }
  if (!("signal" in out)) out.signal = signal
  return out
}

export function createEffectHandlers(rt: EffectRuntime): Partial<Record<number, EffectHandler>> {
  const kept = new KeptLists()
  const envs = new Map<number, unknown>()
  const contextOf = (signal: AbortSignal): Context => withAbortSignal(signal, BACKGROUND_CONTEXT)
  const bytes = (v: Uint8Array | string) => typeof v === "string" ? enc.encode(v) : v

  const modelContext: EffectHandler = async (effect, { host, signal }) => {
    // The payload is a recipe (ABI section 10.2): expand it, then parse the JSON it stands for.
    // A range segment stands for the messages of a committed context: the host derives them from storage and they replace the
    // `null` the segment expands to.
    const ranged: unknown[][] = []
    const p = JSON.parse(dec.decode(expandRecipe(effect.payload, (conv, head, tail) => {
      ranged.push(host.contexts.messages(conv, head, tail))
    }))) as ModelContextEffect
    if (ranged.length > 0) {
      const slot = p.context?.messages
      if (ranged.length !== 1 || !Array.isArray(slot) || slot.length !== 1 || slot[0] !== null) throw new Error(`model_context ${effect.id}: a range segment must be the whole of context.messages`)
      p.context!.messages = ranged[0]!
    }
    let messages: unknown[]
    if (p.extends !== undefined) {
      const base = kept.take(p.extends)
      if (!base) throw new Error(`model_context ${effect.id} extends effect ${p.extends}, whose message list the host no longer holds`)
      for (const message of p.append ?? []) base.push(message)
      messages = base
    } else messages = p.context?.messages ?? []
    kept.keep(effect.id, messages, p.conversationId)
    const model = rt.models.getModel(p.model.provider, p.model.modelId)
    const fail = (message: string) => host.modelEvents(effect.id, [{ type: "error", reason: "error", error: { role: "assistant", content: [], api: "", provider: p.model.provider, model: p.model.modelId, usage: emptyUsage(), stopReason: "error", errorMessage: message, timestamp: host.now() } }])
    if (model === undefined) return fail(`Model ${p.model.provider}/${p.model.modelId} is not available`)
    const batch = new EventBatch(json => host.modelEventsRaw(effect.id, json))
    try {
      // Each request gets its own copy of the list, as Pi's generation does (generation.ts streamSimple call): the kept list grows
      // in place when a later request of the conversation extends it.
      const stream = rt.models.streamSimple(model, { messages: [...messages] }, withSignal(p.options, signal))
      let terminal = false
      // Pi stores the final message through a strict-JSON copy in the answer commit; one that is not JSON faults the run there.
      const checked = (event: any): unknown => {
        const message = event.type === "done" ? event.message : event.error
        try { copyJson(message as JsonValue, { omitUndefinedProperties: true }) } catch (error) { return { type: "fault", message: (error as Error)?.message ?? errorMessage(error) } }
        return event
      }
      for await (const event of stream) {
        if (event.type === "done" || event.type === "error") { terminal = true; batch.push(checked(event) as typeof event); continue }
        batch.push(event)
      }
      // generation.ts returns `events.result()` once the stream ends: a stream that ends without a terminal event still has its final message.
      if (!terminal && !signal.aborted) {
        const message = await stream.result()
        batch.push(checked(message.stopReason === "error" || message.stopReason === "aborted"
          ? { type: "error", reason: message.stopReason, error: message }
          : { type: "done", reason: message.stopReason, message }) as never)
      }
      batch.flush()
    } catch (error) {
      batch.flush()
      if (!signal.aborted) fail((error as Error)?.message ?? errorMessage(error))
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
    const invocation = invocationBinding(p.taskId, signal)
    const base = invocationApi(rt.hostApi, who, invocation.binding)
    const retained = new OutputBuffer(outputLimitsOf(registration.outputLimits, p.outputWindow))
    // Nested call admissions this invocation started: the phase waits for them before it settles, so settlement sees every nested call (tool.ts admissions).
    const admissions: Promise<unknown>[] = []
    const resumes = p.replay === "safe"
    const api = {
      ...base,
      callId: p.callId,
      models: rt.models,
      registry: rt.snapshot?.(),
      env: envs.get(p.taskId),
      outputWindow: p.outputWindow ?? undefined,
      output: (chunk: string | Uint8Array, skipped?: { bytes: number; newlines: number; endsWithNewline: boolean }) => {
        live()
        retained.push(chunk, skipped)
        const data = bytes(chunk)
        if (skipped === undefined || skipped.bytes === 0) return host.toolProgress(effect.id, 0, 0, data)
        const meta = enc.encode(JSON.stringify(skipped))
        const framed = new Uint8Array(4 + meta.length + data.length)
        new DataView(framed.buffer).setUint32(0, meta.length, true)
        framed.set(meta, 4)
        framed.set(data, 4 + meta.length)
        host.toolProgress(effect.id, 0, skipped.bytes, framed)
      },
      // tool.ts retainedOutput: what the model will see if the result omits `output`.
      retainedOutput: () => {
        live()
        const kept = retained.snapshot()
        return { text: kept.text, truncated: kept.droppedBytes > 0 }
      },
      diagnostic: (diagnostic: unknown) => { live(); host.toolProgress(effect.id, 2, 0, JSON.stringify(diagnostic)) },
      details: async (value: unknown, detailsContext: Context) => {
        live()
        detailsContext.abortSignal?.throwIfAborted()
        const { waitId, settled } = rt.client.expectAck()
        host.toolProgress(effect.id, 1, 0, JSON.stringify(value), waitId)
        await Promise.race([settled, abortion(detailsContext)])
      },
      // tool.ts commit: the call's own result is the caller's, never the task's next state.
      commit: async <T>(change: (tx: TxProxy) => T | Promise<T>, commitContext: Context): Promise<T> => {
        let result: T | undefined
        await invocationCommit(rt.hostApi, { invocation: p.taskId, conversationId: p.conversationId }, invocation.binding)(async (tx: TxProxy) => { result = await change(tx); return undefined }, commitContext)
        return result as T
      },
      // tool.ts createTask: a child the call owns is abandoned on restart unless the tool itself resumes.
      createTask: async (task: any, input: unknown, options: any, taskContext: Context) => {
        const child = options?.ownership?.kind === "task" && !resumes ? { abandonOnRestart: true, ...options } : options
        let id: unknown
        await invocationCommit(rt.hostApi, { invocation: p.taskId, conversationId: p.conversationId }, invocation.binding)(async (tx: TxProxy) => { id = await tx.createTask(task, input, child); return undefined }, taskContext)
        return id
      },
      executeTool: undefined as unknown,
    }
    api.executeTool = nestedExecutor(api as any, p.callId, resumes, live, admissions)
    try {
      const result = await registration.execute(p.arguments, api, context)
      ended = true
      // No admission starts after `ended`; let those underway commit, so settlement aborts and lists their calls (tool.ts run:
      // `await Promise.allSettled(admissions)`, one tick when none was admitted).
      await Promise.allSettled(admissions)
      invocation.end()
      // Pi records the result through a strict-JSON copy at the settle commit; a function or a cycle faults the call there.
      try { copyJson((result ?? null) as JsonValue, { omitUndefinedProperties: true }) } catch (error) {
        host.toolDone(effect.id, 2, thrown(error))
        return
      }
      host.toolDone(effect.id, 0, result ?? null)
    } catch (error) {
      ended = true
      // A throw that is not a cancellation becomes the call's error result, which settles after the admissions as a return does (tool.ts run).
      if (!signal.aborted) await Promise.allSettled(admissions)
      invocation.end()
      // A cancelled call still reports how it ended: the core joins the call before it settles the tool with its aborted result (ABI cancel row).
      host.toolDone(effect.id, 1, thrown(error))
    }
  }

  const hook: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<HookEffect>(effect)
    const context = contextOf(signal)
    const own = rt.hostHooks?.[p.name]
    if (own) {
      // A host hook that returns its answer answers in the tick it was asked: the core's caller is Pi code that runs the same
      // call synchronously (registry.snapshot() in a step's decide, scheduler.ts :955), so an `await` of a plain value would add a tick.
      try {
        const answer = own(p.payload, context)
        host.hookDone(effect.id, 0, typeof (answer as PromiseLike<unknown> | undefined)?.then === "function" ? await answer : answer)
      } catch (error) { host.hookDone(effect.id, 1, thrown(error)) }
      return
    }
    // A name the host does not know or an index that holds no function is a protocol failure, not a user-code throw: the
    // HostSession discards the handle (PROTOCOL.md, hook; ratified 2026-10-07).
    const argumentsOf = HOOK_ARGUMENTS[p.name]
    if (!argumentsOf) throw new Error(`hook effect ${effect.id} names unknown hook ${p.name}`)
    const fn = rt.registry.at<(...args: unknown[]) => unknown>(p.handler)
    if (typeof fn !== "function") throw new Error(`hook effect ${effect.id}: handler ${p.handler} of ${p.name} is not a function`)
    const args = argumentsOf(p.payload)
    const api = invocationApi(rt.hostApi, { taskId: p.taskId, conversationId: p.conversationId })
    try {
      const value = await fn(...args, { taskId: api.taskId, conversationId: api.conversationId, models: rt.models, memo: api.memo, snapshot: api.snapshot, snapshotAsOf: api.snapshotAsOf }, context)
      host.hookDone(effect.id, 0, value)
    } catch (error) {
      host.hookDone(effect.id, 1, thrown(error))
    }
  }

  const section: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<SectionEffect & { handler?: number }>(effect)
    const context = contextOf(signal)
    try {
      const target = p.handler !== undefined ? rt.registry.at<{ render: (...a: unknown[]) => unknown }>(p.handler) : await sectionOf(rt, p)
      // types.ts PromptInput: the section renders with the request's resolved agent (the codemode section lists its callable
      // tools, deferred ones left out), resolved here from the conversation's committed state as the core resolved it.
      const agent = rt.hostApi.agent === undefined ? undefined : await rt.hostApi.agent(p.conversationId, context)
      const input = { ...p.input, conversationId: p.conversationId, agent, env: envs.get(p.taskId), read: documentReader(rt.client) }
      host.hookDone(effect.id, 0, (await target.render(input, context)) ?? null)
    } catch (error) {
      host.hookDone(effect.id, 1, thrown(error))
    }
  }

  const env: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<EnvEffect>(effect)
    const context = contextOf(signal)
    try {
      const built = rt.env ? await rt.env({ conversationId: p.conversationId, ...(p.cwd === undefined ? {} : { cwd: p.cwd }), read: rt.readTarget?.() ?? documentReader(rt.client) }, context) : undefined
      if (p.taskId !== undefined) { if (built === undefined) envs.delete(p.taskId); else envs.set(p.taskId, built) }
      host.hookDone(effect.id, 0, null)
    } catch (error) {
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
      host.hookDone(effect.id, 1, thrown(error))
    }
  }

  const phase: EffectHandler = async (effect, { host, signal }) => {
    const p = parse<PhaseEffect>(effect)
    if (!rt.phase) throw new Error("the host has no custom task runtime")
    // The runtime reports through `settled` in the microtask where the handler's await resumes (Pi's `await handler()`); a runtime that does not, or a failure before the handler starts, reports here.
    let sent = false
    const settled = (failure?: { error: unknown }) => {
      sent = true
      // An aborted handler still reports: the core ends the invocation only when its handler has settled.
      if (failure) host.phaseDone(effect.id, 1, thrown(failure.error)); else host.phaseDone(effect.id, 0, null)
    }
    try {
      await rt.phase(p, contextOf(signal), effect.id, settled)
      if (!sent) settled()
    } catch (error) {
      if (!sent) settled({ error })
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

/** Reject when the context is cancelled: a details wait that gives up leaves the update in place (tool.ts details). */
const abortion = (context: Context): Promise<never> => new Promise((_, reject) => {
  const signal = context.abortSignal
  if (!signal) return
  if (signal.aborted) return reject(signal.reason)
  signal.addEventListener("abort", () => reject(signal.reason), { once: true })
})

const emptyUsage = () => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } })

/**
 * tool.ts: the limits of a call's output buffer are the tool's `outputLimits` over the defaults (head retention); a window the core
 * offered (tail retention only) carries the call's own limits and replaces them.
 */
function outputLimitsOf(declared: unknown, window: ToolEffect["outputWindow"]): OutputLimits {
  const own = (declared ?? {}) as Partial<OutputLimits>
  const limits: OutputLimits = { maxBytes: own.maxBytes ?? DEFAULT_MAX_BYTES, maxLines: own.maxLines ?? DEFAULT_MAX_LINES, retain: own.retain ?? "head" }
  if (window === undefined || window === null) return limits
  return { maxBytes: window.maxBytes ?? limits.maxBytes, maxLines: window.maxLines ?? limits.maxLines, retain: "tail" }
}

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
