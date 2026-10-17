// Checkpoints of derived contexts, so a cold read of a long conversation does not scan and derive every entry again.
//
// A checkpoint is a cache of a pure derivation, never authoritative: the stored entries are. It holds, for one conversation and
// one head marker, the SETTLED messages of the entries through `tail` (everything before the last assistant message: later
// parts start with an assistant message, so those messages are final, context.ts settle) in segments of at most SEGMENT_BYTES,
// and the raw open contributions after them, and the baseline (Pi 5713c950). A read resumes from it (context.ts rangeFromCheckpoint) and scans only the entries
// after `tail`. A checkpoint that is missing, torn, malformed (a value that is not a message list, open messages the tool result
// ordering cannot read), of another store, schema or derivation, for another head marker or past the requested tail is ignored and
// the context is derived from the entries. Bytes rewritten in place into other well-formed messages are not detected, as a
// rewritten entry record is not. The reader writes checkpoints for contexts without a head marker only (contextstore.ts save).
//
// The tables are `pig_`-prefixed, outside Pi's tables and sequence (Pi's code does not read them, and a store Pi wrote has
// none), and are written by the host in a transaction of their own.
import { derivationId, type Baseline, type Message } from "./context.ts"
import type { Store } from "./store.ts"

/** The most bytes of messages one segment row holds (a Durable Object value is 2 MB). */
export const SEGMENT_BYTES = 512 * 1024

const CREATE_CHECKPOINTS = `CREATE TABLE IF NOT EXISTS pig_context_checkpoints (
  conversation INTEGER PRIMARY KEY, head INTEGER NOT NULL, tail INTEGER NOT NULL, digest TEXT NOT NULL, schema_version INTEGER NOT NULL,
  derivation TEXT NOT NULL, settled INTEGER NOT NULL, segments INTEGER NOT NULL, open BLOB NOT NULL) STRICT`
const CREATE_SEGMENTS = `CREATE TABLE IF NOT EXISTS pig_context_segments (
  conversation INTEGER NOT NULL, idx INTEGER NOT NULL, count INTEGER NOT NULL, messages BLOB NOT NULL, PRIMARY KEY (conversation, idx)) STRICT, WITHOUT ROWID`
const SELECT_CHECKPOINT = "SELECT head, tail, digest, schema_version, derivation, settled, segments, CAST(open AS BLOB) FROM pig_context_checkpoints WHERE conversation = ?"
const SELECT_SEGMENTS = "SELECT idx, count, CAST(messages AS BLOB) FROM pig_context_segments WHERE conversation = ? ORDER BY idx"
const SELECT_SCHEMA = "SELECT version FROM durable_schema WHERE singleton = 1"
const SELECT_ENTRY = "SELECT CAST(record AS BLOB) FROM entries WHERE id = ?"

const enc = new TextEncoder()
const dec = new TextDecoder()
const bytesOf = (blob: unknown) => (blob instanceof Uint8Array ? blob : new Uint8Array(blob as ArrayBuffer))
/** A stored message is an object with a role; anything else (a null, a list) is a checkpoint that is not one. */
const isMessage = (value: unknown): value is Message =>
  typeof value === "object" && value !== null && !Array.isArray(value) && typeof (value as { role?: unknown }).role === "string"

/** A 64-bit hash of bytes (cyrb53 over two seeds): it tells records of different stores apart, it is not a security boundary. */
export function digestOf(bytes: Uint8Array): string {
  let a = 0xdeadbeef, b = 0x41c6ce57
  for (let i = 0; i < bytes.length; i++) {
    a = Math.imul(a ^ bytes[i]!, 2654435761)
    b = Math.imul(b ^ bytes[i]!, 1597334677)
  }
  a = Math.imul(a ^ (a >>> 16), 2246822507) ^ Math.imul(b ^ (b >>> 13), 3266489909)
  b = Math.imul(b ^ (b >>> 16), 2246822507) ^ Math.imul(a ^ (a >>> 13), 3266489909)
  return `${(b >>> 0).toString(16).padStart(8, "0")}${(a >>> 0).toString(16).padStart(8, "0")}`
}

export type Checkpoint = { readonly tail: number; readonly settled: Message[]; readonly open: Message[]; readonly baseline: Baseline; readonly segments: number }

/** What a reader last stored for a conversation: a save that continues it appends only if the header still says so. */
/**
 * How a checkpoint names the baseline (context.ts `findBaseline`): "unresolved" while a later entry may be one, null when the range
 * has none (also when an edit omitted it, so no message of the range is it), else the index of its message among the settled
 * messages followed by the open ones.
 */
function baselineIndex(settled: readonly Message[], open: readonly Message[], baseline: Baseline): number | null | "unresolved" {
  if (baseline === undefined) return "unresolved"
  if (baseline === null) return null
  const at = settled.indexOf(baseline)
  if (at >= 0) return at
  const inOpen = open.indexOf(baseline)
  return inOpen >= 0 ? settled.length + inOpen : null
}

export type Stored = { readonly tail: number; readonly count: number; readonly segments: number }

type StoreHandle = Pick<Store, "all" | "run" | "tx">

export class ContextSegments {
  private readonly store: StoreHandle
  private readonly derivation = derivationId()
  constructor(store: StoreHandle) { this.store = store }

