// A Store over a Durable Object's synchronous SQL (`ctx.storage.sql`). rowsRead and rowsWritten are the cursor counters
// (billing units, index rows included).
import { countRead, countWrite, zeroCounters, type Store } from "./store.ts"
import type { Param } from "./wire.ts"

const bind = (params: Param[]) => params.map(p => p instanceof Uint8Array ? (p.byteOffset === 0 && p.byteLength === p.buffer.byteLength ? p.buffer : p.slice().buffer) : p)

export function doStore(storage: DurableObjectStorage): Store {
  const sql = storage.sql
  const counters = zeroCounters()
  return {
    counters,
    run(query, params) {
      const t0 = performance.now()
      counters.statements++
      countWrite(counters, params)
      const cursor = sql.exec(query, ...bind(params) as SqlStorageValue[])
      cursor.toArray()
      counters.rowsRead += cursor.rowsRead
      counters.rowsWritten += cursor.rowsWritten
      counters.ms += performance.now() - t0
      return cursor.rowsWritten
    },
    all(query, params) {
      const t0 = performance.now()
      counters.statements++
      const cursor = sql.exec(query, ...bind(params) as SqlStorageValue[])
      const rows = [...cursor.raw()] as unknown[][]
      counters.rowsRead += cursor.rowsRead
      counters.rowsWritten += cursor.rowsWritten
      countRead(counters, rows)
      counters.ms += performance.now() - t0
      return rows
    },
    tx(work) {
      storage.transactionSync(work)
      counters.commits++
    },
  }
}
