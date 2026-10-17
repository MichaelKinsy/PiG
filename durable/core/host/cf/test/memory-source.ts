import type { ContextSource, EntryRecord, HeadMarker } from "../src/context.ts"

// A ContextSource over in-memory conversations: the entries each owns and the fork parents, as pi-durable resolves visibility.
export type Conv = { id: number; parent?: { conversationId: number; at: number } }

export class MemorySource implements ContextSource {
  readonly convs = new Map<number, Conv>()
  readonly own = new Map<number, EntryRecord[]>()
  visibleOf(conv: number): EntryRecord[] {
    const segments: EntryRecord[][] = []
    let upper = Infinity
    for (let c = this.convs.get(conv); c !== undefined; c = c.parent ? this.convs.get(c.parent.conversationId) : undefined) {
      segments.push((this.own.get(c.id) ?? []).filter((e) => e.id <= upper))
      if (!c.parent) break
      upper = Math.min(upper, c.parent.at)
    }
    return segments.reverse().flat()
  }
  newest(conv: number) { return this.visibleOf(conv).at(-1)?.id }
  visible(conv: number, id: number) { return this.visibleOf(conv).some((e) => e.id === id) }
  latestHead(conv: number, tail: number) { return this.visibleOf(conv).filter((e) => e.id <= tail && e.head !== undefined).at(-1) as HeadMarker | undefined }
  scan(conv: number, min: number | undefined, max: number) { return this.visibleOf(conv).filter((e) => (min === undefined || e.id >= min) && e.id <= max) }
}

