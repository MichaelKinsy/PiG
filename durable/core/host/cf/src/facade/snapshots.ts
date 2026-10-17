import type { Prepared, Tracker } from "@earendil-works/chord/delta"
import type { CoreClient } from "../client.ts"
import { addressId } from "./documents.ts"

/**
 * Immutable revisions share structure (session.ts snapshots, "returns shared immutable snapshots"): a read whose document is
 * unchanged returns the very object the previous read returned, and a changed revision reuses every unchanged subtree of it.
 * The core answers with a fresh decoded value each time, so the host keeps the latest value per document address and swaps in
 * the previous subtrees that compare equal.
 */
export function shareStructure(previous: unknown, next: unknown): unknown {
  if (previous === next) return previous
  if (typeof previous !== "object" || previous === null || typeof next !== "object" || next === null || Array.isArray(previous) !== Array.isArray(next)) return next
  const before = previous as Record<string, unknown>
  const after = next as Record<string, unknown>
  const keys = Object.keys(after)
  let same = Object.keys(before).length === keys.length
  for (const key of keys) {
    const kept = Object.hasOwn(before, key) ? shareStructure(before[key], after[key]) : after[key]
    if (kept !== before[key] || !Object.hasOwn(before, key)) same = false
    after[key] = kept
  }
  return same ? previous : next
}

const SNAPSHOT_CACHE_LIMIT = 256
const latestSnapshots = new WeakMap<object, Map<string, unknown>>()
let clock = 0

const stampsByCache = new WeakMap<Map<string, unknown>, Map<string, number>>()
const stampsOf = (cache: Map<string, unknown>) => stampsByCache.get(cache) ?? stampsByCache.set(cache, new Map()).get(cache)!

const cacheOf = (client: CoreClient) => latestSnapshots.get(client) ?? latestSnapshots.set(client, new Map()).get(client)!

const remember = (cache: Map<string, unknown>, key: string, value: unknown) => {
  stampsOf(cache).set(key, ++clock)
  cache.delete(key)
  cache.set(key, value)
  if (cache.size > SNAPSHOT_CACHE_LIMIT) cache.delete(cache.keys().next().value as string)
}

/** The value a read of this document returns: the latest one when it is structurally equal (or shares its unchanged subtrees), and it becomes the latest. */
export function shareSnapshot(client: CoreClient, address: unknown, def: { kind?: unknown } | undefined, value: unknown): unknown {
  const cache = cacheOf(client)
  const key = JSON.stringify([address, def?.kind])
  const kept = cache.has(key) ? shareStructure(cache.get(key), value) : value
  remember(cache, key, kept)
  return kept
}

/** A watch's newer revision is the latest value of its document: the next read returns it. */
export function rememberSnapshot(client: CoreClient, address: unknown, def: { kind?: unknown } | undefined, value: unknown): void {
  const key = JSON.stringify([address, def?.kind])
  const cache = cacheOf(client)
  // The latest value when the frame's is the same content (a commit this host wrote already made its revision the latest, and a migration's base leaves it): the document keeps its revision object.
  if (cache.has(key) && sameContent(cache.get(key), value)) return
  remember(cache, key, value)
}


/** Pi's unloadDocuments: the next read loads the document again. */
export function forgetSnapshots(client: CoreClient): void { latestSnapshots.delete(client); trackers.delete(client) }

/** The point in the sequence of remembered values; a commit takes it when it writes a document. */
export function snapshotStamp(client: CoreClient): number { void client; return clock }

/**
 * A document revision a transaction's draft prepared (Chord `prepare()`): the immutable value and the operations the commit
 * carries. Session adopts it by pointer swap, so the Storage write, the commit publication and every later snapshot of the
 * document are these very objects (session.ts, "adopts by pointer swap and shares operation payloads"). The core builds its own
 * decoded copies of the same content; the host puts these in their place.
 */
export type Revision = {
  address: unknown
  addressId: string
  definition: { kind?: unknown }
  /** The stored record's ID for a loaded document; absent for one the transaction creates. */
  id?: number
  value: unknown
  ops: readonly unknown[]
  /** The tracker adopts the revision when the commit succeeds (a loaded document with operations, or a created one). */
  adopt: boolean
  tracker: Tracker<any>
  prepared: Prepared<any>
}

const staged = new WeakMap<object, Revision[]>()

/** The revisions of the commit in flight, until its answer settles. */
export function stageRevision(client: CoreClient, revision: Revision): void {
  const list = staged.get(client) ?? staged.set(client, []).get(client)!
  list.push(revision)
}
export function unstageRevision(client: CoreClient, revision: Revision): void {
  const list = staged.get(client)
  const at = list?.indexOf(revision) ?? -1
  if (at >= 0) list!.splice(at, 1)
}

