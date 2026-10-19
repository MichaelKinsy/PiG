// The Durable core built natively, served by durable/core/cmd/corenative in a child process (Node only). The host talks to
// it over two FIFOs with blocking reads, so a step stays a synchronous call as it is for the Wasm builds (ABI section 4):
// the conformance gate runs the native build through the same facade. The request and response frames are documented in
// cmd/corenative.
import { uuidv7 } from "@earendil-works/pi-ai"
import { ABI_ID, EVENT } from "./abi.ts"
import { decodeReadRequest, decodeStep, encodeRows, type Param, type Rows, type Step } from "./wire.ts"
import { AbiMismatch, CoreTrap } from "./wasm.ts"

type Fs = typeof import("node:fs")
type ChildProcess = typeof import("node:child_process")
type Os = typeof import("node:os")
type Path = typeof import("node:path")

const dec = new TextDecoder()
const enc = new TextEncoder()

export interface NativeOptions {
  uuidv7?: () => string
  expectedAbiId?: string
}

export class NativeCore {
  readonly sqlTable: string[]
  readonly abiId: string
  /** Payload bytes that crossed the pipe since the core started, both ways (the count gate's bytesCrossed). */
  bytesCrossed = 0
  private readonly fs: Fs
  private readonly toCore: number
  private readonly fromCore: number
  private readonly makeUuid: () => string

  /** Start the native core. The node: modules are passed in so a Workers bundle never resolves them. */
  constructor(exe: string, mods: { fs: Fs; childProcess: ChildProcess; os: Os; path: Path }, options: NativeOptions = {}) {
    const { fs, childProcess, os, path } = mods
    this.fs = fs
    this.makeUuid = options.uuidv7 ?? (() => uuidv7())
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "corenative-"))
    const req = path.join(dir, "req"), res = path.join(dir, "res")
    childProcess.execFileSync("mkfifo", [req, res])
    const child = childProcess.spawn(exe, [req, res], { stdio: ["ignore", "inherit", "inherit"] })
    child.unref()
    // Opening a FIFO blocks until the other side opens it; the child opens req, then res.
    this.toCore = fs.openSync(req, "w")
    this.fromCore = fs.openSync(res, "r")
    fs.rmSync(dir, { recursive: true, force: true })
    this.abiId = dec.decode(this.call(1, 0, 0, 0))
    const expected = options.expectedAbiId ?? ABI_ID
    if (this.abiId !== expected) throw new AbiMismatch(`abi_id ${this.abiId} is not the host's ${expected}`)
    const table = this.call(2, 0, 0, 0)
    const dv = new DataView(table.buffer, table.byteOffset, table.byteLength)
    const n = dv.getUint16(0, true)
    let p = 2
    this.sqlTable = []
    for (let i = 0; i < n; i++) { const len = dv.getUint32(p, true); p += 4; this.sqlTable.push(dec.decode(table.subarray(p, p + len))); p += len }
  }

  private writeAll(bytes: Uint8Array) {
    let off = 0
    while (off < bytes.length) off += this.fs.writeSync(this.toCore, bytes, off, bytes.length - off)
  }

  private readExactly(n: number): Uint8Array {
    const out = new Uint8Array(n)
    let off = 0
    while (off < n) {
      const got = this.fs.readSync(this.fromCore, out, off, n - off, null)
      if (got === 0) throw new CoreTrap("the native core exited (a corenative guard line on stderr names a runaway step or heap)")
      off += got
    }
    return out
  }

  private send(op: number, handle: number, kind: number, now: number, payload: Uint8Array, origin = 0) {
    const head = new Uint8Array(22)
    const dv = new DataView(head.buffer)
    dv.setUint8(0, op); dv.setUint32(1, handle, true); dv.setUint8(5, kind); dv.setFloat64(6, now, true); dv.setUint32(14, origin, true); dv.setUint32(18, payload.length, true)
    this.writeAll(head)
    this.bytesCrossed += payload.length
    if (payload.length) this.writeAll(payload)
  }

  /** One request; answers the core's uuidv7 and read requests (type 2, ABI section 4.2 pig.read: `reader` answers it) until the op's own response arrives. */
  call(op: number, handle: number, kind: number, now: number, payload: Uint8Array = new Uint8Array(0), reader?: (sql: number, params: Param[]) => Uint8Array, origin = 0): Uint8Array {
    this.send(op, handle, kind, now, payload, origin)
    for (;;) {
      const head = this.readExactly(5)
      const type = head[0]!
      const len = new DataView(head.buffer).getUint32(1, true)
      const body = this.readExactly(len)
      this.bytesCrossed += len
      if (type === 0) return body
      if (type === 2) {
        if (!reader) throw new CoreTrap("the native core read outside a step")
        const { sql, params } = decodeReadRequest(body, 0, body.length)
        this.send(8, 0, 0, 0, reader(sql, params))
        continue
      }
      const text = this.makeUuid()
      if (text.length !== 36) throw new CoreTrap(`uuidv7 returned ${text.length} bytes`)
      this.send(7, 0, 0, 0, enc.encode(text))
    }
  }

  /** The core process's heap after a full collection and what its runtime obtained from the OS (mem_stats slots 6 and 7; the call runs the collection). */
  heapStats(): { heap: number; reserved: number } {
    const b = this.call(6, 0, 0, 0)
    const dv = new DataView(b.buffer, b.byteOffset, b.byteLength)
    return { heap: Number(dv.getBigUint64(48, true)), reserved: Number(dv.getBigUint64(56, true)) }
  }

  /**
   * The count gate's heap (op 10, heap_threads): the core process's live heap after two full collections, the Go runtime's
   * live threads at that reading, the heap bytes one more thread holds (measured by the process's first call, which starts
   * six threads to do so; 0 if it could not), and the threads at the call's first reading. `heap - threads * perThread` is
   * the heap net of the runtime's per-thread objects, which follow the host's scheduling, not the core.
   */
  threadStats(): { heap: number; threads: number; perThread: number; threadsBefore: number } {
    const b = this.call(10, 0, 0, 0)
    const dv = new DataView(b.buffer, b.byteOffset, b.byteLength)
    const u = (i: number) => Number(dv.getBigUint64(i * 8, true))
    return { heap: u(0), threads: u(1), perThread: u(2), threadsBefore: u(3) }
  }

  /** The core process's bytes allocated, allocations and completed collections since it started (alloc_stats, ABI section 4.1); no collection runs. */
  allocStats(): { bytes: number; mallocs: number; gcs: number } {
    const b = this.call(9, 0, 0, 0)
    const dv = new DataView(b.buffer, b.byteOffset, b.byteLength)
    return { bytes: Number(dv.getBigUint64(0, true)), mallocs: Number(dv.getBigUint64(8, true)), gcs: Number(dv.getBigUint64(16, true)) }
  }

  newSession(): NativeSession {
    const id = new DataView(this.call(3, 0, 0, 0).buffer).getUint32(0, true)
    return new NativeSession(this, id)
  }
}

