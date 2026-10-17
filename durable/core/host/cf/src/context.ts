// Ports packages/durable/src/harness/context.ts (pi-durable main): the committed context of a conversation, derived from
// its visible entries within a head marker and a tail, extended incrementally by the entries appended after a kept range.
// The host builds the context the model request names (ABI section 10.2) with this: records are read as bytes, decoded
// once and parsed in V8, never listed through the core.

export type Message = { role: string; [key: string]: unknown }
export type ContextEdit = { target: number; action: "omit" | "replace"; messages?: Message[] }
export type EntryRecord = {
  id: number
  conversationId: number
  kind: string
  model?: Message[]
  head?: number
  edits?: ContextEdit[]
  [key: string]: unknown
}
export type HeadMarker = EntryRecord & { head: number }

/** Head marker and newest visible entry that fix one committed context range. */
export type ContextBounds = { readonly head: HeadMarker | undefined; readonly tail: number }

export type ContextView = {
  readonly head: HeadMarker | undefined
  readonly entries: readonly EntryRecord[]
  readonly contributions: readonly (readonly Message[])[]
  readonly messages: readonly Message[]
}

/** The entries scanned within one set of bounds, the view derived from them, and what extending it needs. */
export type ContextRange = {
  readonly bounds: ContextBounds
  readonly entries: readonly EntryRecord[]
  readonly view: ContextView
  /** Targets of the edits in `entries`. */
  readonly edited: ReadonlySet<number>
  /** `view.messages` from the contributed messages before the last assistant message. */
  readonly settled: readonly Message[]
  /** Contributed messages from the last assistant message on, before tool result ordering. */
  readonly open: readonly Message[]
  /** The baseline's system message; see `findBaseline()`. */
  readonly baseline: Baseline
  /**
   * The range was resumed from a stored checkpoint: `settled` and `open` are complete, but `entries`, `view.entries` and
   * `view.contributions` hold only the entries added after the checkpoint. Whatever needs the whole range derives it again.
   */
  readonly partial?: true
}

/** A baseline's system message, `null` when the range cannot have one, or `undefined` while a later entry may be one. */
export type Baseline = Message | null | undefined

/** What a context read needs of storage: the visible entries of a conversation (its own, and its fork ancestors' up to each fork point). */
export interface ContextSource {
  /** The newest visible entry id of the conversation, or undefined when it has none. */
  newest(conversationId: number): number | undefined
  /** Whether entry `id` is visible from the conversation. */
  visible(conversationId: number, id: number): boolean
  /** The newest head marker visible from the conversation with an id at or below `tail`. */
  latestHead(conversationId: number, tail: number): HeadMarker | undefined
  /** The visible entries with ids in [min, max] (no lower bound when min is undefined), oldest first. */
  scan(conversationId: number, min: number | undefined, max: number): EntryRecord[]
}

const EXCLUDED_STOP_REASONS: ReadonlySet<unknown> = new Set(["aborted", "error", "deferred"])
const MISSING_RESULT_TEXT = "Tool result unavailable: history ends before this call completed."

/** The bounds of the current context, or of the context cut off at the visible entry `at` (captureContextBounds). */
export function captureContextBounds(source: ContextSource, conversationId: number, at?: number): ContextBounds | undefined {
  let tail: number | undefined
  if (at === undefined) {
    tail = source.newest(conversationId)
    if (tail === undefined) return undefined
  } else {
    if (!source.visible(conversationId, at)) throw new Error(`Entry ${at} is not visible from conversation ${conversationId}`)
    tail = at
  }
  return { head: source.latestHead(conversationId, tail), tail }
}

const emptyView = (): ContextView => ({ head: undefined, entries: [], contributions: [], messages: [] })

/**
 * `readContext()` that reuses `previous`, an earlier range of the same conversation: with the same head marker, only
 * entries after its tail are scanned. Returns the view and the range to pass to the next read (readContextFrom).
 */
export function readContextFrom(
  source: ContextSource,
  conversationId: number,
  at: number | undefined,
  previous: ContextRange | undefined,
  owned = false,
): { view: ContextView; range: ContextRange | undefined } {
  const bounds = captureContextBounds(source, conversationId, at)
  if (bounds === undefined) return { view: emptyView(), range: undefined }
  const range = rangeWithin(bounds, previous, (min, max) => source.scan(conversationId, min, max), owned)
  const { view } = range
  return { view: { ...view, entries: [...view.entries], contributions: [...view.contributions], messages: [...view.messages] }, range }
}

