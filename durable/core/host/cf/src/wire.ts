// Step and event codecs (ABI sections 4.3-4.5). Values are JS values: null, number (i64 and f64), string (text),
// Uint8Array (blob). Statement text lives in the core's sql_table.
import { STATUS } from "./abi.ts"

export type Param = null | number | string | Uint8Array
export type Stmt = { sql: number; params: Param[] }
export type Commit = { seq: number; stmts: Stmt[] }
export type Read = { id: number; sql: number; params: Param[] }
/** `origin` tags the async context the host runs a notice's listeners or an effect's handler in (ABI section 5.4); 0: none. */
export type Notice = { kind: number; origin: number; payload: Uint8Array }
export type Effect = { id: number; kind: number; origin: number; payload: Uint8Array }
export type Step = {
  status: number
  commits: Commit[]
  reads: Read[]
  notices: Notice[]
  effects: Effect[]
  /** The notices and effects in Pi's synchronous order (ABI section 5.3), as `[region, index]` pairs: region 0 notice, 1 effect. Absent: all notices, then all effects. */
  order?: Array<[number, number]>
  error?: { name?: string; message?: string; raw: string }
  bytes: number
  /** The step's bytes, kept only when the session asked for them. */
  raw?: Uint8Array
}
/** One answered read: columns by row. Blob columns may be Uint8Array or ArrayBuffer. */
export type Rows = { id: number; cols: number; rows: unknown[][] }

const dec = new TextDecoder()
const enc = new TextEncoder()

/** Reads the ABI's values, parameter lists and commits from `mem` at `base`; `off` is the position within the `length` bytes from `base`. */
class WireReader {
  readonly mem: Uint8Array
  readonly base: number
  readonly dv: DataView
  off = 0
  constructor(mem: Uint8Array, base: number, length: number) { this.mem = mem; this.base = base; this.dv = new DataView(mem.buffer, mem.byteOffset + base, length) }
  value(): Param {
    const { dv, mem, base } = this
    const tag = dv.getUint8(this.off++)
    switch (tag) {
      case 0: return null
      case 1: { const v = dv.getBigInt64(this.off, true); this.off += 8; if (v > 9007199254740991n || v < -9007199254740991n) throw new Error(`i64 ${v} is outside the safe integer range`); return Number(v) }
      case 2: { const v = dv.getFloat64(this.off, true); this.off += 8; return v }
      case 3: { const n = dv.getUint32(this.off, true); this.off += 4; const s = dec.decode(mem.subarray(base + this.off, base + this.off + n)); this.off += n; return s }
      case 4: { const n = dv.getUint32(this.off, true); this.off += 4; const b = mem.slice(base + this.off, base + this.off + n); this.off += n; return b }
      default: throw new Error(`unknown value tag ${tag}`)
    }
  }
  params(): Param[] {
    const n = this.dv.getUint8(this.off++)
    const out: Param[] = new Array(n)
    for (let i = 0; i < n; i++) out[i] = this.value()
    return out
  }
  /** One commit: u32 length of the rest, i64 seq, u16 statement count, statements. */
  commit(): Commit {
    const { dv } = this
    const len = dv.getUint32(this.off, true); const end = this.off + 4 + len
    const seq = Number(dv.getBigInt64(this.off + 4, true))
    const n = dv.getUint16(this.off + 12, true)
    this.off += 14
    const stmts: Stmt[] = new Array(n)
    for (let s = 0; s < n; s++) { const sql = dv.getUint16(this.off, true); this.off += 2; stmts[s] = { sql, params: this.params() } }
    if (this.off !== end) throw new Error(`commit is ${end - this.off} bytes off its length`)
    return { seq, stmts }
  }
}

/** The payload of a hops effect (ABI section 5.3): `n` hops, then `hopped`; `commits` are applied after hop `at` (0 < at < n), none when at is 0. */
export type Hops = { n: number; at: number; commits: Commit[] }

/** Decode a hops payload: u32 n, u32 at, commit*. */
export function decodeHops(payload: Uint8Array): Hops {
  const r = new WireReader(payload, 0, payload.length)
  const n = r.dv.getUint32(0, true), at = r.dv.getUint32(4, true)
  r.off = 8
  const commits: Commit[] = []
  while (r.off < payload.length) commits.push(r.commit())
  if (n === 0 || (at === 0) !== (commits.length === 0) || (at !== 0 && at >= n)) throw new Error(`hops n=${n} at=${at} with ${commits.length} commits is malformed`)
  return { n, at, commits }
}

