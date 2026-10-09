// Step and event codecs (ABI sections 4.3-4.5). Values are JS values: null, number (i64 and f64), string (text),
// Uint8Array (blob). Statement text lives in the core's sql_table.
import { STATUS } from "./abi.ts"

export type Param = null | number | string | Uint8Array
export type Stmt = { sql: number; params: Param[] }
export type Commit = { seq: number; stmts: Stmt[] }
export type Read = { id: number; sql: number; params: Param[] }
export type Notice = { kind: number; payload: Uint8Array }
export type Effect = { id: number; kind: number; payload: Uint8Array }
export type Step = {
  status: number
  commits: Commit[]
  reads: Read[]
  notices: Notice[]
  effects: Effect[]
  error?: { name?: string; message?: string; raw: string }
  bytes: number
  /** The step's bytes, kept only when the session asked for them. */
  raw?: Uint8Array
}
/** One answered read: columns by row. Blob columns may be Uint8Array or ArrayBuffer. */
export type Rows = { id: number; cols: number; rows: unknown[][] }

const dec = new TextDecoder()
const enc = new TextEncoder()

/** Decode a step from `view`, which is the step's bytes (including the size prefix). Nothing aliases the view afterwards. */
export function decodeStep(mem: Uint8Array, at: number, length: number): Step {
  const dv = new DataView(mem.buffer, mem.byteOffset + at, length)
  const size = dv.getUint32(0, true)
  if (size + 4 !== length) throw new Error(`step size ${size} does not match the ${length} bytes the core returned`)
  const status = dv.getUint8(4)
  const nCommits = dv.getUint16(5, true), nReads = dv.getUint16(7, true), nNotices = dv.getUint16(9, true), nEffects = dv.getUint16(11, true)
  let off = 13
  const base = at
  const value = (): Param => {
    const tag = dv.getUint8(off++)
    switch (tag) {
      case 0: return null
      case 1: { const v = dv.getBigInt64(off, true); off += 8; if (v > 9007199254740991n || v < -9007199254740991n) throw new Error(`i64 ${v} is outside the safe integer range`); return Number(v) }
      case 2: { const v = dv.getFloat64(off, true); off += 8; return v }
      case 3: { const n = dv.getUint32(off, true); off += 4; const s = dec.decode(mem.subarray(base + off, base + off + n)); off += n; return s }
      case 4: { const n = dv.getUint32(off, true); off += 4; const b = mem.slice(base + off, base + off + n); off += n; return b }
      default: throw new Error(`unknown value tag ${tag}`)
    }
  }
  const params = (): Param[] => {
    const n = dv.getUint8(off++)
    const out: Param[] = new Array(n)
    for (let i = 0; i < n; i++) out[i] = value()
    return out
  }
  const commits: Commit[] = new Array(nCommits)
  for (let c = 0; c < nCommits; c++) {
    const len = dv.getUint32(off, true); const end = off + 4 + len
    const seq = Number(dv.getBigInt64(off + 4, true))
    const n = dv.getUint16(off + 12, true)
    off += 14
    const stmts: Stmt[] = new Array(n)
    for (let s = 0; s < n; s++) { const sql = dv.getUint16(off, true); off += 2; stmts[s] = { sql, params: params() } }
    if (off !== end) throw new Error(`commit ${c} is ${end - off} bytes off its length`)
    commits[c] = { seq, stmts }
  }
  const reads: Read[] = new Array(nReads)
  for (let r = 0; r < nReads; r++) { const id = dv.getUint32(off, true), sql = dv.getUint16(off + 4, true); off += 6; reads[r] = { id, sql, params: params() } }
  const notices: Notice[] = new Array(nNotices)
  for (let i = 0; i < nNotices; i++) { const kind = dv.getUint8(off); const n = dv.getUint32(off + 1, true); off += 5; notices[i] = { kind, payload: mem.slice(base + off, base + off + n) }; off += n }
  const effects: Effect[] = new Array(nEffects)
  for (let i = 0; i < nEffects; i++) { const id = dv.getUint32(off, true), kind = dv.getUint8(off + 4), n = dv.getUint32(off + 5, true); off += 9; effects[i] = { id, kind, payload: mem.slice(base + off, base + off + n) }; off += n }
  const step: Step = { status, commits, reads, notices, effects, bytes: length }
  if (status !== STATUS.ok) {
    const n = dv.getUint32(off, true); off += 4
    const raw = dec.decode(mem.subarray(base + off, base + off + n)); off += n
    let parsed: { name?: string; message?: string } = {}
    try { parsed = JSON.parse(raw) } catch { /* the raw text is reported */ }
    step.error = { ...parsed, raw }
  }
  if (off !== length) throw new Error(`step has ${length - off} trailing bytes`)
  return step
}

