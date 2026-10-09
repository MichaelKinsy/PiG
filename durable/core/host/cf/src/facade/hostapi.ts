// What extension code reaches through a TaskRuntime, a ToolExecutionApi or a HookApi (ABI section 8's method table): each
// method is one `api` request or one transaction on the core. The same object serves tools, hooks, sections and custom task
// phases; only the invocation identity differs.
import { awaitWithContext } from "@earendil-works/chord/context"
import type { CoreClient } from "../client.ts"
import { runCommit, type TxProxy } from "./tx.ts"
import { resolveAddress, wireAddress, type AnyDocToken } from "./documents.ts"
import type { Context } from "@earendil-works/chord"

export type InvocationIdentity = { taskId: number; conversationId: number }

export interface HostApiOptions {
  client: CoreClient
  /** Builds the invocation-bound handle of a conversation (Harness layer). */
  conversation?: (id: number, context: Context) => Promise<unknown>
  /** pi-durable `Context` values are chord contexts; every wait honours their cancellation. */
}

const read = <T>(client: CoreClient, context: Context, op: string, args: Record<string, unknown>): Promise<T> =>
  awaitWithContext(client.api<T>(op, args), context)

/** Committed document reads (`DocumentReader`): `snapshot(token, ...owner, [key], context)` and `snapshotAsOf`. */
export function documentReader(client: CoreClient) {
  return {
    snapshot: async (token: AnyDocToken, ...args: unknown[]) => {
      const { address, nextArgument } = resolveAddress(token.definition, args)
      const context = args[nextArgument] as Context
      return (await read<unknown>(client, context, "snapshot", { doc: wireAddress(address) })) ?? undefined
    },
    snapshotAsOf: async (token: AnyDocToken, ...args: unknown[]) => {
      const { address, nextArgument } = resolveAddress(token.definition, args)
      const at = args[nextArgument] as number
      const context = args[nextArgument + 1] as Context
      return (await read<unknown>(client, context, "snapshotAsOf", { doc: wireAddress(address), at })) ?? undefined
    },
  }
}

/** The methods every invocation shares. */
export function invocationApi(options: HostApiOptions, who: InvocationIdentity & { invocation?: number }) {
  const { client } = options
  return {
    ...documentReader(client),
    taskId: who.taskId,
    conversationId: who.conversationId,
    memo: async (name: string, ...rest: unknown[]) => {
      // memo(name, context) reads; memo(name, candidate, context) writes the candidate once, first writer wins.
      if (rest.length <= 1) return (await read<unknown>(client, rest[0] as Context, "memo.get", { taskId: who.taskId, name })) ?? undefined
      return read<unknown>(client, rest[1] as Context, "memo.put", { taskId: who.taskId, name, candidate: rest[0] })
    },
    getTask: async (id: number, context: Context) => (await read<unknown>(client, context, "getTask", { taskId: id })) ?? undefined,
    waitForTask: (id: number, context: Context) => read<unknown>(client, context, "waitForTask", { taskId: id }),
    outcomes: (ids: readonly number[], context: Context) => read<unknown[]>(client, context, "outcomes", { taskIds: ids }),
    entry: async (...args: unknown[]) => {
      // entry(id, context) or entry(token, id, context).
      const typed = typeof args[0] !== "number"
      const id = (typed ? args[1] : args[0]) as number
      const context = (typed ? args[2] : args[1]) as Context
      return (await read<unknown>(client, context, "entry", { conversationId: who.conversationId, entryId: id })) ?? undefined
    },
    context: (conversationId: number, context: Context, at?: number) => read<unknown>(client, context, "context", { conversationId, ...(at === undefined ? {} : { at }) }),
    agent: (context: Context) => read<unknown>(client, context, "agent", { conversationId: who.conversationId }),
    sleep: (until: number, context: Context) => read<null>(client, context, "sleep", { taskId: who.taskId, until }).then(() => undefined),
    conversation: async (id: number, context: Context) => options.conversation ? options.conversation(id, context) : undefined,
  }
}

/** `commit(change, context)` bound to an invocation: the transaction carries the invocation so the core can reject it once the invocation ends. */
export function invocationCommit(options: HostApiOptions, who: { invocation?: number; conversationId?: number }) {
  return <T>(change: (tx: TxProxy, current?: unknown) => T | Promise<T>, context: Context): Promise<T> =>
    awaitWithContext(runCommit(
      () => options.client.begin({ ...(who.invocation === undefined ? {} : { invocation: who.invocation }), ...(who.conversationId === undefined ? {} : { conversationId: who.conversationId }) }),
      change as (tx: TxProxy) => T | Promise<T>,
      result => result,
    ), context)
}
