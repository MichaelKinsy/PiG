import assert from "node:assert/strict"
import { test } from "node:test"
import type { CoreClient } from "../src/client.ts"
import { track, type Tracker } from "@earendil-works/chord/delta"
import { forgetSnapshots, rememberSnapshot, settleRevision, shareSnapshot, shareStructure, snapshotStamp, type Revision } from "../src/facade/snapshots.ts"

test("an unchanged document value is the very object the previous read returned", () => {
  const first = { items: ["a", "b"], nested: { count: 0 } }
  assert.equal(shareStructure(first, structuredClone(first)), first)
})

test("a changed revision keeps every unchanged subtree of the previous one", () => {
  const first = { items: ["a", "b"], other: { label: "x" }, nested: { count: 0 } }
  const second = shareStructure(first, { items: ["a", "b"], other: { label: "x" }, nested: { count: 1 } }) as typeof first
  assert.notEqual(second, first)
  assert.equal(second.items, first.items)
  assert.equal(second.other, first.other)
  assert.equal(first.nested.count, 0)
  assert.equal(second.nested.count, 1)
})

test("arrays of different length and values of a different shape are replaced, not shared", () => {
  const first = { items: ["a"], kind: { a: 1 } as unknown }
  const second = shareStructure(first, { items: ["a", "b"], kind: [1] }) as typeof first
  assert.deepEqual(second.items, ["a", "b"])
  assert.deepEqual(second.kind, [1])
})

test("a read shares with the latest value of its address, a watch's newer revision becomes the latest, and unloading forgets it", () => {
  const client = {} as CoreClient
  const def = { kind: "test.live" }
  const read = shareSnapshot(client, [1, "k"], def, { n: 1 })
  assert.equal(shareSnapshot(client, [1, "k"], def, { n: 1 }), read)
  assert.notEqual(shareSnapshot(client, [2, "k"], def, { n: 1 }), read)
  const watched = { n: 2 }
  rememberSnapshot(client, [1, "k"], def, watched)
  assert.equal(shareSnapshot(client, [1, "k"], def, { n: 2 }), watched)
  forgetSnapshots(client)
  assert.notEqual(shareSnapshot(client, [1, "k"], def, { n: 2 }), watched)
})

/** One commit of a document the way a transaction does it: the draft's prepared revision, staged, then settled. */
function commit(client: CoreClient, address: unknown, def: { kind: string }, tracker: Tracker<any>, edit: (draft: any) => void, committed = true, since = snapshotStamp(client)) {
  const change = tracker.beginChange()
  edit(change.state)
  const prepared = change.prepare()
  const revision: Revision = { address, addressId: JSON.stringify(address), definition: def, id: 7, value: prepared.value, ops: prepared.ops, adopt: true, tracker, prepared }
  settleRevision(client, revision, committed, since)
  return revision
}

test("a committed revision is the document's next snapshot, sharing its unchanged subtrees with the previous one", () => {
  const client = {} as CoreClient
  const def = { kind: "test.live" }
  const tracker = track({ items: ["a", "b"], other: { x: 1 }, count: 0 })
  const before = shareSnapshot(client, [1], def, tracker.value) as { items: string[]; other: object; count: number }
  const revision = commit(client, [1], def, tracker, draft => { draft.count = 1 })
  const after = shareSnapshot(client, [1], def, { items: ["a", "b"], other: { x: 1 }, count: 1 }) as typeof before
  assert.equal(after, revision.value, "the read returns the revision the tracker adopted")
  assert.equal(tracker.value, revision.value)
  assert.equal(after.items, before.items)
  assert.equal(after.other, before.other)
  assert.equal(before.count, 0)
})

test("a failed commit leaves the snapshot and the tracker as they were", () => {
  const client = {} as CoreClient
  const def = { kind: "test.live" }
  const tracker = track({ items: ["a"] })
  const before = shareSnapshot(client, [1], def, tracker.value)
  commit(client, [1], def, tracker, draft => { draft.items.push("b") }, false)
  assert.equal(tracker.value, before)
  assert.equal(shareSnapshot(client, [1], def, { items: ["a"] }), before)
  const revision = commit(client, [1], def, tracker, draft => { draft.items.push("c") })
  assert.equal(shareSnapshot(client, [1], def, { items: ["a", "c"] }), revision.value)
})

test("a watch frame that arrived after the commit started is already the revision: settling does not replace it", () => {
  const client = {} as CoreClient
  const def = { kind: "test.live" }
  const tracker = track({ n: 1 })
  shareSnapshot(client, [1], def, tracker.value)
  const since = snapshotStamp(client)
  const frame = { n: 2 }
  rememberSnapshot(client, [1], def, frame)
  commit(client, [1], def, tracker, draft => { draft.n = 2 }, true, since)
  assert.equal(shareSnapshot(client, [1], def, { n: 2 }), frame)
  commit(client, [1], def, tracker, draft => { draft.n = 3 }, true, snapshotStamp(client))
  assert.deepEqual(shareSnapshot(client, [1], def, { n: 3 }), { n: 3 })
})

test("a frame with the content of the latest snapshot leaves its object the latest", () => {
  const client = {} as CoreClient
  const def = { kind: "test.live" }
  const read = shareSnapshot(client, [1], def, { n: 1 })
  rememberSnapshot(client, [1], def, { n: 1 })
  assert.equal(shareSnapshot(client, [1], def, { n: 1 }), read)
})