const isBlob = (v: unknown): v is Uint8Array | ArrayBuffer => v instanceof Uint8Array || v instanceof ArrayBuffer

type Cell = { tag: number; n?: number; text?: Uint8Array; blob?: Uint8Array }
function cellOf(v: unknown): Cell {
  if (v === null || v === undefined) return { tag: 0 }
  if (typeof v === "number") return Number.isInteger(v) && Math.abs(v) <= Number.MAX_SAFE_INTEGER ? { tag: 1, n: v } : { tag: 2, n: v }
  if (typeof v === "bigint") return { tag: 1, n: Number(v) }
  if (typeof v === "string") return { tag: 3, text: enc.encode(v) }
  if (isBlob(v)) return { tag: 4, blob: v instanceof Uint8Array ? v : new Uint8Array(v) }
  throw new Error(`cannot encode ${typeof v} as an ABI value`)
}

/** Encode a rows event (ABI section 4.5) into a buffer obtained from `reserve(size)`. Returns the number of bytes. */
export function encodeRows(answers: Rows[], reserve: (n: number) => Uint8Array): number {
  const cells = answers.map(a => a.rows.map(row => row.map(cellOf)))
  let size = 2
  for (let i = 0; i < answers.length; i++) {
    size += 9
    for (const row of cells[i]!) for (const c of row) size += c.tag === 0 ? 1 : c.tag <= 2 ? 9 : 5 + (c.text ?? c.blob)!.length
  }
  const out = reserve(size)
  const dv = new DataView(out.buffer, out.byteOffset, size)
  let off = 0
  dv.setUint16(off, answers.length, true); off += 2
  for (let i = 0; i < answers.length; i++) {
    const a = answers[i]!
    dv.setUint32(off, a.id, true); dv.setUint32(off + 4, a.rows.length, true); dv.setUint8(off + 8, a.cols); off += 9
    for (const row of cells[i]!) for (const c of row) {
      dv.setUint8(off++, c.tag)
      if (c.tag === 1) { dv.setBigInt64(off, BigInt(c.n!), true); off += 8 }
      else if (c.tag === 2) { dv.setFloat64(off, c.n!, true); off += 8 }
      else if (c.tag >= 3) { const b = (c.text ?? c.blob)!; dv.setUint32(off, b.length, true); off += 4; out.set(b, off); off += b.length }
    }
  }
  return size
}

/** An event payload: fixed little-endian fields (`u8` or `u32`) followed by a body (ABI section 5). */
export function payloadBytes(fixed: ["u8" | "u32", number][], body?: Uint8Array | string): Uint8Array {
  const bytes = typeof body === "string" ? enc.encode(body) : body ?? new Uint8Array(0)
  const out = new Uint8Array(fixed.reduce((n, [t]) => n + (t === "u32" ? 4 : 1), 0) + bytes.length)
  const dv = new DataView(out.buffer)
  let off = 0
  for (const [type, value] of fixed) { if (type === "u32") { dv.setUint32(off, value, true); off += 4 } else out[off++] = value }
  out.set(bytes, off)
  return out
}
