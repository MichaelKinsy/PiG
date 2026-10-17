// The TaskRuntime custom task handlers receive (types.ts TaskRuntime): the invocation's identity and cancellation, the
// registry and settings, and every runtime method as one api request or transaction on the core (hostapi.ts). A custom
// task's phase or abort handler runs here, in response to the core's `phase` effect.
import { BACKGROUND_CONTEXT, withAbortSignal } from "@earendil-works/chord/context"
import type { Context } from "@earendil-works/chord"
import type { PhaseEffect } from "../protocol.ts"
import type { HostRegistry } from "../registry.ts"
import { documentReader, invocationApi, invocationBinding, invocationCommit, type HostApiOptions } from "./hostapi.ts"

export interface TaskRuntimeSources {
  registry: HostRegistry
  hostApi: HostApiOptions
  models: unknown
  settings: () => unknown
  now: () => number
  report: (error: unknown) => void
  /** The registry snapshot object `runtime.registry` shows. */
  snapshot: () => unknown
  env?: (target: { conversationId: number; read: unknown }, context: Context) => unknown | Promise<unknown>
  /** The Harness itself (harness.ts buildEnv passes `read: this`). */
  readTarget?: () => unknown
}

type Handler = (task: unknown, runtime: unknown, context: Context) => Promise<void>

/** A runtime lives from a task's first phase to the invocation's end (scheduler.ts: one Runtime for every phase of an invocation). The core ends it by cancelling the invocation's latest phase effect. */
type Live = { runtime: Record<string, unknown>; phase: { resetAgent?: () => void; snapshot?: unknown }; invocation: ReturnType<typeof invocationBinding>; abort: AbortController; effectIds: Set<number>; active: number; ending?: boolean }

export function customTaskRunner(sources: TaskRuntimeSources) {
  const live = new Map<string, Live>()
  const byEffect = new Map<number, string>()

  const build = (effect: PhaseEffect): Live => {
    const conversationId = (effect.record as { conversationId: number }).conversationId
    const who = { taskId: effect.taskId, conversationId, invocation: effect.invocation }
    const abort = new AbortController()
    const invocation = invocationBinding(effect.taskId, abort.signal)
    const binding = invocation.binding
    const phase: Live["phase"] = {}
    const api = invocationApi(sources.hostApi, who, binding, phase)
    const own = withAbortSignal(abort.signal, BACKGROUND_CONTEXT)
    const runtime = {
      ...api,
      // scheduler.ts hooks.each: the handlers of this task's hooks in the agent's selected extensions, in extension order; a throw is reported and the next runs, unless the invocation was aborted.
      hooks: {
        each: async (name: string, invoke: (handler: (...args: unknown[]) => unknown) => unknown) => {
          const agent = await api.agent(own) as { extensions: { hooks?: { task: string; handlers: Record<string, unknown> }[] }[] }
          for (const extension of agent.extensions) {
            for (const hook of extension.hooks ?? []) {
              if (hook.task !== effect.kind) continue
              const handler = hook.handlers[name]
              if (typeof handler !== "function") continue
              try { await invoke(handler.bind(hook.handlers) as (...args: unknown[]) => unknown) } catch (error) {
                if (abort.signal.aborted) throw error
                sources.report(error)
              }
            }
          }
        },
      },
      signal: abort.signal,
      get registry() { return phase.snapshot },
      get settings() { return sources.settings() },
      models: sources.models,
      // The clock and the report channel are the invocation's: they throw once it has ended (scheduler.ts runtime).
      now: () => { binding.check(); return sources.now() },
      report: (error: unknown) => { binding.check(); sources.report(error) },
      env: async (callContext: Context) => sources.env?.({ conversationId, read: sources.readTarget?.() ?? documentReader(sources.hostApi.client) }, callContext),
      commit: invocationCommit(sources.hostApi, { invocation: effect.invocation, conversationId }, binding),
    }
    return { runtime, phase, invocation, abort, effectIds: new Set(), active: 0 }
  }

  const run = async (effect: PhaseEffect, context: Context, effectId: number, settled?: (failure?: { error: unknown }) => void): Promise<void> => {
    // The definition the core reserved the task under, even if a reload has replaced or removed it since.
    const registered = effect.handler === undefined ? sources.registry.task(effect.kind) : sources.registry.at<{ definition: unknown }>(effect.handler)
    const definition = registered.definition as { phases?: Record<string, Handler>; abort?: Handler }
    const key = `${effect.taskId}:${effect.abort === true}`
    let entry = live.get(key)
    if (entry === undefined) { entry = build(effect); live.set(key, entry) }
    entry.effectIds.add(effectId)
    // Each phase sees the registry as it is at its start and resolves its agent anew.
    entry.phase.snapshot = sources.snapshot()
    entry.phase.resetAgent?.()
    byEffect.set(effectId, key)
    // The invocation's signal follows the phase whose effect the core cancels when the invocation aborts.
    const abort = entry.abort
    const signal = context.abortSignal
    if (signal?.aborted) abort.abort(signal.reason)
    else signal?.addEventListener("abort", () => abort.abort(signal.reason), { once: true })
    entry.active++
    // `settled` reports the handler's outcome in the microtask where its await resumes, as scheduler.ts's `await handler()` does: no hop between the handler settling and the report (ABI section 5.3, rule 3).
    let failure: { error: unknown } | undefined
    try {
      if (effect.abort) {
        if (!definition.abort) throw new Error(`Task ${effect.kind} has no abort handler`)
        await definition.abort(effect.record, entry.runtime, context)
      } else {
        const handler = effect.phase === undefined ? undefined : definition.phases?.[effect.phase]
        if (!handler) throw new Error(`Task ${effect.kind} has no phase ${effect.phase}`)
        await handler(effect.record, entry.runtime, context)
      }
    } catch (error) {
      if (settled === undefined) throw error
      failure = { error }
    } finally {
      // An invocation the core ended while its handler ran ends when the handler settles, so a wait the abort cancels rejects with the abort.
      if (--entry.active === 0 && entry.ending) finish(key, entry)
    }
    settled?.(failure)
  }

  const finish = (key: string, entry: Live): void => {
    if (live.get(key) === entry) live.delete(key)
    for (const id of entry.effectIds) byEffect.delete(id)
    entry.abort.abort()
    entry.invocation.end()
  }

  /** The core cancelled effect `effectId`: when it is an invocation's latest phase effect, the invocation has ended. */
  run.ended = (effectId: number): void => {
    const key = byEffect.get(effectId)
    const entry = key === undefined ? undefined : live.get(key)
    if (key === undefined || entry === undefined) return
    entry.abort.abort()
    if (entry.active > 0) entry.ending = true
    else finish(key, entry)
  }
  return run
}
