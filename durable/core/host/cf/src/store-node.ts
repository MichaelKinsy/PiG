// A Store over node:sqlite, for hosts outside a Durable Object and for the contract tools.
import { copyFileSync, existsSync, mkdtempSync, rmSync, statSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { countRead, countWrite, zeroCounters, type Store } from "./store.ts"
import type { Param } from "./wire.ts"

type Statement = ReturnType<DatabaseSync["prepare"]>

/** Read-only opens retried after racing a writer's checkpoint (Pi f5f4a71af READ_ONLY_OPEN_ATTEMPTS). */
const READ_ONLY_OPEN_ATTEMPTS = 5

/**
 * The state a writer committed, as a private copy of its database and WAL in a temporary directory: SQLite readers write the
 * shared-memory index of the directory they read (read marks), and a read-only open must write nothing there (Pi f5f4a71af, #11939).
 * A copy that raced a checkpoint (the database changed while it was read) is taken again.
 */
function snapshotDatabase(path: string): { db: DatabaseSync; release(): void } {
  for (let attempt = 1; ; attempt++) {
    const directory = mkdtempSync(join(tmpdir(), "pig-readonly-"))
    const release = () => rmSync(directory, { recursive: true, force: true })
    try {
      const before = statSync(path)
      const copy = join(directory, "snapshot")
      copyFileSync(path, copy)
      if (existsSync(`${path}-wal`)) copyFileSync(`${path}-wal`, `${copy}-wal`)
      const after = statSync(path)
      if (after.size !== before.size || after.mtimeMs !== before.mtimeMs) throw new Error("the database changed during the copy")
      const db = new DatabaseSync(copy)
      db.prepare("SELECT count(*) FROM sqlite_master").get()
      return { db, release }
    } catch (error) {
      release()
      if (attempt === READ_ONLY_OPEN_ATTEMPTS) throw error
    }
  }
}

export function nodeStore(path: string, options: { wal?: boolean; readOnly?: boolean } = {}): Store & { db: DatabaseSync; close(): void } {
  const snapshot = options.readOnly === true ? snapshotDatabase(path) : undefined
  const db = snapshot?.db ?? new DatabaseSync(path)
  if (snapshot === undefined && (options.wal ?? true)) db.exec("PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL")
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
    close() { cache.clear(); db.close(); snapshot?.release() },
  }
}

function sqlChanges(db: DatabaseSync) {
  return (db.prepare("SELECT changes() AS n").get() as { n: number }).n
}
