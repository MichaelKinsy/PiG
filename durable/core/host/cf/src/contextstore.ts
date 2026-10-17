// The committed contexts a model_context range names (ABI section 10.2), derived from the records in storage with
// context.ts (pi-durable's range algorithm). Records are read as bytes (CAST AS BLOB), decoded once and parsed in V8. Each
// conversation keeps its newest range, so a later request reads only the entries after the ones it holds; Pi keeps the same
// range in the scheduler and drops it when the conversation has been idle for contextRetentionMs.
import { rangeFromCheckpoint, rangeWithin, type ContextBounds, type ContextRange, type EntryRecord, type HeadMarker, type Message } from "./context.ts"
import type { ContextSegments, Stored } from "./contextsegments.ts"
import { RecipeError } from "./recipe.ts"

const dec = new TextDecoder()
const CONVERSATION_SQL = "SELECT CAST(record AS BLOB) FROM conversations WHERE id = ?"
/**
 * One page of a conversation's entries in an id range as one row: the newest id of the page and the records as one JSON array
 * (BLOB), so a page is one result value, one decode and one `JSON.parse` instead of a row object, a decode and a parse per
 * entry. The page is empty (NULL id) past the last entry, and a page of fewer entries than asked for is the last. A page that holds more than the store can return in one value (a
 * Durable Object's 2 MB) fails; the reader then halves its page size, down to one record, and keeps the smaller size.
 */
const FIRST_PAGE_ROWS = 1024
const ENTRY_PAGE_SQL = `SELECT max(id), count(*), CAST('[' || group_concat(record, ',') || ']' AS BLOB) FROM (
  SELECT id, record FROM entries WHERE conversation_id = ? AND id > ? AND id <= ? ORDER BY id LIMIT ?)`
const ENTRY_SQL = "SELECT CAST(record AS BLOB) FROM entries WHERE id = ?"

/** The most conversations whose range the reader keeps; the least recently asked for is dropped past it. */
export const MAX_KEPT_RANGES = 64

type Rows = (sql: string, params: (number | null)[]) => unknown[][]
type Conversation = { id: number; parent?: { conversationId: number; at: number } }

const bytesOf = (blob: unknown) => (blob instanceof Uint8Array ? blob : new Uint8Array(blob as ArrayBuffer))
const parse = <T>(blob: unknown): T => JSON.parse(dec.decode(bytesOf(blob))) as T

export type ContextViewJSON = { head?: HeadMarker; entries: EntryRecord[]; contributions: Message[][]; messages: Message[] }

/** Objects `freezeJson()` froze with everything in them: a frozen object is not walked again. */
const frozen = new WeakSet<object>()

function freezeJson<T>(value: T): T {
  if (value === null || typeof value !== "object" || frozen.has(value)) return value
  for (const child of Object.values(value)) freezeJson(child)
  Object.freeze(value)
  frozen.add(value)
  return value
}

export class ContextReader {
  private readonly ranges = new Map<number, ContextRange>()
  private readonly conversations = new Map<number, Conversation>()
  private readonly all: Rows
  private pageRows = FIRST_PAGE_ROWS
  private readonly segments: ContextSegments | undefined
  /** What the checkpoint store holds for a conversation: the settled list it was written from, and how much of it. */
  private readonly saved = new Map<number, Stored & { settled: readonly Message[]; head: number }>()
  private readonly pending = new Set<number>()
  private reads = 0
  private readonly checkpointEvery: number
  private readonly checkpointAfterReads: number
  /**
   * `segments` is where checkpoints are kept; without it every cold read derives from the entries. A checkpoint is written
   * once `checkpointEvery` messages have settled that the last one lacks, at the first read after `checkpointAfterReads`
   * reads, or when the range is dropped or evicted.
   */
  constructor(all: Rows, segments?: ContextSegments, checkpointEvery = 256, checkpointAfterReads = 512) {
    this.all = all
    this.segments = segments
    this.checkpointEvery = checkpointEvery
    this.checkpointAfterReads = checkpointAfterReads
  }

  get size() { return this.ranges.size }

