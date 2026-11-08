// Ports packages/durable/src/session/keys.ts GatedStorage: the Storage a job on the Session line reads through. Every read first touches its
// key, which waits while the key has an unpublished writer in the core's writer, so the read sees what the line admitted before it. Scans
// that cannot be keyed touch ALL_KEYS. The keys are the strings the core names them with (session/keys.go).
import type { Context } from "@earendil-works/chord/context"
import { addressId, type DocumentAddress } from "./documents.ts"

/** Every unpublished write: what a read that cannot be keyed, such as a scan, touches. */
export const ALL_KEYS = "*"

/** The incarnations alive in a scope: created and retired blind, read by scans. */
const scopeKey = (scope: { kind: string; conversationId?: number; taskId?: number }) => scope.kind === "session" ? "docscope:session" : scope.kind === "conversation" ? `docscope:c${scope.conversationId}` : `docscope:t${scope.taskId}`

/** Wait while `key` has an unpublished writer: a promise, or `undefined` when there is none (session.ts touch). */
export type Touch = (key: string) => Promise<void> | undefined

/** The Pi Storage the line reads through: its methods, each called with Pi's arguments. */
type Reads = Record<string, (...args: unknown[]) => Promise<unknown>>

export class GatedStorage {
  private readonly storage: Reads
  private readonly touch: Touch

  constructor(storage: unknown, touch: Touch) {
    this.storage = storage as Reads
    this.touch = touch
  }

  commit(...args: unknown[]) { return this.storage.commit!(...args) }
  mintId() { return this.storage.mintId!() }
  close(context: Context) { return this.storage.close!(context) }

  conversation(id: number, context: Context) { return this.after(`conv:${id}`, () => this.storage.conversation!(id, context)) }
  scanConversations(query: unknown, limit: number, cursor: unknown, context: Context) { return this.after(ALL_KEYS, () => this.storage.scanConversations!(query, limit, cursor, context)) }

  entry(id: number, context: Context): Promise<unknown>
  entry(conversationId: number, id: number, context: Context): Promise<unknown>
  entry(first: number, second: number | Context, third?: Context): Promise<unknown> {
    if (third === undefined) return this.after(`entry:${first}`, () => this.storage.entry!(first, second))
    // Visibility follows the conversation's immutable ancestry; the conversation may be unpublished itself.
    return this.after(`conv:${first}`, () => this.after(`entry:${second as number}`, () => this.storage.entry!(first, second, third)))
  }

  findLatestHeadMarker(conversationId: number, atOrBeforeEntryId: number | undefined, context: Context) {
    return this.afterEntries(conversationId, () => this.storage.findLatestHeadMarker!(conversationId, atOrBeforeEntryId, context))
  }
  scanEntries(query: { conversationId: number }, limit: number, cursor: unknown, context: Context) {
    return this.afterEntries(query.conversationId, () => this.storage.scanEntries!(query, limit, cursor, context))
  }

  task(id: number, context: Context) { return this.after(`task:${id}`, () => this.storage.task!(id, context)) }
  scanTasks(query: unknown, limit: number, cursor: unknown, context: Context) { return this.after(ALL_KEYS, () => this.storage.scanTasks!(query, limit, cursor, context)) }
  submission(id: number, context: Context) { return this.after(`sub:${id}`, () => this.storage.submission!(id, context)) }
  scanSubmissions(query: unknown, limit: number, cursor: unknown, context: Context) { return this.after(ALL_KEYS, () => this.storage.scanSubmissions!(query, limit, cursor, context)) }
  submissionByRequest(conversationId: number, requestId: string, context: Context) {
    return this.after(`req:${conversationId}:${requestId}`, () => this.storage.submissionByRequest!(conversationId, requestId, context))
  }
  findDocument(address: DocumentAddress, at: unknown, context: Context) { return this.after(`doc:${addressId(address)}`, () => this.storage.findDocument!(address, at, context)) }
  document(id: number, at: unknown, context: Context) { return this.after(`docid:${id}`, () => this.storage.document!(id, at, context)) }
  scanDocuments(query: { scope: { kind: string; conversationId?: number; taskId?: number } }, limit: number, cursor: unknown, context: Context) {
    return this.after(scopeKey(query.scope), () => this.storage.scanDocuments!(query, limit, cursor, context))
  }

  /** Run a read of a conversation's entries once neither its appends nor its creation has an unpublished writer: Storage refuses the read of a conversation it does not have yet. */
  private afterEntries(conversationId: number, read: () => Promise<unknown>): Promise<unknown> {
    return this.after(`conv:${conversationId}`, () => this.after(`entry-append:${conversationId}`, read))
  }

  /** Run `read` once `key` has no unpublished writer. */
  private after(key: string, read: () => Promise<unknown>): Promise<unknown> {
    const gate = this.touch(key)
    return gate === undefined ? read() : gate.then(read)
  }
}
