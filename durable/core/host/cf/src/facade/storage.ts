// Storage handles for Harness.open: a synchronous SQL surface (ABI section 3) plus how to close it. The Durable Object host
// passes `doStorage(ctx.storage)`; Node programs pass MemoryStorage or openNodeSqliteStorage.
import { doStore } from "../store-do.ts"
import { apply as applyOps } from "@earendil-works/chord/delta"
import { zeroCounters, type Store } from "../store.ts"
import { StorageReads } from "./storage-reads.ts"

/**
 * The Storage adapter whose await structure the core models when it charges Pi's microtask hops (ABI.md 5.3 rule 6, the owner's
 * S7 decision): each adapter names its profile, and the host sends it in `open`. `gate` is the conformance reference's capsql
 * facade behind the lazy MemoryStorage proxy; `gate-capsql` is the same facade opened directly (the reference's openNodeSqliteStorage and
 * openNodeJsonlStorage); only the contract hooks select them (contract-setup.ts).
 */
export type StorageProfile = "memory" | "node-sqlite" | "cloudflare-do" | "jsonl" | "gate" | "gate-capsql" | "node"

export interface Storage { open(): Promise<Store> | Store; close?(): void; readonly profile?: StorageProfile }

/** A Durable Object's SQL storage (`ctx.storage`). The Object owns its lifetime. */
export const openDurableObjectSqliteStorage = (storage: DurableObjectStorage): Storage => ({ open: () => doStore(storage), profile: "cloudflare-do" })

/**
 * The conformance gate's store (durable/contract hooks/register.mjs): when it is installed, every store this module opens goes
 * through its capsql capture, so the gate sees each statement and commit the host issues.
 */
const contractStore = async (path: string): Promise<(Store & { close(): void }) | undefined> => {
  const hook = (globalThis as Record<symbol, { openStore?: (path: string) => Promise<Omit<Store, "counters"> & { close(): void }> } | undefined>)[Symbol.for("pig.contract")]
  if (!hook?.openStore) return undefined
  return { ...(await hook.openStore(path)), counters: zeroCounters() }
}

let memoryStores = 0

/** A Storage over one SQLite store: the synchronous SQL surface the core runs on, and Pi's `Storage` methods over the committed rows. */
export abstract class SqlStorage implements Storage {
  abstract readonly profile: StorageProfile
  protected store?: Store & { close(): void }
  protected closed = false
  protected abstract openStore(): Promise<Store & { close(): void }>
  async open() { return this.store ??= await this.openStore() }
  close() { this.closed = true; this.store?.close() }
  /** Pi's Storage reads over the committed rows (storage-reads.ts); every one rejects once the storage is closed, as MemoryStorage's does. */
  private async reads(): Promise<StorageReads> {
    if (this.closed) throw new Error(`${this.constructor.name} is closed`)
    return new StorageReads(await this.open())
  }
  async conversation(id: number, _context?: unknown) { return (await this.reads()).conversation(id) }
  async scanConversations(query: Parameters<StorageReads["scanConversations"]>[0], limit: number, cursor: Parameters<StorageReads["scanConversations"]>[2], _context?: unknown) { return (await this.reads()).scanConversations(query, limit, cursor) }
  async entry(...args: unknown[]) { return (await this.reads()).entry(...args) }
  async findLatestHeadMarker(conversationId: number, atOrBeforeEntryId: number | undefined, _context?: unknown) { return (await this.reads()).findLatestHeadMarker(conversationId, atOrBeforeEntryId) }
  async scanEntries(query: Parameters<StorageReads["scanEntries"]>[0], limit: number, cursor: Parameters<StorageReads["scanEntries"]>[2], _context?: unknown) { return (await this.reads()).scanEntries(query, limit, cursor) }
  async task(id: number, _context?: unknown) { return (await this.reads()).task(id) }
  async scanTasks(query: Parameters<StorageReads["scanTasks"]>[0], limit: number, cursor: Parameters<StorageReads["scanTasks"]>[2], _context?: unknown) { return (await this.reads()).scanTasks(query, limit, cursor) }
  async submission(id: number, _context?: unknown) { return (await this.reads()).submission(id) }
  async scanSubmissions(query: Parameters<StorageReads["scanSubmissions"]>[0], limit: number, cursor: Parameters<StorageReads["scanSubmissions"]>[2], _context?: unknown) { return (await this.reads()).scanSubmissions(query, limit, cursor) }
  async submissionByRequest(conversationId: number, requestId: string, _context?: unknown) { return (await this.reads()).submissionByRequest(conversationId, requestId) }
  /** Commits the Harness's core hands to the Storage's own `commit` first (the gate): the call is a no-op, the core applies them afterwards. */
  gating = 0
  /**
   * Pi's `Storage.commit`. Under the Storage gate (PROTOCOL.md) it is a no-op the overriding subclasses call through `super.commit`: the
   * core applies the commit itself once the call resolves. Called directly (a test seeding a store before a Harness opens it) it
   * applies the writes through the core in storage mode (storage-direct.ts).
   */
  async commit(writes: readonly unknown[], _context?: unknown): Promise<number> {
    const store = await this.open()
    if (this.gating === 0) return (await import("./storage-direct.ts")).directCommit(store, writes)
    // The commit being gated takes the store's next sequence number (CONTRACT section 3.1: a commit's seq is the next_seq it consumes);
    // a store the core has not initialized yet (an open that failed first) is at its first.
    try { return Number(store.all("SELECT next_seq FROM durable_metadata", [])[0]?.[0] ?? 1) } catch { return 1 }
  }
  private minted = 0
  /** Pi's `Storage.mintId`: a fresh candidate from the Session-global ID namespace (above every ID the store has claimed). */
  async mintId<I = number>(): Promise<I> {
    const store = await this.open()
    let claimed = 0
    try { claimed = Number(store.all("SELECT COALESCE(MAX(id), 0) FROM record_ids", [])[0]?.[0] ?? 0) } catch { /* the core has not created the schema yet */ }
    this.minted = Math.max(this.minted, claimed) + 1
    return this.minted as I
  }
  /** The `documents` columns Pi's sqlite storage derives from an address (storage/sqlite/storage.ts addressParts); strings are JSON-encoded. */
  private static parts(address: { kind: string; scope: { kind: string; conversationId?: number; taskId?: number }; key?: string }) {
    const { scope } = address
    return [JSON.stringify(address.kind), scope.kind, scope.kind === "conversation" ? scope.conversationId : scope.kind === "task" ? scope.taskId : 0, address.key === undefined ? 0 : 1, JSON.stringify(address.key ?? "")]
  }
  private static text = (raw: unknown): string => typeof raw === "string" ? raw : new TextDecoder().decode(raw as Uint8Array)
  async findDocument(address: Parameters<StorageReads["findDocument"]>[0], at: Parameters<StorageReads["findDocument"]>[1], _context?: unknown) { return (await this.reads()).findDocument(address, at) }
  async document(id: number, at: Parameters<StorageReads["document"]>[1], _context?: unknown) { return (await this.reads()).document(id, at) }
  /** Pi's `Storage.scanDocuments`: the records of one scope ascending by ID, optionally of one kind, alive at `query.at`. */
  async scanDocuments(query: { scope: { kind: string; conversationId?: number; taskId?: number }; kind?: string; at: "current" | number }, limit: number, cursor: { after?: number } | undefined, _context?: unknown): Promise<{ items: Record<string, unknown>[]; next?: { after: number } }> {
    const store = await this.open()
    const [, scopeKind, owner] = SqlStorage.parts({ kind: "", scope: query.scope })
    const clauses = ["scope_kind = ?", "owner_id = ?", "id > ?"]
    const params: unknown[] = [scopeKind, owner, cursor?.after ?? -1]
    if (query.kind !== undefined) { clauses.push("kind = ?"); params.push(JSON.stringify(query.kind)) }
    if (query.at === "current") clauses.push("retired_at IS NULL")
    else { clauses.push("created_at <= ?", "(retired_at IS NULL OR retired_at > ?)"); params.push(query.at, query.at) }
    params.push(limit + 1)
    const rows = store.all(`SELECT CAST(record AS BLOB) FROM documents WHERE ${clauses.join(" AND ")} ORDER BY id LIMIT ?`, params as never).map(r => JSON.parse(SqlStorage.text(r[0])) as Record<string, unknown>)
    const items = rows.slice(0, limit)
    return rows.length <= limit ? { items } : { items, next: { after: items.at(-1)!.id as number } }
  }
}