  /** The model messages of conversation `conversation`'s context cut off at entry `tail` under head marker entry `head` (0: none). */
  messages(conversation: number, head: number, tail: number): Message[] {
    return [...this.read(conversation, head, tail).view.messages]
  }

  /** The kept range of the context cut off at entry `tail` under head marker entry `head` (0: none), derived or extended from the one kept. */
  read(conversation: number, head: number, tail: number, full = false): ContextRange {
    let previous = this.ranges.get(conversation)
    // A range resumed from a checkpoint holds only the entries after it: a read that lists the entries derives them all.
    if (full && previous?.partial) previous = undefined
    let marker: HeadMarker | undefined
    if (head !== 0 && previous?.bounds.head?.id === head) {
      marker = previous.bounds.head // a stored record never changes
    } else if (head !== 0) {
      const rows = this.all(ENTRY_SQL, [head])
      if (rows.length === 0) throw new RecipeError(`model_context range names head marker ${head}, which storage does not have`)
      marker = parse<HeadMarker>(rows[0]![0])
      if (marker.head === undefined) throw new RecipeError(`model_context range names entry ${head}, which is not a head marker`)
    }
    const bounds: ContextBounds = { head: marker, tail }
    if (previous === undefined && !full && this.segments !== undefined) {
      const checkpoint = this.segments.load(conversation, head, tail)
      // A checkpoint whose open messages Pi's tool result ordering cannot read is ignored like any other bad one.
      let resumed: ContextRange | undefined
      try {
        if (checkpoint !== undefined) resumed = rangeFromCheckpoint(marker, checkpoint.tail, checkpoint.settled, checkpoint.open, checkpoint.baseline)
      } catch { resumed = undefined }
      if (resumed !== undefined) {
        previous = resumed
        this.saved.set(conversation, { settled: checkpoint!.settled, head, tail: checkpoint!.tail, count: checkpoint!.settled.length, segments: checkpoint!.segments })
      }
    }
    const missingTail = () => new RecipeError(`model_context range names tail ${tail}, which conversation ${conversation} does not have`)
    // The kept range is extended in place, so a scan that does not end at the tail fails before the extension appends to it.
    let scanned: EntryRecord[] | undefined
    const scan = (min: number | undefined, max: number) => {
      const entries = (scanned = this.scan(conversation, min, max))
      if (entries.length > 0 && entries.at(-1)!.id !== tail) throw missingTail()
      return entries
    }
    const range = rangeWithin(bounds, previous, scan, true)
    // A range resumed from a checkpoint holds no entries of its own: a tail past it that scans nothing is missing too.
    const missing = range.partial && scanned?.length === 0 && previous !== undefined && tail > previous.bounds.tail
    if (missing || (range.view.entries.length > 0 && range.entries.at(-1)?.id !== tail)) throw missingTail()
    // Like Pi's scheduler (runtime.context), a read through an older entry does not replace a newer kept range.
    const kept = previous === undefined || previous.bounds.tail <= range.bounds.tail ? range : previous
    this.ranges.delete(conversation)
    this.ranges.set(conversation, kept)
    for (const oldest of this.ranges.keys()) {
      if (this.ranges.size <= MAX_KEPT_RANGES) break
      this.forget(oldest)
    }
    this.considerCheckpoint(conversation, kept)
    return range
  }

  private considerCheckpoint(conversation: number, range: ContextRange) {
    if (this.segments === undefined) return
    const saved = this.saved.get(conversation)
    const stored = saved !== undefined && saved.settled === range.settled && saved.head === (range.bounds.head?.id ?? 0) ? saved.count : 0
    if (range.settled.length - stored >= this.checkpointEvery) this.pending.add(conversation)
    if (this.pending.size > 0 && ++this.reads >= this.checkpointAfterReads) this.flush()
  }

  /**
   * Write the checkpoints that are due. A checkpoint is a cache: one that cannot be written (a store that refuses, a full
   * disk) is skipped, and the next read does not need it.
   */
  flush() {
    this.reads = 0
    for (const conversation of this.pending) this.save(conversation)
  }