  /**
   * The checkpoint of `conversation` for head marker entry `head` (0: none) if it is valid for a read through `maxTail`.
   * Every way it can be wrong returns undefined, and so does a store that has no such tables.
   */
  load(conversation: number, head: number, maxTail: number): Checkpoint | undefined {
    try {
      const rows = this.store.all(SELECT_CHECKPOINT, [conversation])
      if (rows.length !== 1) return undefined
      const [rowHead, tail, digest, schema, derivation, settledCount, segmentCount, openBlob] = rows[0]! as [number, number, string, number, string, number, number, unknown]
      if (rowHead !== head || tail > maxTail || derivation !== this.derivation) return undefined
      const version = this.store.all(SELECT_SCHEMA, [])
      if (version.length !== 1 || version[0]![0] !== schema) return undefined
      const record = this.store.all(SELECT_ENTRY, [tail])
      if (record.length !== 1 || digestOf(bytesOf(record[0]![0])) !== digest) return undefined
      const segments = this.store.all(SELECT_SEGMENTS, [conversation])
      if (segments.length !== segmentCount) return undefined
      const settled: Message[] = []
      for (let i = 0; i < segments.length; i++) {
        const [idx, count, blob] = segments[i]! as [number, number, unknown]
        if (idx !== i) return undefined
        const messages = JSON.parse(dec.decode(bytesOf(blob))) as Message[]
        if (!Array.isArray(messages) || messages.length !== count) return undefined
        for (const message of messages) {
          if (!isMessage(message)) return undefined
          settled.push(message)
        }
      }
      if (settled.length !== settledCount) return undefined
      // The open column holds the baseline (see `baselineIndex`) and the open messages: { baseline, open }.
      const stored = JSON.parse(dec.decode(bytesOf(openBlob))) as { baseline?: unknown; open?: unknown }
      const open = stored.open as Message[]
      if (typeof stored !== "object" || stored === null || !Array.isArray(open) || !open.every(isMessage)) return undefined
      let baseline: Baseline
      if (stored.baseline === null) baseline = null
      else if (stored.baseline === "unresolved") baseline = undefined
      else if (typeof stored.baseline === "number" && Number.isInteger(stored.baseline) && stored.baseline >= 0 && stored.baseline < settled.length + open.length) baseline = stored.baseline < settled.length ? settled[stored.baseline]! : open[stored.baseline - settled.length]!
      else return undefined
      return { tail, settled, open, baseline, segments: segmentCount }
    } catch {
      return undefined
    }
  }

  /**
   * Store the checkpoint of `conversation`: the settled messages, the open contributions, and the record of entry `tail` that
   * binds the checkpoint to this store. With `prior`, what this reader stored last for the conversation and whose settled list
   * `settled` continues, only the messages after `prior.count` are appended, and the write fails unless the stored header is
   * still `prior`'s; without it everything is written again. The header row is written last, in the same transaction as the
   * segments it counts.
   */
  save(conversation: number, head: number, tail: number, settled: readonly Message[], open: readonly Message[], baseline: Baseline, prior?: Stored): Stored {
    const chunks = this.chunk(settled, prior?.count ?? 0)
    let segments = prior?.segments ?? 0
    this.store.tx(() => {
      this.store.run(CREATE_CHECKPOINTS, [])
      this.store.run(CREATE_SEGMENTS, [])
      if (prior === undefined) this.store.run("DELETE FROM pig_context_segments WHERE conversation = ?", [conversation])
      for (const chunk of chunks) this.store.run("INSERT INTO pig_context_segments (conversation, idx, count, messages) VALUES (?, ?, ?, ?)", [conversation, segments++, chunk.count, chunk.bytes])
      const version = this.store.all(SELECT_SCHEMA, [])[0]![0] as number
      const record = this.store.all(SELECT_ENTRY, [tail])
      if (record.length !== 1) throw new Error(`context checkpoint names entry ${tail}, which storage does not have`)
      const values = [head, tail, digestOf(bytesOf(record[0]![0])), version, this.derivation, settled.length, segments, enc.encode(JSON.stringify({ baseline: baselineIndex(settled, open, baseline), open }))]
      if (prior === undefined) {
        this.store.run(
          "INSERT OR REPLACE INTO pig_context_checkpoints (conversation, head, tail, digest, schema_version, derivation, settled, segments, open) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
          [conversation, ...values],
        )
      } else if (this.store.run(
        "UPDATE pig_context_checkpoints SET head = ?, tail = ?, digest = ?, schema_version = ?, derivation = ?, settled = ?, segments = ?, open = ? WHERE conversation = ? AND head = ? AND tail = ? AND derivation = ? AND settled = ? AND segments = ?",
        [...values, conversation, head, prior.tail, this.derivation, prior.count, prior.segments],
      ) === 0) {
        // Another writer replaced the checkpoint this reader continues: its segments are not the ones counted.
        throw new Error(`context checkpoint of conversation ${conversation} changed since it was stored`)
      }
    })
    return { tail, count: settled.length, segments }
  }

  /** The messages from `from` on as JSON arrays of at most SEGMENT_BYTES each (a message larger than that is a segment of its own). */
  private chunk(settled: readonly Message[], from: number): { count: number; bytes: Uint8Array }[] {
    const out: { count: number; bytes: Uint8Array }[] = []
    let parts: Uint8Array[] = []
    let size = 2
    const flush = () => {
      if (parts.length === 0) return
      const bytes = new Uint8Array(size)
      let at = 0
      bytes[at++] = 0x5b
      parts.forEach((part, i) => { if (i > 0) bytes[at++] = 0x2c; bytes.set(part, at); at += part.length })
      bytes[at] = 0x5d
      out.push({ count: parts.length, bytes })
      parts = []
      size = 2
    }
    for (let i = from; i < settled.length; i++) {
      const part = enc.encode(JSON.stringify(settled[i]))
      if (parts.length > 0 && size + part.length + 1 > SEGMENT_BYTES) flush()
      size += part.length + (parts.length > 0 ? 1 : 0)
      parts.push(part)
    }
    flush()
    return out
  }
}
