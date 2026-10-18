// What extension code reaches through a TaskRuntime, a ToolExecutionApi or a HookApi (ABI section 8's method table): each
// method is one `api` request or one transaction on the core. The same object serves tools, hooks, sections and custom task
// phases; only the invocation identity differs.
import { handRecord } from "./table-identity.ts"
import { forkCopyHook, withColdLoad } from "./coldload.ts"
import { BACKGROUND_CONTEXT, awaitWithContext, withAbortSignal } from "@earendil-works/chord/context"
import type { CoreClient } from "../client.ts"
import { migrateRetry, runCommit, wireDefinition, type TxProxy } from "./tx.ts"
import { enterCommit } from "./commit-context.ts"
import { loadedTracker, keepTracker, settleRevision, shareSnapshot, snapshotStamp, stageRevision, unstageRevision } from "./snapshots.ts"
import { resolveAddress, wireAddress, type AnyDocToken } from "./documents.ts"
import type { Context } from "@earendil-works/chord"

/** What ties a handle to an invocation: its signal, and a check that throws once the invocation has ended (harness.ts InvocationBinding). */
export type InvocationBinding = { readonly signal: AbortSignal | undefined; /** The task whose invocation this binding belongs to; absent for a handle outside any invocation. */ readonly invocation?: number; check(): void; /** Run `callback` when the invocation ends (at once if it already has). */ onEnd(callback: () => void): void }

/** A binding that never ends: the handle of a caller outside any invocation. */
export const UNBOUND: InvocationBinding = { signal: undefined, check() {}, onEnd() {} }

/** Tracks the end of one invocation: `end()` runs the stop callbacks of its watches and states. */
export function invocationBinding(taskId: number, signal: AbortSignal | undefined) {
  const callbacks: (() => void)[] = []
  let ended = false
  const binding: InvocationBinding = {
    signal, invocation: taskId,
    check() { if (ended) throw new Error(`Task ${taskId} invocation has ended`) },
    onEnd(callback) { if (ended) callback(); else callbacks.push(callback) },
  }
  return { binding, end() { ended = true; for (const callback of callbacks.splice(0)) callback() } }
}
const invocationOfContext = new WeakMap<object, number>()
/** A context that runs under an invocation's signal and remembers the invocation, so the operations that reach the core carry it: the core rejects them at line-job start once the invocation's abort mark is published (session.ts:411 throwIfAborted, harness.ts:381/:391). */
export function bindContext(binding: InvocationBinding, context: Context): Context {
  if (binding.signal === undefined) return context
  const bound = withAbortSignal(binding.signal, context)
  if (binding.invocation !== undefined) invocationOfContext.set(bound, binding.invocation)
  return bound
}
/** The invocation a bound context carries, if any. */
export const invocationOf = (context: Context): number | undefined => invocationOfContext.get(context)

export type InvocationIdentity = { taskId: number; conversationId: number }

export interface HostApiOptions {
  client: CoreClient
  /** Builds the invocation-bound handle of a conversation (Harness layer). */
  conversation?: (id: number, binding: InvocationBinding, context: Context) => Promise<unknown>
  /** The Harness's agent resolution for a conversation. */
  agent?: (conversationId: number, context: Context) => Promise<unknown>
  /** The Harness's committed-document watches and states, which an invocation's handle stops when the invocation ends. */
  watchDoc?: (token: AnyDocToken, ...args: unknown[]) => Promise<{ stop(): unknown } | undefined>
  documentState?: (token: AnyDocToken, ...args: unknown[]) => Promise<{ dispose?(): unknown } | undefined>
  /** session.ts assertUsable for a Session that failed: throws SessionFailed, so a commit whose callback outlived the failure does not commit. */
  failed?: () => void
  /** pi-durable `Context` values are chord contexts; every wait honours their cancellation. */
}

/** Pi's runtime reads that take the caller's context but never race it (scheduler.ts :1159-1250: `#read` runs a readOnLine whose Storage call ignores the signal); a facade wrapper that raced it would spend a pass Pi does not. */
const UNCANCELLED_READS = new Set(["context", "getTask", "entry"])

const read = <T>(client: CoreClient, context: Context, op: string, args: Record<string, unknown>, revive?: (value: T) => T): Promise<T> =>
  awaitWithContext(client.api<T>(op, args, revive), context)

