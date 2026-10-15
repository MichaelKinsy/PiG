import type { CoreClient } from "../client.ts"

// A commit's publication and the frames of the document watches it advances are one revision in Pi (session.ts #publish: commit
// listeners and observers get the same `value` and `ops` objects). The core publishes the commit's `changes` and, in later notices
// of the same step, one frame per watch; each notice is decoded on its own, so the host keeps the latest publications by commit
// sequence and gives a frame the objects of the change it repeats.

type DocumentChange = { type?: string; value?: unknown; ops?: unknown }
const KEPT = 8
const publications = new WeakMap<CoreClient, Map<number, DocumentChange[]>>()

/** The complete publication of a commit (the `changes` of its `published` notice). */
export function recordPublication(client: CoreClient, seq: number, changes: readonly unknown[]): void {
  const kept = publications.get(client) ?? publications.set(client, new Map()).get(client)!
  kept.set(seq, (changes as DocumentChange[]).filter(change => change.type === "document"))
  if (kept.size > KEPT) kept.delete(kept.keys().next().value as number)
}

/** The change of the newest publication that a frame repeats: the same operations and the same value. A frame's notice names no commit, and its commit's publication precedes it. */
export function sharedChange(client: CoreClient, frame: { value?: unknown; ops: readonly unknown[] }): DocumentChange | undefined {
  const kept = publications.get(client)
  if (kept === undefined) return undefined
  const ops = JSON.stringify(frame.ops)
  const value = frame.value === undefined ? undefined : JSON.stringify(frame.value)
  for (const changes of [...kept.values()].reverse()) {
    const found = changes.find(change => JSON.stringify(change.ops) === ops && (value === undefined || JSON.stringify(change.value) === value))
    if (found !== undefined) return found
  }
  return undefined
}