/**
 * The range of `bounds`, reusing `previous`, an earlier range of the same conversation: with the same head marker, only
 * entries after its tail are scanned. `scan(min, max)` gives the visible entries with ids in [min, max], oldest first.
 * `owned` says the caller keeps no reference to `previous` once it has the new range, so an extension appends to the arrays
 * of `previous` instead of copying them: the work and the allocation of a request are those of the added entries, not of
 * the whole range. The range a caller keeps is then the only holder of its arrays; what it hands out it copies.
 */
export function rangeWithin(
  bounds: ContextBounds,
  previous: ContextRange | undefined,
  scan: (min: number | undefined, max: number) => EntryRecord[],
  owned = false,
): ContextRange {
  if (previous === undefined || previous.bounds.head?.id !== bounds.head?.id) return deriveRange(bounds, scan(bounds.head?.head, bounds.tail))
  if (bounds.tail === previous.bounds.tail) return previous
  if (bounds.tail < previous.bounds.tail) {
    return deriveRange(bounds, previous.partial ? scan(bounds.head?.head, bounds.tail) : previous.entries.filter((entry) => entry.id <= bounds.tail))
  }
  return extendRange(previous, bounds, scan(previous.bounds.tail + 1, bounds.tail), owned, () => scan(bounds.head?.head, bounds.tail))
}

/** The committed context of a conversation (readContext). */
export function readContext(source: ContextSource, conversationId: number, at?: number): ContextView {
  return readContextFrom(source, conversationId, at, undefined).view
}

/** Derive the context view from the entries scanned within `bounds`. */
function deriveRange(bounds: ContextBounds, entries: readonly EntryRecord[]): ContextRange {
  const edits = new Map<number, ContextEdit>()
  // Edits of every entry in the range count, including older head markers that `selectActive()` drops.
  for (const entry of entries) for (const edit of entry.edits ?? []) edits.set(edit.target, edit)
  const active = selectActive(bounds.head, entries)
  const contributions = active.map((entry) => contribute(entry, edits.get(entry.id)))
  const { settled, open } = settle([], contributions.flat())
  const baseline = findBaseline(bounds.head, active)
  const messages = leadWithSystem([...settled, ...orderToolResults(open)], baseline)
  const view = { head: bounds.head, entries: active, contributions, messages }
  return { bounds, entries, view, edited: new Set(edits.keys()), settled, open, baseline }
}

/**
 * `previous` extended by the entries `added` after its tail under the same head marker: only their contributions and
 * the open messages are derived. An added edit can change an earlier entry, so it derives the whole range again.
 * With `owned`, `previous`'s arrays are extended in place; everything that can throw is computed before the first append,
 * so a failed extension leaves `previous` as it was.
 */
function extendRange(previous: ContextRange, bounds: ContextBounds, added: readonly EntryRecord[], owned: boolean, whole: () => EntryRecord[]): ContextRange {
  const rederive = added.some((entry) => entry.edits !== undefined || entry.head !== undefined || previous.edited.has(entry.id))
  // A range resumed from a checkpoint does not hold the entries an edit or a new head marker needs: derive from storage.
  if (rederive && previous.partial) return deriveRange(bounds, whole())
  if (!owned) {
    const entries = [...previous.entries, ...added]
    if (rederive) return deriveRange(bounds, entries)
    const contributions = added.map((entry) => contribute(entry, undefined))
    const { settled, open } = settle(previous.settled, [...previous.open, ...contributions.flat()])
    const baseline = previous.baseline !== undefined ? previous.baseline : findBaseline(bounds.head, added)
    const view = {
      head: bounds.head,
      entries: [...previous.view.entries, ...added],
      contributions: [...previous.view.contributions, ...contributions],
      messages: leadWithSystem([...settled, ...orderToolResults(open)], baseline),
    }
    return { bounds, entries, view, edited: previous.edited, settled, open, baseline }
  }
  if (rederive) return deriveRange(bounds, [...previous.entries, ...added])
  const entries = previous.entries as EntryRecord[]
  const contributions = added.map((entry) => contribute(entry, undefined))
  const open = [...previous.open, ...contributions.flat()]
  const last = open.findLastIndex((message) => message.role === "assistant")
  const settledNow = last <= 0 ? [] : orderToolResults(open.slice(0, last))
  const nextOpen = last <= 0 ? open : open.slice(last)
  const ordered = orderToolResults(nextOpen)
  const baseline = previous.baseline !== undefined ? previous.baseline : findBaseline(bounds.head, added)
  // Nothing below can throw: append. A loop, not push(...added): a spread call throws past V8's argument limit (~125,000).
  const settled = previous.settled as Message[]
  for (const message of settledNow) settled.push(message)
  for (const entry of added) entries.push(entry)
  const viewEntries = previous.view.entries as EntryRecord[]
  const viewContributions = previous.view.contributions as (readonly Message[])[]
  // Without a head marker the view's entries are the range's own list (selectActive), which is already extended.
  if (viewEntries !== entries) for (const entry of added) viewEntries.push(entry)
  for (const contributed of contributions) viewContributions.push(contributed)
  const view = lazyView(bounds.head, viewEntries, viewContributions, settled, ordered, baseline)
  return { bounds, entries, view, edited: previous.edited, settled, open: nextOpen, baseline, ...(previous.partial ? { partial: true as const } : {}) }
}

