// The conformance gate's store hook (durable/contract hooks/register.mjs): storage handles open their stores through
// globalThis[Symbol.for("pig.contract")].openStore so the gate captures every statement.
import assert from "node:assert/strict"
import { afterEach, test } from "node:test"
import { MemoryStorage, openNodeSqliteStorage } from "../src/facade/storage.ts"
import { openNodeJsonlStorage } from "../src/facade/storage/jsonl/node.ts"

const key = Symbol.for("pig.contract")
const g = globalThis as Record<symbol, unknown>
afterEach(() => { delete g[key] })

function install() {
  const opened: string[] = []
  const closed: string[] = []
  g[key] = {
    openStore: async (path: string) => {
      opened.push(path)
      return { run: () => 0, all: () => [], tx: (work: () => void) => work(), close: () => { closed.push(path) } }
    },
  }
  return { opened, closed }
}

test("MemoryStorage opens a distinct captured store per instance and keeps it across a close (memory.ts: a reopen finds what was committed)", async () => {
  const { opened, closed } = install()
  const a = new MemoryStorage(), b = new MemoryStorage()
  const sa = await a.open()
  assert.equal(await a.open(), sa, "one store per storage")
  await b.open()
  assert.equal(new Set(opened).size, 2)
  assert.ok(opened.every(p => p.startsWith(":memory:")))
  assert.equal(sa.counters.statements, 0, "the captured store carries counters")
  a.close()
  assert.deepEqual(closed, [], "a closed MemoryStorage keeps its rows for a reopen")
  assert.equal(await a.open(), sa, "the reopened storage is the same store")
})

test("openNodeSqliteStorage and openNodeJsonlStorage name the path the gate maps to a store file", async () => {
  const { opened } = install()
  await openNodeSqliteStorage("/data/a.sqlite").open()
  await ((await openNodeJsonlStorage("/data/jsonl/")) as unknown as { open(): Promise<unknown> }).open()
  assert.deepEqual(opened, ["/data/a.sqlite", "/data/jsonl/jsonl-as-sqlite"])
})

test("without the gate's hook the storages open node:sqlite stores", async () => {
  const store = await new MemoryStorage().open()
  assert.equal(store.run("CREATE TABLE t (a)", []), 0)
  store.tx(() => { store.run("INSERT INTO t VALUES (1)", []) })
  assert.deepEqual(store.all("SELECT a FROM t", []), [[1]])
  assert.equal(store.counters.commits, 1)
})