/** The staged revision a Storage write or publication change describes: a loaded document by its record ID, a created one by its address. */
export function revisionOf(client: CoreClient, record: { id?: unknown; kind?: unknown; scope?: { kind?: string; conversationId?: number; taskId?: number }; key?: string }): Revision | undefined {
  const list = staged.get(client)
  if (list === undefined || list.length === 0) return undefined
  const created = typeof record.kind === "string" && record.scope !== undefined ? addressId(record as never) : undefined
  for (let i = list.length - 1; i >= 0; i--) {
    const rev = list[i]!
    if (rev.id !== undefined ? rev.id === record.id : rev.addressId === created) return rev
  }
  return undefined
}

/** The same JSON content: the guard that keeps a substituted value honest when the core's own edits reached the document too. */
export const sameContent = (a: unknown, b: unknown): boolean => a === b || JSON.stringify(a) === JSON.stringify(b)

/**
 * The loaded Chord tracker of each document (session.ts keeps one per loaded document): a transaction continues from it while the
 * core's value for the document is the same content, so revisions share their unchanged subtrees and the tracker adopts what the
 * commit prepared.
 */
const trackers = new WeakMap<object, Map<string, Tracker<any>>>()
const trackersOf = (client: CoreClient) => trackers.get(client) ?? trackers.set(client, new Map()).get(client)!

export function loadedTracker(client: CoreClient, id: string, value: unknown): Tracker<any> | undefined {
  const tracker = trackersOf(client).get(id)
  return tracker !== undefined && sameContent(tracker.value, value) ? tracker : undefined
}

export function keepTracker(client: CoreClient, id: string, tracker: Tracker<any>): void {
  const map = trackersOf(client)
  map.delete(id)
  map.set(id, tracker)
  if (map.size > SNAPSHOT_CACHE_LIMIT) map.delete(map.keys().next().value as string)
}

/** The commit settled: a successful one adopts the revision by pointer swap (the next read returns it); a failed one aborts it. */
export function settleRevision(client: CoreClient, revision: Revision, committed: boolean, since: number): void {
  if (!committed || !revision.adopt) { revision.prepared.abort(); return }
  try { revision.tracker.adopt(revision.prepared) } catch { revision.prepared.abort(); trackersOf(client).delete(revision.addressId); return }
  keepTracker(client, revision.addressId, revision.tracker)
  const key = JSON.stringify([revision.address, revision.definition?.kind])
  // A newer value than the one the commit started from (a watch's frame of this very commit) is already the revision: keep it.
  if ((stampsByCache.get(cacheOf(client))?.get(key) ?? -1) > since) return
  remember(cacheOf(client), key, revision.value)
}

type DocumentWrite = { type: string; id?: number; record?: { kind?: unknown; scope?: { kind?: string; conversationId?: number; taskId?: number }; key?: string }; content?: { kind?: string; value?: unknown; ops?: unknown } }

/** A Storage write of a staged revision carries the revision's own value or operations (the Storage sees what Session handed it). */
export function substituteWrite(client: CoreClient, write: DocumentWrite): void {
  if (write.type !== "document.change" && write.type !== "document.create") return
  const content = write.content
  if (content === undefined) return
  const revision = revisionOf(client, write.type === "document.create" ? write.record ?? {} : { id: write.id })
  if (revision === undefined) return
  if (content.kind === "base" && sameContent(content.value, revision.value)) content.value = revision.value
  else if (content.kind === "delta" && sameContent(content.ops, revision.ops)) content.ops = revision.ops
}

/** A document change of a commit publication is the staged revision: its value and operations are the Storage write's and the next snapshot's. */
export function substitutePublished(client: CoreClient, change: { type?: string; record?: DocumentWrite["record"] & { id?: number }; value?: unknown; ops?: unknown }): void {
  if (change.type !== "document" || change.record === undefined || change.value === null || change.value === undefined) return
  const revision = revisionOf(client, change.record)
  if (revision === undefined) return
  if (sameContent(change.value, revision.value)) change.value = revision.value
  if (sameContent(change.ops, revision.ops)) change.ops = revision.ops
}

/** The staged revision of the document a watch observes (its wire address): the frame of the commit that wrote it carries the revision's own value and operations. */
export function revisionAt(client: CoreClient, address: { definition?: string; scope?: string; conversationId?: number; taskId?: number; key?: string }): Revision | undefined {
  const list = staged.get(client)
  if (list === undefined || list.length === 0) return undefined
  const owner = address.scope === "session" ? null : address.scope === "conversation" ? address.conversationId : address.taskId
  const id = JSON.stringify([address.definition, address.scope, owner ?? null, address.key ?? null])
  for (let i = list.length - 1; i >= 0; i--) if (list[i]!.addressId === id) return list[i]
  return undefined
}
