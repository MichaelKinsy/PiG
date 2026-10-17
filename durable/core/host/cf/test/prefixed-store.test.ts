// A store whose table names are renamed by the adapter must reopen. The Cloudflare Agents SDK's PiHarness (agents 0.28.0,
// harness/pi session-store) runs pi-durable over a Durable Object's database shared with the SDK's own tables: its adapter
// prefixes every table and index name Pi's migrations create (default `pi_`) in each SQL text, outside '...' string literals.
// Pi detects its schema by identifiers only (storage/sqlite/migrations.ts applySqliteMigrations: CREATE TABLE IF NOT EXISTS
// durable_schema, INSERT OR IGNORE, SELECT version), so Pi reopens such a store. The workload commits, closes, reopens,
// commits again and reopens again, once on Pi and once on PiG, over the same rewriting adapter.
import assert from "node:assert/strict"
import { existsSync, readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import * as pi from "@earendil-works/pi-durable"
import { SQLITE_MIGRATIONS, SqliteStorage, type SqliteDatabase, type SqliteExecutor } from "@earendil-works/pi-durable/storage/sqlite"
import { openNodeSqliteDatabase } from "@earendil-works/pi-durable/storage/sqlite/node"
import * as pig from "../src/facade/index.ts"
import { Harness as PigHarness } from "../src/facade/harness.ts"
import type { Storage } from "../src/facade/storage.ts"
import type { Store } from "../src/store.ts"
import { nodeStore } from "../src/store-node.ts"
import { WasmCore } from "../src/wasm.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const wasm = join(root, "dist", `core-${toolchain}.wasm`)
const context = BACKGROUND_CONTEXT
const noModels = { getModel: () => undefined, streamSimple: () => { throw new Error("a Session has no models") } }

/** The adapter's rename: `durable_schema` and every name a migration creates, on word boundaries, outside string literals. */
function prefixer(prefix: string) {
  const names = new Set(["durable_schema"])
  const create = /\bCREATE\s+(?:UNIQUE\s+)?(?:TABLE|INDEX)\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)/gi
  for (const migration of SQLITE_MIGRATIONS) for (const statement of migration.statements) for (const match of statement.matchAll(create)) names.add(match[1]!)
  const pattern = new RegExp(`\\b(${[...names].join("|")})\\b`, "g")
  const rewritten = { count: 0 }
  const rewrite = (sql: string) => {
    const out = sql.split(/('(?:[^']|'')*')/).map((part, i) => i % 2 === 1 ? part : part.replace(pattern, name => `${prefix}${name}`)).join("")
    if (out !== sql) rewritten.count++
    return out
  }
  return { rewrite, rewritten, names }
}

/** Pi's SqliteDatabase with every SQL text renamed. */
function prefixedDatabase(db: SqliteDatabase, rewrite: (sql: string) => string): SqliteDatabase {
  const executor = (x: SqliteExecutor): SqliteExecutor => ({
    exec: sql => x.exec(rewrite(sql)),
    run: (sql, ...params) => x.run(rewrite(sql), ...params),
    get: (sql, ...params) => x.get(rewrite(sql), ...params),
    all: (sql, ...params) => x.all(rewrite(sql), ...params),
  })
  return { ...executor(db), transaction: callback => db.transaction(tx => callback(executor(tx))), close: () => db.close() }
}

/** PiG's synchronous Store with every SQL text renamed. */
function prefixedStore(store: Store & { close(): void }, rewrite: (sql: string) => string): Store & { close(): void } {
  return {
    counters: store.counters,
    run: (sql, params) => store.run(rewrite(sql), params),
    all: (sql, params) => store.all(rewrite(sql), params),
    tx: work => store.tx(work),
    close: () => store.close(),
  }
}

type Api = { Harness: { open(storage: unknown, options: unknown, context: unknown): Promise<any> }; createRegistry(): unknown; defineDoc: typeof pi.defineDoc }

/** Commit, close and reopen twice; returns the document's items after each open. */
async function reopenTwice(api: Api, open: () => Promise<unknown>, options: Record<string, unknown>) {
  const Live = api.defineDoc<{ items: number[] }>({ kind: "prefixed.live", version: 1, scope: "conversation", history: "latest", fork: "initial", initial: () => ({ items: [] }) })
  const registry = api.createRegistry()
  const seen: (number[] | undefined)[] = []
  let id: unknown
  for (let round = 0; round < 3; round++) {
    const h = await api.Harness.open(await open(), { models: noModels, registry, ...options }, context)
    try {
      if (id === undefined) id = await h.commit(async (tx: any) => (await tx.createConversation({ ownership: { kind: "ownerless" } })).id, context)
      else seen.push((await h.snapshot(Live, id, context))?.items)
      await h.commit(async (tx: any) => { (await tx.doc(Live, id)).items.push(round) }, context)
    } finally {
      await h.close(context)
    }
  }
  return seen
}

test("Pi reopens a store whose tables the adapter renames (the reference for the PiG case)", async () => {
  const dir = tempDir("prefixed-pi")
  try {
    const p = prefixer("pi_")
    const path = join(dir, "pi.sqlite")
    const open = async () => SqliteStorage.open(prefixedDatabase(await openNodeSqliteDatabase(path), p.rewrite))
    const seen = await reopenTwice(pi as never, open, {})
    assert.deepEqual(seen, [[0], [0, 1]])
    assert.ok(p.rewritten.count > 0, "the adapter renamed Pi's SQL")
    assertOnlyPrefixedTables(path, p.names)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

test("PiG reopens a store whose tables the adapter renames", { skip: !existsSync(wasm) }, async () => {
  const module = new (WebAssembly.Module as any)(readFileSync(wasm)) as WebAssembly.Module
  const dir = tempDir("prefixed-pig")
  try {
    const p = prefixer("pi_")
    const path = join(dir, "pig.sqlite")
    const open = async (): Promise<Storage> => {
      const store = prefixedStore(nodeStore(path), p.rewrite)
      return { open: () => store, close: () => store.close(), profile: "node-sqlite" }
    }
    const seen = await reopenTwice({ ...pig, Harness: PigHarness } as never, open, { mode: "session", core: new WasmCore(module) })
    assert.deepEqual(seen, [[0], [0, 1]])
    assertOnlyPrefixedTables(path, p.names)
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

/** Every Pi table in the file carries the prefix; none exists under its own name. */
function assertOnlyPrefixedTables(path: string, names: Set<string>) {
  const store = nodeStore(path, { wal: false })
  try {
    const tables = store.all("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name", []).map(r => String(r[0]))
    assert.deepEqual(tables.filter(t => names.has(t)), [], `unprefixed Pi tables: ${tables.join(", ")}`)
    assert.ok(tables.includes("pi_durable_schema") && tables.includes("pi_entries"), `prefixed tables: ${tables.join(", ")}`)
  } finally { store.close() }
}
