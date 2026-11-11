import assert from "node:assert/strict"
import { mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { test } from "node:test"
import { nodeStore } from "../src/store-node.ts"

// storage/jsonl/storage.ts readOnly (Pi f5f4a71af, #11939; test/harness-read-only.test.ts "opens a JSONL session with work running elsewhere without
// writing"): the facade keeps a JSONL directory's state in one WAL SQLite file, and a SQLite reader writes the directory's shared-memory index, so
// the read-only open reads a private copy of what the writer committed and leaves every byte of the directory alone.
const bytes = (directory: string) => Object.fromEntries(readdirSync(directory).sort().map(name => [name, readFileSync(join(directory, name), "base64")]))

test("a read-only store holds the committed state and writes nothing to the directory", () => {
  const directory = mkdtempSync(join(tmpdir(), "pig-ro-test-"))
  try {
    const writer = nodeStore(join(directory, "jsonl-as-sqlite"))
    writer.run("CREATE TABLE t (a INTEGER)", [])
    writer.run("INSERT INTO t VALUES (?)", [1])
    const before = bytes(directory)

    const viewer = nodeStore(join(directory, "jsonl-as-sqlite"), { readOnly: true })
    assert.deepEqual(viewer.all("SELECT a FROM t", []), [[1]])
    writer.run("INSERT INTO t VALUES (?)", [2])
    assert.deepEqual(viewer.all("SELECT a FROM t", []), [[1]], "the state committed at open")
    viewer.close()
    writer.run("DELETE FROM t WHERE a = 2", [])
    const again = bytes(directory)
    writer.close()
    assert.notDeepEqual(again, before)

    const second = nodeStore(join(directory, "jsonl-as-sqlite"), { readOnly: true })
    const quiet = bytes(directory)
    assert.deepEqual(second.all("SELECT a FROM t", []), [[1]])
    second.close()
    assert.deepEqual(bytes(directory), quiet)
  } finally {
    rmSync(directory, { recursive: true, force: true })
  }
})