/**
 * The range a stored checkpoint stands for: the head marker, the entry the settled messages run through (`tail`), the settled
 * messages and the open contributions after them. It extends like a derived range; it is `partial` (see ContextRange).
 */
export function rangeFromCheckpoint(head: HeadMarker | undefined, tail: number, settled: Message[], open: readonly Message[], baseline: Baseline): ContextRange {
  const entries: EntryRecord[] = []
  // Ordered now, as extendRange orders: open messages the ordering cannot read fail here, not at the first read of the messages.
  const ordered = orderToolResults(open)
  let messages: Message[] | undefined
  const view: ContextView = {
    head,
    entries,
    contributions: [],
    get messages() { return (messages ??= leadWithSystemInPlace([...settled, ...ordered], baseline)) },
  }
  return { bounds: { head, tail }, entries, view, edited: new Set(), settled, open, baseline, partial: true }
}

/**
 * Identifies the derivation: a checkpoint written by other code is not trusted. It is the hash of the source text of the
 * functions that derive a context, so a change to the algorithm changes it without anyone remembering to.
 */
export function derivationId(): string {
  const text = [rangeWithin, deriveRange, extendRange, selectActive, contribute, settle, orderToolResults, missingResult, leadWithSystem, leadWithSystemInPlace]
    .map((fn) => fn.toString()).join("\n") + [...EXCLUDED_STOP_REASONS].join(",") + MISSING_RESULT_TEXT
  let a = 0xdeadbeef, b = 0x41c6ce57
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i)
    a = Math.imul(a ^ c, 2654435761)
    b = Math.imul(b ^ c, 1597334677)
  }
  a = Math.imul(a ^ (a >>> 16), 2246822507) ^ Math.imul(b ^ (b >>> 13), 3266489909)
  b = Math.imul(b ^ (b >>> 16), 2246822507) ^ Math.imul(a ^ (a >>> 13), 3266489909)
  return `${(b >>> 0).toString(16).padStart(8, "0")}${(a >>> 0).toString(16).padStart(8, "0")}`
}

/**
 * A view whose messages are derived on first read. A separate function, so the getter's closure context holds only these
 * arrays: a getter created inside extendRange shares that function's context, which holds `previous` (captured by the
 * rederive test), and every kept view would then retain the range it extended, and so the whole chain of earlier ranges.
 */
function lazyView(head: HeadMarker | undefined, entries: readonly EntryRecord[], contributions: readonly (readonly Message[])[], settled: readonly Message[], ordered: readonly Message[], baseline: Baseline): ContextView {
  let messages: Message[] | undefined
  return { head, entries, contributions, get messages() { return (messages ??= leadWithSystemInPlace([...settled, ...ordered], baseline)) } }
}

/**
 * The baseline among `entries`, oldest first: the system message of the first `pi.system` entry after the head marker, or of
 * the transcript without one, when generation wrote it with every section and tool in effect. After a head marker it always
 * does, and omits the retained `pi.system` entries. Without one it does when it saw no system message before, which holds
 * unless an earlier entry carries one, even if an edit omits it later. An edit that targets the baseline contributes other
 * messages, or none, so none leads; a later entry does not take its place (Pi 5713c950).
 */
function findBaseline(head: HeadMarker | undefined, entries: readonly EntryRecord[]): Baseline {
  for (const entry of entries) {
    if (head !== undefined && entry.id <= head.id) continue
    if (entry.kind === "pi.system") return entry.model?.find((message) => message.role === "system") ?? null
    if (head === undefined && carriesSystemMessage(entry)) return null
  }
  return undefined
}