  private save(conversation: number) {
    this.pending.delete(conversation)
    const range = this.ranges.get(conversation)
    if (range === undefined || this.segments === undefined) return
    // A head marker's context is read whole (answerContextRead: its summary lists the active entries), so a checkpoint under a
    // marker is never resumed and would only rewrite the messages at every release. An edit of an entry after the tail derives
    // the range again when that entry is appended (context.ts `edited`); a checkpoint does not hold the edits.
    if (range.bounds.head !== undefined) return
    for (const target of range.edited) if (target > range.bounds.tail) return
    const head = 0
    const saved = this.saved.get(conversation)
    const continues = saved !== undefined && saved.settled === range.settled && saved.head === head
    try {
      const stored = this.segments.save(conversation, head, range.bounds.tail, range.settled, range.open, range.baseline, continues ? saved : undefined)
      this.saved.set(conversation, { ...stored, settled: range.settled, head })
    } catch {
      this.saved.delete(conversation)
    }
  }

  /** Stop keeping a conversation's range: write its checkpoint if one is due, and keep nothing of its messages. */
  private forget(conversation: number) {
    if (this.pending.has(conversation)) this.save(conversation)
    this.ranges.delete(conversation)
    this.saved.delete(conversation)
  }

  /**
   * Pi's ContextView of the context a model request or a context read names (ABI section 8.1, `$context`): the arrays are the
   * caller's, the records and messages are shared with the kept range and frozen, as Pi's kept ranges are.
   */
  view(conversation: number, head: number, tail: number): ContextViewJSON {
    const { view } = this.read(conversation, head, tail, true)
    for (const entry of view.entries) freezeJson(entry)
    for (const contributed of view.contributions) for (const message of contributed) freezeJson(message)
    for (const message of view.messages) freezeJson(message)
    return { ...(view.head === undefined ? {} : { head: freezeJson(view.head) }), entries: [...view.entries], contributions: view.contributions.map((c) => [...c]), messages: [...view.messages] }
  }

  /** Forget a conversation's range (the conversation unloaded or was idle past its retention). */
  drop(conversation: number) { this.forget(conversation) }

  private conversation(id: number): Conversation {
    let c = this.conversations.get(id)
    if (c === undefined) {
      const rows = this.all(CONVERSATION_SQL, [id])
      if (rows.length === 0) throw new RecipeError(`Unknown conversation: ${id}`)
      c = parse<Conversation>(rows[0]![0])
      this.conversations.set(id, c)
    }
    return c
  }

  /** The entries with ids in [min, max] visible from `conversation` (its own and its fork ancestors' up to each fork point), oldest first. */
  private scan(conversation: number, min: number | undefined, max: number): EntryRecord[] {
    const segments: EntryRecord[][] = []
    let upper = max
    for (let c: Conversation | undefined = this.conversation(conversation); c !== undefined; ) {
      const entries: EntryRecord[] = []
      let after = (min ?? 1) - 1
      for (;;) {
        let page: unknown[] | undefined
        try {
          page = this.all(ENTRY_PAGE_SQL, [c.id, after, upper, this.pageRows])[0]
        } catch (e) {
          if (this.pageRows === 1) throw e
          this.pageRows >>= 1
          continue
        }
        if (page === undefined || page[0] === null) break
        const records = parse<EntryRecord[]>(page[2])
        // The subquery's ORDER BY and LIMIT choose the page's records, but SQLite leaves the order group_concat joins them in
        // unspecified: a page out of id order is put back in it (the ids are unique), not derived in the wrong order.
        for (let i = 1; i < records.length; i++) {
          if (records[i - 1]!.id >= records[i]!.id) { records.sort((x, y) => x.id - y.id); break }
        }
        for (const record of records) entries.push(record)
        if (Number(page[1]) < this.pageRows) break
        after = Number(page[0])
      }
      segments.push(entries)
      if (c.parent === undefined) break
      upper = Math.min(upper, c.parent.at)
      c = this.conversation(c.parent.conversationId)
    }
    return segments.reverse().flat()
  }
}
