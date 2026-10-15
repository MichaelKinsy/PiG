// Pi's `Storage` reads (src/storage/sqlite/storage.ts: conversation, entry, findLatestHeadMarker, scanEntries, scanTasks,
// scanSubmissions, scanConversations, task, submission, submissionByRequest) over the synchronous SQL store, for the callers
// that read through a Storage object: a test's subclass of MemoryStorage that counts or holds reads and delegates to `super`.
// The statements and the paging are Pi's; the rows are the ones the core writes (CONTRACT section 3.1).
import { apply } from "@earendil-works/chord/delta"
import { StorageRequestError } from "../errors.ts"
import type { Store } from "../store.ts"
import type { Param } from "../wire.ts"

type Json = Record<string, any>
type ScanOrder = "ascending" | "descending"
type Cursor = { after: number; order?: ScanOrder }
type Page<T> = { items: T[]; next?: Cursor }

const decoder = new TextDecoder()
const parse = <T>(raw: unknown): T => JSON.parse(typeof raw === "string" ? raw : decoder.decode(raw as Uint8Array)) as T
/** Indexed strings are stored JSON-encoded (storage.ts encodeIndexedString). */
const indexed = (value: string): string => JSON.stringify(value)

/** storage/scan.ts scanStart. */
function scanStart(requested: ScanOrder | undefined, cursor: Cursor | undefined, fallback: ScanOrder): { order: ScanOrder; after: number | undefined } {
  if (requested !== undefined && requested !== "ascending" && requested !== "descending") throw new StorageRequestError(`Invalid scan order: ${String(requested)}`)
  if (cursor === undefined) return { order: requested ?? fallback, after: undefined }
  const { after, order: stored } = cursor
  if (typeof after !== "number" || !Number.isSafeInteger(after)) throw new StorageRequestError("Invalid storage cursor")
  if (stored !== undefined && stored !== "ascending" && stored !== "descending") throw new StorageRequestError("Invalid storage cursor")
  const order = stored ?? fallback
  if (requested !== undefined && requested !== order) throw new StorageRequestError(`The cursor continues a ${order} scan; the query asks for ${requested}`)
  return { order, after }
}

/** storage.ts page. */
function page<T extends { id: number }>(values: T[], limit: number, order: ScanOrder = "ascending"): Page<T> {
  const items = values.slice(0, limit)
  if (values.length <= limit) return { items }
  return { items, next: { after: items.at(-1)!.id, order } }
}

/** storage.ts scanSql. */
const scanSql = (start: { order: ScanOrder; after: number | undefined }) =>
  start.order === "ascending" ? { clause: "id > ?", param: start.after ?? -1, direction: "ASC" } : { clause: "id < ?", param: start.after ?? Number.MAX_SAFE_INTEGER, direction: "DESC" }

const blob = (column: string) => `CAST(${column} AS BLOB)`

export class StorageReads {
  private readonly store: Store
  constructor(store: Store) { this.store = store }

  private get(sql: string, params: Param[]): unknown | undefined { return this.store.all(sql, params)[0]?.[0] }
  private all<T>(sql: string, params: Param[]): T[] { return this.store.all(sql, params).map(row => parse<T>(row[0])) }

  conversation(id: number): Json | undefined {
    const raw = this.get(`SELECT ${blob("record")} FROM conversations WHERE id = ?`, [id])
    return raw === undefined ? undefined : parse<Json>(raw)
  }

  scanConversations(query: { order?: ScanOrder; ownerConversationId?: number; ownerTaskId?: number }, limit: number, cursor: Cursor | undefined): Page<Json> {
    const start = scanStart(query.order, cursor, "ascending")
    const scan = scanSql(start)
    const clauses = [scan.clause]
    const params: Param[] = [scan.param]
    if (query.ownerConversationId !== undefined) { clauses.push("owner_conversation_id = ?"); params.push(query.ownerConversationId) }
    if (query.ownerTaskId !== undefined) { clauses.push("owner_task_id = ?"); params.push(query.ownerTaskId) }
    params.push(limit + 1)
    return page(this.all<Json & { id: number }>(`SELECT ${blob("record")} FROM conversations WHERE ${clauses.join(" AND ")} ORDER BY id ${scan.direction} LIMIT ?`, params), limit, start.order)
  }

  entry(...args: unknown[]): { entry: Json; commitSeq: number } | undefined {
    // entry(id, context) or entry(conversationId, id, context)
    const scoped = args.length >= 3
    const id = scoped ? args[1] : args[0]
    if (typeof id !== "number") throw new StorageRequestError("Storage.entry() requires an entry ID")
    let conversation: Json | undefined
    if (scoped) {
      conversation = this.conversation(args[0] as number)
      if (conversation === undefined) throw new StorageRequestError(`Unknown conversation: ${args[0]}`)
    }
    const row = this.store.all(`SELECT ${blob("record")}, commit_seq FROM entries WHERE id = ?`, [id])[0]
    if (row === undefined) return undefined
    const entry = parse<Json>(row[0])
    if (conversation !== undefined) {
      let upper = Number.POSITIVE_INFINITY
      while (conversation.id !== entry.conversationId) {
        if (conversation.parent === undefined) return undefined
        upper = Math.min(upper, conversation.parent.at)
        conversation = this.conversation(conversation.parent.conversationId)!
      }
      if (entry.id > upper) return undefined
    }
    return { entry, commitSeq: Number(row[1]) }
  }