/** Whether an entry's own messages, or the replacement messages of its edits, include a system message. */
function carriesSystemMessage(entry: EntryRecord): boolean {
  const isSystem = (message: Message) => message.role === "system"
  return (entry.model?.some(isSystem) ?? false) || (entry.edits?.some((edit) => edit.action === "replace" && (edit.messages ?? []).some(isSystem)) ?? false)
}

/**
 * Move the first system message to the front when only user messages precede it, or when it is the baseline's (spec section 2.1,
 * rule 10). Only the first system message moves, so replaying system messages in order gives the same sections and tools;
 * any other system message after an assistant message stays where it was committed.
 */
function leadWithSystem(messages: Message[], baseline: Baseline): Message[] {
  const index = messages.findIndex((message) => message.role === "system")
  if (index <= 0) return messages
  const system = messages[index]!
  if (system !== baseline && messages.slice(0, index).some((message) => message.role !== "user")) return messages
  return [system, ...messages.slice(0, index), ...messages.slice(index + 1)]
}

/** leadWithSystem on a list the caller owns: the system message moves to the front without another copy. */
function leadWithSystemInPlace(messages: Message[], baseline: Baseline): Message[] {
  const index = messages.findIndex((message) => message.role === "system")
  if (index <= 0) return messages
  const system = messages[index]!
  if (system !== baseline) for (let i = 0; i < index; i++) if (messages[i]!.role !== "user") return messages
  messages.copyWithin(1, 0, index)
  messages[0] = system
  return messages
}

/** One active entry's model messages after its edit and excluded stop reasons, before tool result ordering. */
function contribute(entry: EntryRecord, edit: ContextEdit | undefined): readonly Message[] {
  if (edit?.action === "omit") return []
  const contributed = edit?.action === "replace" ? (edit.messages ?? []) : (entry.model ?? [])
  return contributed.filter((message) => message.role !== "assistant" || !EXCLUDED_STOP_REASONS.has(message.stopReason))
}

/**
 * Move the ordered messages before the last assistant message of `open` to `settled`. `orderToolResults()` of a
 * sequence equals the concatenation over its parts when each later part starts with an assistant message.
 */
function settle(settled: readonly Message[], open: readonly Message[]): { readonly settled: readonly Message[]; readonly open: readonly Message[] } {
  const last = open.findLastIndex((message) => message.role === "assistant")
  if (last <= 0) return { settled, open }
  return { settled: [...settled, ...orderToolResults(open.slice(0, last))], open: open.slice(last) }
}

/** The head marker followed by the range's non-head entries, or the whole range without a marker. */
function selectActive(head: HeadMarker | undefined, range: readonly EntryRecord[]): readonly EntryRecord[] {
  return head === undefined ? range : [head, ...range.filter((entry) => entry.head === undefined)]
}

type ToolCall = { type: "toolCall"; id: string; name: string }

/**
 * Place each assistant's tool results directly after it in call order. Results are taken from the messages before
 * the next assistant; a missing result is synthesized and unmatched results are dropped.
 */
export function orderToolResults(messages: readonly Message[]): Message[] {
  const ordered: Message[] = []
  for (let index = 0; index < messages.length; index++) {
    const message = messages[index]!
    if (message.role === "toolResult") continue
    ordered.push(message)
    if (message.role !== "assistant") continue
    const calls = (message.content as { type: string }[]).filter((content): content is ToolCall => content.type === "toolCall")
    if (calls.length === 0) continue
    const results = new Map<string, number>()
    for (let next = index + 1; next < messages.length && messages[next]!.role !== "assistant"; next++) {
      const candidate = messages[next]!
      if (candidate.role === "toolResult" && !results.has(candidate.toolCallId as string)) results.set(candidate.toolCallId as string, next)
    }
    for (const call of calls) {
      const resultIndex = results.get(call.id)
      ordered.push(resultIndex === undefined ? missingResult(call, message.timestamp as number) : messages[resultIndex]!)
    }
  }
  return ordered
}

function missingResult(call: ToolCall, timestamp: number): Message {
  return {
    role: "toolResult",
    toolCallId: call.id,
    toolName: call.name,
    content: [{ type: "text", text: MISSING_RESULT_TEXT }],
    isError: true,
    details: { reason: "missing_result" },
    timestamp,
  }
}
