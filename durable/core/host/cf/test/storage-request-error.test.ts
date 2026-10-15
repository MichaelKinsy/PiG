import assert from "node:assert/strict"
import { test } from "node:test"
import { StorageRequestError } from "../src/errors.ts"
import { StorageReads } from "../src/facade/storage-reads.ts"

// storage/scan.ts scanStart and storage/sqlite/storage.ts (Pi 42a3497d and later): a read the caller's request got wrong throws StorageRequestError, which
// fails only that read (facade harness storageReadFailed leaves the Session open for exactly this class).
const reads = new StorageReads({ all: () => [] } as never)

test("an invalid read request throws StorageRequestError", () => {
  const cases: Record<string, () => unknown> = {
    "an invalid scan order": () => reads.scanConversations({ order: "sideways" as never }, 5, undefined),
    "a cursor without an ID": () => reads.scanConversations({}, 5, { after: "x" } as never),
    "a cursor with an invalid order": () => reads.scanConversations({}, 5, { after: 3, order: "up" } as never),
    "a cursor of the other order": () => reads.scanConversations({ order: "descending" }, 5, { after: 3, order: "ascending" }),
    "entry without an ID": () => reads.entry(undefined, {}),
    "entry of an unknown conversation": () => reads.entry(9, 1, {}),
    "the head marker of an unknown conversation": () => reads.findLatestHeadMarker(9, undefined),
    "a scan of an unknown conversation": () => reads.scanEntries({ conversationId: 9 }, 5, undefined),
  }
  for (const [name, call] of Object.entries(cases)) {
    assert.throws(call, error => error instanceof StorageRequestError, name)
  }
})