  findLatestHeadMarker(conversationId: number, atOrBeforeEntryId: number | undefined): Json | undefined {
    let conversation = this.conversation(conversationId)
    if (conversation === undefined) throw new StorageRequestError(`Unknown conversation: ${conversationId}`)
    let upper = atOrBeforeEntryId
    for (;;) {
      const raw = upper === undefined
        ? this.get(`SELECT ${blob("record")} FROM entries WHERE conversation_id = ? AND head IS NOT NULL ORDER BY id DESC LIMIT 1`, [conversation.id])
        : this.get(`SELECT ${blob("record")} FROM entries WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1`, [conversation.id, upper])
      if (raw !== undefined) return parse<Json>(raw)
      if (conversation.parent === undefined) return undefined
      upper = upper === undefined ? conversation.parent.at : Math.min(upper, conversation.parent.at)
      conversation = this.conversation(conversation.parent.conversationId)!
    }
  }

  scanEntries(query: { conversationId: number; minEntryId?: number; maxEntryId?: number; order?: ScanOrder }, limit: number, cursor: Cursor | undefined): Page<Json> {
    const { order, after } = scanStart(query.order, cursor, "descending")
    if (order === "ascending") return this.scanEntriesAscending(query, limit, after)
    let conversation = this.conversation(query.conversationId)
    if (conversation === undefined) throw new StorageRequestError(`Unknown conversation: ${query.conversationId}`)
    let upper: number | undefined = query.maxEntryId
    if (after !== undefined) upper = Math.min(upper ?? Number.MAX_SAFE_INTEGER, after - 1)
    const values: (Json & { id: number })[] = []
    for (;;) {
      const clauses = ["conversation_id = ?"]
      const params: Param[] = [conversation.id]
      if (query.minEntryId !== undefined) { clauses.push("id >= ?"); params.push(query.minEntryId) }
      if (upper !== undefined) { clauses.push("id <= ?"); params.push(upper) }
      params.push(limit + 1 - values.length)
      values.push(...this.all<Json & { id: number }>(`SELECT ${blob("record")} FROM entries WHERE ${clauses.join(" AND ")} ORDER BY id DESC LIMIT ?`, params))
      if (values.length > limit || conversation.parent === undefined) break
      upper = upper === undefined ? conversation.parent.at : Math.min(upper, conversation.parent.at)
      if (query.minEntryId !== undefined && upper !== undefined && upper < query.minEntryId) break
      conversation = this.conversation(conversation.parent.conversationId)!
    }
    return page(values, limit, "descending")
  }

  /** Oldest first: the fork chain's segments from the root conversation forward, each up to its fork point. */
  private scanEntriesAscending(query: { conversationId: number; minEntryId?: number; maxEntryId?: number }, limit: number, after: number | undefined): Page<Json> {
    const segments: { conversationId: number; upper: number | undefined }[] = []
    let conversation = this.conversation(query.conversationId)
    if (conversation === undefined) throw new StorageRequestError(`Unknown conversation: ${query.conversationId}`)
    let upper: number | undefined = query.maxEntryId
    for (;;) {
      segments.push({ conversationId: conversation.id, upper })
      if (conversation.parent === undefined) break
      upper = upper === undefined ? conversation.parent.at : Math.min(upper, conversation.parent.at)
      if (query.minEntryId !== undefined && upper !== undefined && upper < query.minEntryId) break
      conversation = this.conversation(conversation.parent.conversationId)!
    }
    let lower: number | undefined = query.minEntryId
    if (after !== undefined) lower = Math.max(lower ?? after + 1, after + 1)
    const values: (Json & { id: number })[] = []
    for (const segment of segments.reverse()) {
      const clauses = ["conversation_id = ?"]
      const params: Param[] = [segment.conversationId]
      if (lower !== undefined) { clauses.push("id >= ?"); params.push(lower) }
      if (segment.upper !== undefined) { clauses.push("id <= ?"); params.push(segment.upper) }
      params.push(limit + 1 - values.length)
      values.push(...this.all<Json & { id: number }>(`SELECT ${blob("record")} FROM entries WHERE ${clauses.join(" AND ")} ORDER BY id ASC LIMIT ?`, params))
      if (values.length > limit) break
    }
    return page(values, limit, "ascending")
  }

  task(id: number): Json | undefined {
    const raw = this.get(`SELECT ${blob("record")} FROM tasks WHERE id = ?`, [id])
    return raw === undefined ? undefined : parse<Json>(raw)
  }

