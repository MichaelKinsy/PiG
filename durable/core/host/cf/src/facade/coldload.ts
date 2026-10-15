import type { Context } from "@earendil-works/chord"
import { BACKGROUND_CONTEXT, awaitWithContext, withoutAbortSignal } from "@earendil-works/chord/context"
import type { CoreClient } from "../client.ts"

type WireAddress = { scope: string; definition: string; conversationId?: number; taskId?: number; key?: string }
type DocumentStorage = {
  findDocument(address: unknown, at: "current" | number, context: Context): Promise<{ id: number } | undefined>
  document(id: number, at: "current" | number, context: Context): Promise<unknown>
}

interface Loads { storage: DocumentStorage; reads: boolean; loaded: Set<string>; loading: Map<string, Promise<void>> }
const loads = new WeakMap<CoreClient, Loads>()

/**
 * Pi's tracker loads a document it does not hold through the Storage on the line: `findDocument(address, "current")`, then
 * `document(id, "current")` (session.ts #loadDocument). A Harness has the core's own line, which asks the Storage for the reads
 * of its jobs (storageRead gate); a Session has no line in the core, so a Storage that overrides either method (a subclass that
 * counts, holds or fails reads) is asked here, in Pi's order, beside the core's read: the first access of an address, and again
 * after `unloadDocuments`.
 */
export function trackColdLoads(client: CoreClient, storage: object, gate: readonly string[]): void {
  if (!gate.includes("findDocument") && !gate.includes("document")) return
  loads.set(client, { storage: storage as DocumentStorage, reads: gate.includes("findDocument") || gate.includes("document"), loaded: new Set(), loading: new Map() })
}

/** `unloadDocuments`: the next access of every address loads it again. */
export function forgetColdLoads(client: CoreClient): void { loads.get(client)?.loaded.clear() }

/** Start the Storage's side of a cold load of `address`; undefined for a Storage that overrides neither read or an address already loaded. */
function coldLoad(client: CoreClient, address: WireAddress, context: Context): Promise<void> | undefined {
  const state = loads.get(client)
  if (state === undefined || !state.reads) return undefined
  const key = JSON.stringify(address)
  if (state.loaded.has(key)) return undefined
  let load = state.loading.get(key)
  if (load === undefined) {
    const documentAddress = {
      kind: address.definition,
      scope: { kind: address.scope, ...(address.conversationId === undefined ? {} : { conversationId: address.conversationId }), ...(address.taskId === undefined ? {} : { taskId: address.taskId }) },
      ...(address.key === undefined ? {} : { key: address.key }),
    }
    // The load belongs to the line, not to the caller that started it: a caller that gives up stops waiting while the load finishes.
    const background = withoutAbortSignal(context)
    load = (async () => {
      const record = await state.storage.findDocument(documentAddress, "current", background)
      if (record !== undefined) await state.storage.document(record.id, "current", background)
      state.loaded.add(key)
    })().finally(() => state.loading.delete(key))
    state.loading.set(key, load)
  }
  return awaitWithContext(load, context)
}

/**
 * Run the core's read of `address` and, beside it, the Storage's side of its cold load: the read is queued on the line at once (a read
 * queued before a close seals still runs), and the caller gets the answer when the Storage's reads have finished too.
 */
export function withColdLoad<T>(client: CoreClient, address: unknown, context: Context, run: () => Promise<T>): Promise<T> {
  const addresses = (Array.isArray(address) ? address : [address]) as WireAddress[]
  // Several documents (a conversation view's) load one after the other, as the view's observers start.
  const load = addresses.reduce<Promise<void> | undefined>((chain, one) => chain === undefined ? coldLoad(client, one, context) : chain.then(() => coldLoad(client, one, context)), undefined)
  if (load === undefined) return run()
  const result = run()
  return Promise.all([load, result]).then(([, value]) => value)
}

/**
 * A document acquired in a conversation this transaction forked is a copy of the parent's document at the fork point; Pi reads that
 * source through `Storage.document` when the copy is acquired (transaction.ts #acquireForkCopy).
 */
export function forkCopyHook(client: CoreClient): ((address: unknown, parentConversationId: number, at: number) => Promise<void>) | undefined {
  const state = loads.get(client)
  if (state === undefined || !state.reads) return undefined
  return async (address, parentConversationId, at) => {
    const wire = address as WireAddress
    const source = { kind: wire.definition, scope: { kind: "conversation", conversationId: parentConversationId }, ...(wire.key === undefined ? {} : { key: wire.key }) }
    const record = await state.storage.findDocument(source, at, BACKGROUND_CONTEXT as Context)
    if (record !== undefined) await state.storage.document(record.id, at, BACKGROUND_CONTEXT as Context)
  }
}