export class NativeSession {
  private freed = false
  private readonly core: NativeCore
  readonly handle: number
  /** Answers the core's pig.read calls (ABI section 4.2) with the encoded answer; the host loop sets it. */
  reader?: (sql: number, params: Param[]) => Uint8Array
  constructor(core: NativeCore, handle: number) { this.core = core; this.handle = handle }

  step(kind: number, payload: Uint8Array | undefined, now: number, origin = 0): Step {
    if (this.freed) throw new CoreTrap("the session handle is freed")
    const out = this.core.call(4, this.handle, kind, now, payload ?? new Uint8Array(0), this.reader, origin)
    return decodeStep(out, 0, out.length)
  }

  rows(answers: Rows[], now: number): Step {
    let buf = new Uint8Array(0)
    const n = encodeRows(answers, size => (buf = new Uint8Array(size)))
    return this.step(EVENT.rows, buf.subarray(0, n), now)
  }

  memStats() {
    const b = this.core.call(6, this.handle, 0, 0)
    const dv = new DataView(b.buffer, b.byteOffset, b.byteLength)
    const v = (i: number) => (b.length >= (i + 1) * 8 ? Number(dv.getBigUint64(i * 8, true)) : 0)
    return { arena: v(0), index: v(1), derived: v(2), documents: v(3), scratch: v(4), live: v(5), heap: v(6), linear: v(7) }
  }

  free() {
    if (this.freed) return
    this.freed = true
    this.core.call(5, this.handle, 0, 0)
  }
}