/** Committed document reads (`DocumentReader`): `snapshot(token, ...owner, [key], context)` and `snapshotAsOf`. */
export function documentReader(client: CoreClient) {
  return {
    snapshot: async (token: AnyDocToken, ...args: unknown[]) => {
      const { address, nextArgument } = resolveAddress(token.definition, args)
      const context = args[nextArgument] as Context
      const def = wireDefinition(token.definition)
      const value = (await withColdLoad(client, wireAddress(address), context, () => awaitWithContext(client.api<unknown>("snapshot", { doc: wireAddress(address), def }, undefined, migrateRetry(def)), context))) ?? undefined
      return value === undefined ? undefined : shareSnapshot(client, wireAddress(address), def, value)
    },
    snapshotAsOf: async (token: AnyDocToken, ...args: unknown[]) => {
      const { address, nextArgument } = resolveAddress(token.definition, args)
      const at = args[nextArgument] as number
      const context = args[nextArgument + 1] as Context
      const def = wireDefinition(token.definition)
      return (await awaitWithContext(client.api<unknown>("snapshotAsOf", { doc: wireAddress(address), def, at }, undefined, migrateRetry(def)), context)) ?? undefined
    },
  }
}

type ContextViewJSON = { head?: unknown; entries: unknown[]; contributions: unknown[][]; messages: unknown[] }

const deepFreeze = (value: unknown): void => {
  if (value === null || typeof value !== "object" || Object.isFrozen(value)) return
  Object.freeze(value)
  for (const child of Object.values(value)) deepFreeze(child)
}

/** The records and messages of a context view are trusted immutable values, as the Harness's kept ranges are (context.ts); the arrays stay the caller's. */
function freezeView(view: ContextViewJSON): ContextViewJSON {
  deepFreeze(view.head)
  for (const entry of view.entries) deepFreeze(entry)
  for (const contributed of view.contributions) for (const message of contributed) deepFreeze(message)
  for (const message of view.messages) deepFreeze(message)
  return view
}

/** The methods every invocation shares. */
export function invocationApi(options: HostApiOptions, who: InvocationIdentity & { invocation?: number }, binding: InvocationBinding = UNBOUND, phase?: { resetAgent?: () => void }) {
  const { client } = options
  let resolved: Promise<unknown> | undefined
  // A custom task's phase resolves its agent anew (scheduler.ts agentReset at each phase).
  if (phase) phase.resetAgent = () => { resolved = undefined }
  const reader = documentReader(client)
  // A committed-state read of an invocation rejects once the invocation has ended (scheduler.ts: the read runs unless the
  // invocation has ended); a wait the core cancels because the invocation ended reads as that error, not as an abort.
  const run = async <T>(context: Context, op: string, args: Record<string, unknown>, revive?: (value: T) => T): Promise<T> => {
    binding.check()
    try { return await (UNCANCELLED_READS.has(op) ? client.api<T>(op, args, revive) : read<T>(client, context, op, args, revive)) } catch (error) { binding.check(); throw error }
  }
  return {
    // Committed-document reads of an invocation end with it, like every read of it.
    snapshot: async (...args: Parameters<ReturnType<typeof documentReader>["snapshot"]>) => { binding.check(); try { return await reader.snapshot(...args) } catch (error) { binding.check(); throw error } },
    snapshotAsOf: async (...args: Parameters<ReturnType<typeof documentReader>["snapshotAsOf"]>) => { binding.check(); try { return await reader.snapshotAsOf(...args) } catch (error) { binding.check(); throw error } },
    taskId: who.taskId,
    conversationId: who.conversationId,
    memo: async (name: string, ...rest: unknown[]) => {
      // memo(name, context) reads; memo(name, candidate, context) writes the candidate once, first writer wins.
      if (rest.length <= 1) return (await run<unknown>(rest[0] as Context, "memo.get", { taskId: who.taskId, name })) ?? undefined
      return run<unknown>(rest[1] as Context, "memo.put", { taskId: who.taskId, name, candidate: rest[0] })
    },
    // Pi's `#read` returns the readOnLine promise from an async function (scheduler.ts :1183-1186, :1269-1272): one pass over the read's own caller.
    getTask: (id: number, context: Context) => run<unknown>(context, "getTask", { taskId: id }, value => value ?? undefined),
    waitForTask: (id: number, context: Context) => run<unknown>(context, "waitForTask", { taskId: id, by: who.taskId }),
    outcomes: (ids: readonly number[], context: Context) => run<unknown[]>(context, "outcomes", { taskIds: ids, by: who.taskId }),
    /** The live tasks this task owns directly, in ID order, also those an earlier invocation created (scheduler.ts ownedTasks); the core serves them from its ownership index. */
    ownedTasks: (context: Context) => run<unknown[]>(context, "ownedTasks", { by: who.taskId }),
    // Pi's entry is `#read(invocation, async () => { await readOnLine(...); return ... })` (scheduler.ts :1216-1228): the async arrow's own pass is the core's (ReadOnLine of an entry read), the facade adds none.
    // Pi's read callback is an async arrow that awaits readOnLine and returns the entry (scheduler.ts :1216-1228): one pass of its own over getTask's.
    entry: async (...args: unknown[]) => {
      // entry(id, context) or entry(token, id, context).
      const typed = typeof args[0] !== "number"
      const id = (typed ? args[1] : args[0]) as number
      const context = (typed ? args[2] : args[1]) as Context
      const found = await run<unknown>(context, "entry", { conversationId: who.conversationId, entryId: id }, value => value ?? undefined)
      return found
    },
    /** The model context of any conversation; `{at}` reads it as of that entry. */
    context: async (conversationId: number, context: Context, options?: { at?: number }) =>
      freezeView(await run<ContextViewJSON>(context, "context", { taskId: who.taskId, conversationId, ...(options?.at === undefined ? {} : { at: options.at }) })),
    /** The conversation's agent, resolved at first use from the registry and settings at that moment and kept for this handle (a phase resolves its agent once). */
    agent: async (context: Context) => {
      binding.check()
      // Resolved with the invocation's context, not the first caller's: a caller that stops waiting must not fail the phase's resolution.
      resolved ??= options.agent ? options.agent(who.conversationId, binding.signal === undefined ? BACKGROUND_CONTEXT : withAbortSignal(binding.signal, BACKGROUND_CONTEXT)) : Promise.reject(new Error("this handle cannot resolve an agent"))
      resolved.catch(() => {})
      return await awaitWithContext(resolved, context)
    },
    /** Commit a sleep: the core stores it and answers once it is committed; the task then holds no invocation. */
    sleepUntil: (until: number, checkpoint: unknown, context: Context) => run<null>(context, "sleepUntil", { taskId: who.taskId, until, checkpoint }).then(() => undefined),
    conversation: async (id: number, context: Context) => options.conversation ? options.conversation(id, binding, context) : undefined,
    /** The serialized watch of a committed document; it stops when the invocation ends. */
    watchDoc: async (token: AnyDocToken, ...args: unknown[]) => {
      binding.check()
      const watch = await options.watchDoc?.(token, ...args)
      if (watch !== undefined) { binding.onEnd(() => { void watch.stop() }); binding.check() }
      return watch
    },
    documentState: async (token: AnyDocToken, ...args: unknown[]) => {
      binding.check()
      const state = await options.documentState?.(token, ...args)
      if (state !== undefined) binding.onEnd(() => { void state.dispose?.() })
      return state
    },
  }
}