/** An in-memory SQLite database (node:sqlite), private to one Harness. */
export class MemoryStorage extends SqlStorage {
  readonly profile: StorageProfile = "memory"
  private readonly name = `:memory:${memoryStores++}`
  protected async openStore() {
    const captured = await contractStore(this.name)
    if (captured) return captured
    const { nodeStore } = await import("../store-node.ts")
    return nodeStore(":memory:", { wal: false })
  }
  /** memory.ts close: the Storage stops answering, and what it committed survives for a reopen (a test's `reopen()` clears `closed`); the database is released with the object. */
  override close() { this.closed = true }
}

/** A SQLite file (node:sqlite): the store pi-durable's `openNodeSqliteStorage` opens. Its `commit` and reads are Pi's Storage methods, so a caller can wrap them. */
class NodeSqliteStorage extends SqlStorage {
  readonly profile: StorageProfile
  private readonly path: string
  constructor(path: string, profile: StorageProfile) { super(); this.path = path; this.profile = profile }
  protected async openStore() {
    const captured = await contractStore(this.path)
    if (captured) return captured
    const { nodeStore } = await import("../store-node.ts")
    return nodeStore(this.path)
  }
}

export function openNodeSqliteStorage(path: string, profile: StorageProfile = "node-sqlite"): Storage {
  return new NodeSqliteStorage(path, profile)
}

/** pi-durable's `openNodeJsonlStorage(directory)`: the store lives in one SQLite file inside the directory. */
export function openNodeJsonlStorage(directory: string): Storage {
  return openNodeSqliteStorage(`${directory.replace(/\/$/, "")}/jsonl-as-sqlite`, "jsonl")
}

/** The Pi `Storage` methods a caller's Storage object replaces: a subclass of MemoryStorage (or an assignment on a node SQLite storage) that overrides one, or a proxy that wraps it. `commit` is gated by the core (PROTOCOL.md, "Storage gate"). */
const PI_STORAGE_METHODS = ["commit", "mintId", "conversation", "scanConversations", "entry", "findLatestHeadMarker", "scanEntries", "task", "scanTasks", "submission", "submissionByRequest", "scanSubmissions", "findDocument", "document", "scanDocuments"] as const
export function overriddenMethods(storage: object): string[] {
  if (!(storage instanceof SqlStorage) && typeof (storage as { commit?: unknown }).commit !== "function") return []
  return PI_STORAGE_METHODS.filter(name => (storage as Record<string, unknown>)[name] !== undefined && (storage as Record<string, unknown>)[name] !== (SqlStorage.prototype as unknown as Record<string, unknown>)[name])
}
