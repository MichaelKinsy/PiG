// The synchronous SQL the host loop runs on (ABI section 3): the Durable Object's `ctx.storage.sql`, `node:sqlite`, or any
// store a harness supplies. Counters follow BAKEOFF metrics M6 and M7.
import type { Param } from "./wire.ts"

export type StoreCounters = {
  statements: number
  commits: number
  rowsRead: number
  rowsWritten: number
  /** Bytes of text and blob values returned by reads, and bound by writes. */
  bytesRead: number
  bytesWritten: number
  /** Milliseconds inside SQL. */
  ms: number
}
export const zeroCounters = (): StoreCounters => ({ statements: 0, commits: 0, rowsRead: 0, rowsWritten: 0, bytesRead: 0, bytesWritten: 0, ms: 0 })

export interface Store {
  /** Run one statement; returns the number of rows it wrote (Durable Object `rowsWritten`; SQLite `changes()`). */
  run(sql: string, params: Param[]): number
  /** Run one query; rows are arrays, BLOB columns arrive as Uint8Array or ArrayBuffer. */
  all(sql: string, params: Param[]): unknown[][]
  /** Run `work` in one transaction: commit when it returns, roll back when it throws. */
  tx(work: () => void): void
  counters: StoreCounters
}

const size = (v: unknown) => typeof v === "string" ? v.length : v instanceof Uint8Array ? v.length : v instanceof ArrayBuffer ? v.byteLength : 0

export function countRead(c: StoreCounters, rows: unknown[][]) {
  for (const row of rows) for (const v of row) c.bytesRead += size(v)
}
export function countWrite(c: StoreCounters, params: Param[]) {
  for (const p of params) c.bytesWritten += size(p)
}
