// The message lists a host keeps for the delta form of model_context (ABI section 6, effect 3). A list is a whole conversation, so
// the count and the retained messages are bounded; ABI lets the host drop a list only once a later effect extends it or the
// conversation unloads, so a request that names its conversation protects that conversation's newest list from the bounds.
export const MAX_KEPT_LISTS = 1024
export const MAX_KEPT_MESSAGES = 16384

export class KeptLists {
  private readonly lists = new Map<number, unknown[]>()
  private readonly owner = new Map<number, number>()
  private readonly newest = new Map<number, number>()
  private messages = 0

  private readonly maxLists: number
  private readonly maxMessages: number
  constructor(maxLists = MAX_KEPT_LISTS, maxMessages = MAX_KEPT_MESSAGES) { this.maxLists = maxLists; this.maxMessages = maxMessages }

  get size() { return this.lists.size }
  get retainedMessages() { return this.messages }
  has(id: number) { return this.lists.has(id) }

  /** The list of an earlier effect, removed: the extending request keeps the grown list under its own ID. */
  take(id: number): unknown[] | undefined {
    const list = this.lists.get(id)
    if (list) this.forget(id)
    return list
  }

  /** Keep the newest list, then drop the oldest list that is neither the newest nor the newest of its conversation. */
  keep(id: number, list: unknown[], conversationId?: number) {
    this.lists.set(id, list)
    this.messages += list.length
    if (conversationId !== undefined) { this.owner.set(id, conversationId); this.newest.set(conversationId, id) }
    while (this.lists.size > 1 && (this.lists.size > this.maxLists || this.messages > this.maxMessages)) {
      let victim: number | undefined
      let position = 0
      for (const candidate of this.lists.keys()) {
        if (++position === this.lists.size) break
        const conversation = this.owner.get(candidate)
        if (conversation !== undefined && this.newest.get(conversation) === candidate) continue
        victim = candidate
        break
      }
      if (victim === undefined) return
      this.forget(victim)
    }
  }

  private forget(id: number) {
    this.messages -= this.lists.get(id)!.length
    this.lists.delete(id)
    const conversation = this.owner.get(id)
    if (conversation !== undefined) {
      if (this.newest.get(conversation) === id) this.newest.delete(conversation)
      this.owner.delete(id)
    }
  }
}
