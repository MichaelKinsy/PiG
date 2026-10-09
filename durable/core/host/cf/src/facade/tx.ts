// The host side of `Tx` (pi-durable src/types.ts): every operation is one request on the extension transaction channel
// (ABI section 8). Document drafts are Chord trackers over the value the core returns; their Op[] go back before `end`.
// The core enforces ReadAfterWrite and every other §4 rule and answers each operation, so this proxy forwards and never
// decides.
import { copyJson, type JsonValue } from "@earendil-works/chord"
import { track, type Change, type Tracker } from "@earendil-works/chord/delta"
import type { TxChannel } from "../client.ts"
import { addressId, resolveAddress, wireAddress, type AnyDocToken, type DocumentAddress } from "./documents.ts"

type OpenDoc = { address: DocumentAddress; tracker: Tracker<any>; change: Change<any>; retire: boolean }

export class TxProxy {
  readonly channel: TxChannel
  private readonly docs = new Map<string, OpenDoc>()
  private readonly retired = new Set<string>()
  private readonly forward: (op: string, args?: Record<string, unknown>) => Promise<any>

  constructor(channel: TxChannel) {
    this.channel = channel
    // The core answers an absent record with null; pi-durable returns undefined.
    this.forward = async (op, args) => (await channel.op(op, args)) ?? undefined
  }

  // Table reads.
  conversation(id: number) { return this.forward("conversation", { id }) }
  entry(...args: unknown[]) {
    // entry(id) or entry(token, id): the token only types the data; the core checks the kind.
    const [first, second] = args
    return this.forward("entry", typeof first === "number" ? { id: first } : { kind: (first as { kind?: string })?.kind, id: second })
  }
  task(id: number) { return this.forward("task", { id }) }
  scanConversations(query: unknown, limit: number, cursor?: unknown) { return this.forward("scanConversations", { query, limit, cursor }) }
  scanEntries(query: unknown, limit: number, cursor?: unknown) { return this.forward("scanEntries", { query, limit, cursor }) }
  latestHeadMarker(conversationId: number) { return this.forward("latestHeadMarker", { conversationId }) }
  scanTasks(query: unknown, limit: number, cursor?: unknown) { return this.forward("scanTasks", { query, limit, cursor }) }
  submissionByRequest(conversationId: number, requestId: string) { return this.forward("submissionByRequest", { conversationId, requestId }) }

  // Table writes.
  createRootConversation() { return this.forward("createRootConversation") }
  createConversation(options: { ownership: unknown }) { return this.forward("createConversation", { ownership: options.ownership }) }
  forkConversation(parentConversationId: number, at: number, options: { ownership: unknown }) { return this.forward("forkConversation", { parentConversationId, at, ownership: options.ownership }) }
  appendEntry(...args: unknown[]) {
    // appendEntry(conversationId, draft) or appendEntry(token, conversationId, draft).
    if (typeof args[0] === "number") return this.forward("appendEntry", { conversationId: args[0], value: args[1] })
    const token = args[0] as { kind?: string; definition?: { kind?: string } }
    return this.forward("appendEntry", { kind: token.kind ?? token.definition?.kind, conversationId: args[1], value: args[2] })
  }
  /** `initial(input)` runs here, in the host: it is task-definition code (ABI section 8, createTask). */
  createTask(task: { definition: { name: string; version?: number; initial(input: unknown): unknown } }, input: unknown, options: unknown) {
    return this.forward("createTask", { task: task.definition.name, version: task.definition.version ?? 1, state: task.definition.initial(copyJson(input as JsonValue)), input, options })
  }
  createSubmission(create: unknown) { return this.forward("createSubmission", { create }) }
  settleSubmission(id: number, settlement: unknown): void { void this.forward("settleSubmission", { id, settlement }).catch(() => {}) }
  placeSubmission(id: number, entry: number): void { void this.forward("placeSubmission", { id, entry }).catch(() => {}) }

  // Documents.
  async doc(token: AnyDocToken, ...args: unknown[]) {
    const { definition } = token
    const resolved = resolveAddress(definition, args)
    const seed = definition.family === true ? args[resolved.nextArgument] : undefined
    const open = this.docs.get(resolved.id)
    if (open) return open.change.state
    const answer = await this.forward("doc", { address: wireAddress(resolved.address), version: definition.version, ...(seed === undefined ? {} : { seed }) })
    // A second call that raced the first returns the same draft.
    const raced = this.docs.get(resolved.id)
    if (raced) return raced.change.state
    const tracker = track(copyJson(answer.value as JsonValue) as object)
    const change = tracker.beginChange()
    this.docs.set(resolved.id, { address: resolved.address, tracker, change, retire: false })
    return change.state
  }

  async retireDoc(token: AnyDocToken, ...args: unknown[]) {
    const resolved = resolveAddress(token.definition, args)
    await this.forward("retireDoc", { address: wireAddress(resolved.address) })
    this.docs.delete(resolved.id)
    this.retired.add(resolved.id)
  }

  /** Send every changed draft's operations: called by the commit driver before `end`. */
  async flush(): Promise<void> {
    for (const [id, open] of [...this.docs]) {
      const prepared = open.change.prepare()
      if (prepared.ops.length > 0) {
        await this.forward("doc.write", { address: wireAddress(open.address), ops: prepared.ops, baseRevision: prepared.baseRevision })
      }
      prepared.abort()
      this.docs.delete(id)
    }
  }

  discard() {
    for (const open of this.docs.values()) open.change.abort()
    this.docs.clear()
  }

  static addressId = addressId
}

/** Run `change` as one Session commit: begin (waiting for the line), callback, flush drafts, end with the next task state. */
export async function runCommit<T>(
  begin: () => Promise<TxChannel>,
  change: (tx: TxProxy) => T | Promise<T>,
  interpret?: (result: T) => unknown,
): Promise<T> {
  const channel = await begin()
  const tx = new TxProxy(channel)
  let result: T
  try {
    result = await change(tx)
    await tx.flush()
  } catch (error) {
    tx.discard()
    await channel.abort(error)
    throw error
  }
  await channel.end(interpret ? interpret(result) : undefined)
  return result
}