/** `commit(change, context)` bound to an invocation: the transaction carries the invocation so the core can reject it once the invocation ends. */
export function invocationCommit(options: HostApiOptions, who: { invocation?: number; conversationId?: number }, binding: InvocationBinding = UNBOUND) {
  const begin = () => options.client.begin({ ...(who.invocation === undefined ? {} : { invocation: who.invocation }), ...(who.conversationId === undefined ? {} : { conversationId: who.conversationId }) })
  // Not async, and no cancellable wait: Pi's commitWith checks the signal when the job starts and then ignores it (session.ts:411), so the caller's
  // promise is the commit's own and settles the hops after the core's ack that Pi's `run` does. A commit of an invocation that has ended rejects as
  // 'invocation has ended', also when the core refused it for that reason (`onError`).
  return <T>(change: (tx: TxProxy, current?: unknown) => T | Promise<T>, context: Context): Promise<T> => {
    try { binding.check(); context.abortSignal?.throwIfAborted() } catch (error) { return Promise.reject(error) }
    let leave: (() => void) | undefined
    // The commit holds the line from `begin` until its answer settles: publications that arrive meanwhile are its own (commit-context.ts).
    const held = () => { const ready = begin(); ready.then(() => { leave = enterCommit(options.client, context) }, () => {}); return ready }
    const done = runCommit(held, change,
      // Only a task invocation's commit returns the next task state; a Session commit hands its result (a draft that the commit revokes) back unread.
      who.invocation === undefined ? undefined : result => result, () => binding.check(),
      (_address, _definition, revision) => { const since = snapshotStamp(options.client); stageRevision(options.client, revision); return committed => { unstageRevision(options.client, revision); settleRevision(options.client, revision, committed, since) } },
      { load: (id, value) => loadedTracker(options.client, id, value), keep: (id, tracker) => keepTracker(options.client, id, tracker), ...(forkCopyHook(options.client) && { forkCopy: forkCopyHook(options.client)! }), record: record => handRecord(options.client, record) },
      options.failed)
    // A side branch: the caller's promise is `done` itself.
    done.then(() => leave?.(), () => leave?.())
    return done
  }
}