/** Decode a step from `view`, which is the step's bytes (including the size prefix). Nothing aliases the view afterwards. */
export function decodeStep(mem: Uint8Array, at: number, length: number): Step {
  const r = new WireReader(mem, at, length)
  const dv = r.dv
  const size = dv.getUint32(0, true)
  if (size + 4 !== length) throw new Error(`step size ${size} does not match the ${length} bytes the core returned`)
  const status = dv.getUint8(4)
  const nCommits = dv.getUint16(5, true), nReads = dv.getUint16(7, true), nNotices = dv.getUint16(9, true), nEffects = dv.getUint16(11, true)
  r.off = 13
  const base = at
  const commits: Commit[] = new Array(nCommits)
  for (let c = 0; c < nCommits; c++) commits[c] = r.commit()
  let off = r.off
  const reads: Read[] = new Array(nReads)
  for (let i = 0; i < nReads; i++) { const id = dv.getUint32(off, true), sql = dv.getUint16(off + 4, true); r.off = off + 6; reads[i] = { id, sql, params: r.params() }; off = r.off }
  const notices: Notice[] = new Array(nNotices)
  for (let i = 0; i < nNotices; i++) { const kind = dv.getUint8(off), origin = dv.getUint32(off + 1, true), n = dv.getUint32(off + 5, true); off += 9; notices[i] = { kind, origin, payload: mem.slice(base + off, base + off + n) }; off += n }
  const effects: Effect[] = new Array(nEffects)
  for (let i = 0; i < nEffects; i++) { const id = dv.getUint32(off, true), kind = dv.getUint8(off + 4), origin = dv.getUint32(off + 5, true), n = dv.getUint32(off + 9, true); off += 13; effects[i] = { id, kind, origin, payload: mem.slice(base + off, base + off + n) }; off += n }
  const nOrder = dv.getUint16(off, true); off += 2
  const order: Array<[number, number]> | undefined = nOrder === 0 ? undefined : new Array(nOrder)
  for (let i = 0; i < nOrder; i++) {
    const region = dv.getUint8(off), index = dv.getUint16(off + 1, true); off += 3
    if (region > 1 || index >= (region === 0 ? nNotices : nEffects)) throw new Error(`step order entry ${i} names a missing ${region === 0 ? "notice" : "effect"} ${index}`)
    order![i] = [region, index]
  }
  if (order && nOrder !== nNotices + nEffects) throw new Error(`step order lists ${nOrder} of ${nNotices + nEffects} notices and effects`)
  const step: Step = { status, commits, reads, notices, effects, bytes: length }
  if (order) step.order = order
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

/** Decode a pig.read request (ABI section 4.2): u16 sqlId, u8 nParams, value*. */
export function decodeReadRequest(mem: Uint8Array, at: number, length: number): { sql: number; params: Param[] } {
  const dv = new DataView(mem.buffer, mem.byteOffset + at, length)
  let off = 0
  const value = (): Param => {
    const tag = dv.getUint8(off++)
    switch (tag) {
      case 0: return null
      case 1: { const v = dv.getBigInt64(off, true); off += 8; return Number(v) }
      case 2: { const v = dv.getFloat64(off, true); off += 8; return v }
      case 3: { const n = dv.getUint32(off, true); off += 4; const s = dec.decode(mem.subarray(at + off, at + off + n)); off += n; return s }
      case 4: { const n = dv.getUint32(off, true); off += 4; const b = mem.slice(at + off, at + off + n); off += n; return b }
      default: throw new Error(`unknown value tag ${tag}`)
    }
  }
  const sql = dv.getUint16(0, true); off = 2
  const n = dv.getUint8(off++)
  const params: Param[] = new Array(n)
  for (let i = 0; i < n; i++) params[i] = value()
  if (off !== length) throw new Error(`read request of ${length} bytes has ${length - off} trailing bytes`)
  return { sql, params }
}

/** Encode a pig.read answer (ABI section 4.2): u32 nRows, u8 nCols, value{nRows*nCols}; one rows-event answer without its counts and ID. */
export function encodeAnswer(rows: unknown[][]): Uint8Array {
  let whole!: Uint8Array
  encodeRows([{ id: 0, cols: rows[0]?.length ?? 0, rows }], n => (whole = new Uint8Array(n)))
  return whole.subarray(6)
}

/** An event payload: fixed little-endian fields (`u8` or `u32`) followed by a body (ABI section 5). */
/** The payload of an event that is one little-endian u32 (a fresh buffer: a trace or a queued event may keep it). */
export function u32Payload(value: number): Uint8Array {
  const out = new Uint8Array(4)
  out[0] = value & 0xff
  out[1] = (value >>> 8) & 0xff
  out[2] = (value >>> 16) & 0xff
  out[3] = (value >>> 24) & 0xff
  return out
}

export function payloadBytes(fixed: ["u8" | "u32", number][], body?: Uint8Array | string): Uint8Array {
  const bytes = typeof body === "string" ? enc.encode(body) : body ?? new Uint8Array(0)
  const out = new Uint8Array(fixed.reduce((n, [t]) => n + (t === "u32" ? 4 : 1), 0) + bytes.length)
  const dv = new DataView(out.buffer)
  let off = 0
  for (const [type, value] of fixed) { if (type === "u32") { dv.setUint32(off, value, true); off += 4 } else out[off++] = value }
  out.set(bytes, off)
  return out
}
