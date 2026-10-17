// KeptLists: ABI section 6 lets the host drop a kept list only once a later effect extends it or the conversation unloads.
import assert from "node:assert/strict"
import { test } from "node:test"
import { KeptLists } from "../src/kept.ts"

const list = (n: number) => Array.from({ length: n }, (_, i) => i)

test("kept: without conversation IDs the oldest lists go first and the newest stays", () => {
  const kept = new KeptLists(1024, 100)
  for (let id = 1; id <= 5; id++) kept.keep(id, list(40))
  assert.deepEqual([1, 2, 3, 4, 5].filter(id => kept.has(id)), [4, 5])
})

test("kept: the newest list of each conversation survives the bounds and can be extended", () => {
  const kept = new KeptLists(1024, 100)
  kept.keep(1, list(60), 1)
  kept.keep(2, list(60), 2)
  kept.keep(3, list(60), 1)
  assert.equal(kept.has(1), false, "conversation 1's superseded list is dropped")
  assert.equal(kept.has(2), true, "conversation 2's newest list is kept although the bounds are passed")
  assert.equal(kept.retainedMessages, 120)
  const base = kept.take(2)
  assert.equal(base?.length, 60)
  assert.equal(kept.has(2), false, "an extended list is replaced by the grown one")
})

test("kept: take of an evicted or unknown list is undefined", () => {
  const kept = new KeptLists(1024, 10)
  kept.keep(1, list(8))
  kept.keep(2, list(8))
  assert.equal(kept.take(1), undefined)
})
