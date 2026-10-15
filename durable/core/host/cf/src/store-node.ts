// A Store over node:sqlite, for hosts outside a Durable Object and for the contract tools.
import { DatabaseSync } from "node:sqlite"
import { countRead, countWrite, zeroCounters, type Store } from "./store.ts"
import type { Param } from "./wire.ts"

type Statement = ReturnType<DatabaseSync["prepare"]>

export function nodeStore(path: string, options: { wal?: boolean } = {}): Store & { db: DatabaseSync; close(): void } {
  const db = new DatabaseSync(path)
  if (options.wal ?? true) db.exec("PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL")
  const cache = new Map<string, Statement>()
  const stmt = (sql: string) => cache.get(sql) ?? cache.set(sql, db.prepare(sql)).get(sql)!
  const counters = zeroCounters()
  return {
    db,
    counters,
    run(sql, params) {
      const t0 = performance.now()
      counters.statements++
      countWrite(counters, params)
      const result = params.length ? stmt(sql).run(...(params as (string | number | null | Uint8Array)[])) : (db.exec(sql), { changes: sqlChanges(db) })
      counters.rowsWritten += Number(result.changes)
      counters.ms += performance.now() - t0
      return Number(result.changes)
    },
    all(sql, params) {
      const t0 = performance.now()
      counters.statements++
      const s = stmt(sql)
      s.setReturnArrays(true)
      const rows = s.all(...(params as (string | number | null | Uint8Array)[])) as unknown as unknown[][]
      counters.rowsRead += rows.length
      countRead(counters, rows)
      counters.ms += performance.now() - t0
      return rows
    },
    tx(work) {
      db.exec("BEGIN IMMEDIATE")
      try { work() } catch (e) { db.exec("ROLLBACK"); throw e }
      db.exec("COMMIT")
      counters.commits++
    },
    close() { cache.clear(); db.close() },
  }
}

function sqlChanges(db: DatabaseSync) {
  return (db.prepare("SELECT changes() AS n").get() as { n: number }).n
}
