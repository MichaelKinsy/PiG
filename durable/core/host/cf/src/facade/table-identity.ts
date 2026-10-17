import type { CoreClient } from "../client.ts"
import { sameContent } from "./snapshots.ts"

type Change = { type: string; value?: unknown }
type Write = { type: string; value?: unknown }

const handed = new WeakMap<CoreClient, object[]>()
const admitted = new WeakMap<CoreClient, Write[]>()

/** A table record a transaction handed its callback (createConversation, appendEntry): the commit's publication carries that very object. */
export function handRecord(client: CoreClient, record: object): void {
  const list = handed.get(client) ?? handed.set(client, []).get(client)!
  if (list.length < 256) list.push(record)
}

/** The writes the Storage just admitted: their commit's publication carries these very objects. */
export function admitWrites(client: CoreClient, writes: unknown[]): void { admitted.set(client, writes as Write[]) }

/**
 * Pi publishes what it wrote (session.ts): a table change (conversation, entry, task, submission) is the very write the Storage was
 * given, and its value is the record the callback holds. The core publishes decoded copies; each copy that equals the write (or the
 * record handed out) is replaced by that object. Document changes are the revisions' (snapshots.ts substitutePublished).
 */
export function adoptTables(client: CoreClient, changes: Change[]): void {
  const writes = [...(admitted.get(client) ?? [])]
  const records = handed.get(client) ?? []
  admitted.delete(client)
  handed.delete(client)
  for (let i = 0; i < changes.length; i++) {
    const change = changes[i]!
    if (change.type.startsWith("document")) continue
    const at = writes.findIndex(w => w.type === change.type && sameContent(w, change))
    const write = at >= 0 ? writes.splice(at, 1)[0]! : undefined
    const record = typeof change.value === "object" && change.value !== null ? records.find(r => sameContent(r, change.value)) : undefined
    if (write !== undefined) { if (record !== undefined) write.value = record; changes[i] = write as Change } else if (record !== undefined) change.value = record
  }
}