  scanTasks(query: { order?: ScanOrder; conversationId?: number; kind?: string; status?: string; abortRequested?: boolean; background?: boolean }, limit: number, cursor: Cursor | undefined): Page<Json> {
    const start = scanStart(query.order, cursor, "ascending")
    const scan = scanSql(start)
    const clauses = [scan.clause]
    const params: Param[] = [scan.param]
    if (query.conversationId !== undefined) { clauses.push("conversation_id = ?"); params.push(query.conversationId) }
    if (query.kind !== undefined) { clauses.push("kind = ?"); params.push(indexed(query.kind)) }
    if (query.status !== undefined) { clauses.push("status = ?"); params.push(query.status) }
    if (query.abortRequested !== undefined) { clauses.push("abort_requested = ?"); params.push(query.abortRequested ? 1 : 0) }
    if (query.background !== undefined) { clauses.push("background = ?"); params.push(query.background ? 1 : 0) }
    params.push(limit + 1)
    return page(this.all<Json & { id: number }>(`SELECT ${blob("record")} FROM tasks WHERE ${clauses.join(" AND ")} ORDER BY id ${scan.direction} LIMIT ?`, params), limit, start.order)
  }

  submission(id: number): Json | undefined {
    const raw = this.get(`SELECT ${blob("record")} FROM submissions WHERE id = ?`, [id])
    return raw === undefined ? undefined : parse<Json>(raw)
  }

  scanSubmissions(query: { order?: ScanOrder; conversationId?: number; status?: string }, limit: number, cursor: Cursor | undefined): Page<Json> {
    const start = scanStart(query.order, cursor, "ascending")
    const scan = scanSql(start)
    const clauses = [scan.clause]
    const params: Param[] = [scan.param]
    if (query.conversationId !== undefined) { clauses.push("conversation_id = ?"); params.push(query.conversationId) }
    if (query.status !== undefined) { clauses.push("status = ?"); params.push(query.status) }
    params.push(limit + 1)
    return page(this.all<Json & { id: number }>(`SELECT ${blob("record")} FROM submissions WHERE ${clauses.join(" AND ")} ORDER BY id ${scan.direction} LIMIT ?`, params), limit, start.order)
  }

  submissionByRequest(conversationId: number, requestId: string): Json | undefined {
    const raw = this.get(`SELECT ${blob("record")} FROM submissions WHERE conversation_id = ? AND request_id = ?`, [conversationId, indexed(requestId)])
    return raw === undefined ? undefined : parse<Json>(raw)
  }

  /** documents.ts DocumentAddress columns (storage.ts addressParts). */
  private parts(address: Json) {
    const scope = address.scope as { kind: string; conversationId?: number; taskId?: number }
    const ownerId = scope.kind === "conversation" ? scope.conversationId! : scope.kind === "task" ? scope.taskId! : 0
    return { kind: indexed(address.kind), scopeKind: scope.kind, ownerId, family: address.key === undefined ? 0 : 1, keyValue: indexed(address.key ?? "") }
  }

  findDocument(address: Json, at: "current" | number): Json | undefined {
    const parts = this.parts(address)
    const params: Param[] = [parts.kind, parts.scopeKind, parts.ownerId, parts.family, parts.keyValue]
    const sql = at === "current"
      ? `SELECT ${blob("record")} FROM documents WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ? AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`
      : `SELECT ${blob("record")} FROM documents WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ? AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?) ORDER BY created_at DESC LIMIT 1`
    if (at !== "current") params.push(at, at)
    const raw = this.get(sql, params)
    return raw === undefined ? undefined : parse<Json>(raw)
  }

  /** storage.ts materializeDocument: the newest base at or before the point, then its deltas. */
  document(id: number, at: "current" | number): { record: Json; version: number; value: Json; deltasSinceBase: number } | undefined {
    const raw = this.get(`SELECT ${blob("record")} FROM documents WHERE id = ?`, [id])
    if (raw === undefined) return undefined
    const record = parse<Json>(raw)
    if (at !== "current" && (record.scope.kind !== "conversation" || record.history === "latest")) throw new StorageRequestError(`Document ${id} does not retain historical content`)
    const alive = at === "current" ? record.retiredAt === undefined : record.createdAt <= at && (record.retiredAt === undefined || at < record.retiredAt)
    if (!alive) return undefined
    const upper = at === "current" ? Number.MAX_SAFE_INTEGER : at
    const base = this.store.all(`SELECT seq, kind, version, ${blob("content")} FROM document_revisions WHERE document_id = ? AND kind = 'base' AND seq <= ? ORDER BY seq DESC LIMIT 1`, [id, upper])[0]
    if (base === undefined) throw new Error(`Document ${id} is missing a required base`)
    let value = parse<Json>(base[3])
    const tail = this.store.all(`SELECT seq, kind, version, ${blob("content")} FROM document_revisions WHERE document_id = ? AND seq > ? AND seq <= ? ORDER BY seq`, [id, base[0] as number, upper])
    for (const revision of tail) {
      if (revision[1] !== "delta" || Number(revision[2]) !== Number(base[2])) throw new Error(`Document ${id} crosses a stored version boundary without a base`)
      value = apply(value as never, parse<never>(revision[3])) as Json
    }
    return { record, version: Number(base[2]), value, deltasSinceBase: tail.length }
  }
}
