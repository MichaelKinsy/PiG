// Loads a core as a WASI reactor and wraps its ABI 1 exports (ABI section 4). One WasmCore is one Wasm instance and may
// serve many sessions (one handle per Durable Object). Lane-specific imports (ABI section 10) are supplied by the options.
import { uuidv7 } from "@earendil-works/pi-ai"
import { ABI_ID, EVENT, WASM_EXPORTS } from "./abi.ts"
import { decodeReadRequest, decodeStep, encodeRows, type Param, type Rows, type Step } from "./wire.ts"

export type Exports = Record<string, any> & { memory: WebAssembly.Memory }

/** A splitmix32 stream: private to one core instance and the same on every run. */
function runtimeEntropy(): (out: Uint8Array) => void {
  let state = 0x9e3779b9
  return out => {
    for (let i = 0; i < out.length; i++) {
      state = (state + 0x9e3779b9) | 0
      let z = state
      z = Math.imul(z ^ (z >>> 16), 0x85ebca6b)
      z = Math.imul(z ^ (z >>> 13), 0xc2b2ae35)
      out[i] = (z ^ (z >>> 16)) & 0xff
    }
  }
}

export interface CoreOptions {
  /** WASI random_get, which the Go and TinyGo runtimes read for their own hash seeds. The core draws no semantic randomness (uuids come from `uuidv7`), so the default is a private fixed-seed generator that leaves the host's random stream, and with it the ids a seeded run mints, undisturbed. */
  random?: (out: Uint8Array) => void
  /** pi-ai's uuidv7() for the provider document's sessionId (ABI section 4.2). Capture runs return the values recorded on the effect tape. */
  uuidv7?: () => string
  /** Wall clock for the Go runtime's own needs (clock_time_get); the core never reads it for semantics. */
  clock?: () => number
  /** Imports beyond wasi_snapshot_preview1: hist.* for lane L3, kernel.* for lane L4. They receive the instance's memory getter. */
  extraImports?: (memory: () => WebAssembly.Memory) => WebAssembly.Imports
  /** Where the core's debug output (fd_write) goes. Defaults to console.log. */
  log?: (text: string) => void
  /** The abi_id the host was built against. Defaults to abi.json's; tests pass another to prove the refusal. */
  expectedAbiId?: string
}

/** The inputs a core takes during a step besides its event: what a flight recorder (observe.ts) keeps for a deterministic replay. */
export interface StepTap {
  /** A pig.uuidv7 value the core asked for. */
  uuid(text: string): void
  /** A pig.read call: the request bytes as the core wrote them, and the answer the host gave. */
  read(request: Uint8Array, answer: Uint8Array): void
}

export class CoreTrap extends Error {}
export class AbiMismatch extends Error {}

const dec = new TextDecoder()
const enc = new TextEncoder()

/** Names a module imports outside WASI: the shim refuses to guess what they mean. */
export const foreignImports = (module: WebAssembly.Module) =>
  WebAssembly.Module.imports(module).filter(i => i.module !== "wasi_snapshot_preview1")

export class WasmCore {
  readonly x: Exports
  readonly sqlTable: string[]
  readonly abiId: string
  /** Payload bytes that crossed the boundary since the instance started: events and read answers in, steps and read requests out (the count gate's bytesCrossed). */
  bytesCrossed = 0
  /** Milliseconds spent in `new WebAssembly.Instance` plus `_initialize` (M1: instantiate and runtime init). */
  readonly instantiateMs: number
  readonly initMs: number
  private readonly log: (text: string) => void

  constructor(module: WebAssembly.Module, options: CoreOptions = {}) {
    const t0 = performance.now()
    let memory!: WebAssembly.Memory
    const u8 = () => new Uint8Array(memory.buffer), dv = () => new DataView(memory.buffer)
    this.log = options.log ?? (text => console.log("[core]", text))
    const random = options.random ?? runtimeEntropy()
    const clock = options.clock ?? (() => Date.now())
    const wasi: Record<string, Function> = {
      args_sizes_get: (a: number, b: number) => { dv().setUint32(a, 0, true); dv().setUint32(b, 0, true); return 0 },
      args_get: () => 0,
      environ_sizes_get: (a: number, b: number) => { dv().setUint32(a, 0, true); dv().setUint32(b, 0, true); return 0 },
      environ_get: () => 0,
      clock_time_get: (_id: number, _p: bigint, out: number) => { dv().setBigUint64(out, BigInt(Math.round(clock() * 1e6)), true); return 0 },
      random_get: (ptr: number, n: number) => { random(new Uint8Array(memory.buffer, ptr, n)); return 0 },
      fd_write: (_fd: number, iovs: number, n: number, nw: number) => {
        let total = 0, text = ""
        for (let i = 0; i < n; i++) { const p = dv().getUint32(iovs + i * 8, true), l = dv().getUint32(iovs + i * 8 + 4, true); text += dec.decode(u8().subarray(p, p + l)); total += l }
        if (text.trim()) this.log(text.trimEnd())
        dv().setUint32(nw, total, true); return 0
      },
      fd_read: (_fd: number, _iovs: number, _n: number, nr: number) => { dv().setUint32(nr, 0, true); return 0 },
      fd_close: () => 0,
      fd_fdstat_get: (fd: number, buf: number) => { if (fd > 2) return 8; u8().fill(0, buf, buf + 24); dv().setUint8(buf, 2); return 0 },
      fd_fdstat_set_flags: () => 0,
      fd_prestat_get: () => 8,
      fd_prestat_dir_name: () => 8,
      poll_oneoff: (inp: number, out: number, n: number, nev: number) => {
        for (let i = 0; i < n; i++) { u8().copyWithin(out + i * 32, inp + i * 48, inp + i * 48 + 8); dv().setUint16(out + i * 32 + 8, 0, true); dv().setUint8(out + i * 32 + 10, dv().getUint8(inp + i * 48 + 8)) }
        dv().setUint32(nev, n, true); return 0
      },
      sched_yield: () => 0,
      proc_exit: (code: number) => { throw new CoreTrap(`core called proc_exit(${code})`) },
    }
    const makeUuid = options.uuidv7 ?? (() => uuidv7())
    const pig = {
      uuidv7: (dst: number) => { const text = makeUuid(); if (text.length !== 36) throw new CoreTrap(`uuidv7 returned ${text.length} bytes`); new Uint8Array(memory.buffer, dst, 36).set(enc.encode(text)); this.activeTap?.uuid(text) },
      // ABI section 4.2: one read statement answered inside the call, by the session whose step is running. An answer longer
      // than the core's buffer is kept for the core's immediate second call with a buffer of its length.
      read: (req: number, reqLen: number, dst: number, cap: number) => {
        let answer = this.overflow
        this.overflow = undefined
        if (!answer) {
          const reader = this.activeReader
          if (!reader) throw new CoreTrap("pig.read outside a step")
          const { sql, params } = decodeReadRequest(new Uint8Array(memory.buffer), req, reqLen)
          this.bytesCrossed += reqLen
          answer = reader(sql, params)
          this.activeTap?.read(new Uint8Array(memory.buffer, req, reqLen).slice(), answer)
        }
        if (answer.length > cap) { this.overflow = answer; return answer.length }
        new Uint8Array(memory.buffer, dst, answer.length).set(answer)
        this.bytesCrossed += answer.length
        return answer.length
      },
    }
    const imports: WebAssembly.Imports = { wasi_snapshot_preview1: wasi, pig, ...options.extraImports?.(() => memory) }
    const missing = foreignImports(module).filter(i => !(imports[i.module] && i.name in imports[i.module]!))
    if (missing.length) throw new AbiMismatch(`the module imports ${missing.map(i => `${i.module}.${i.name}`).join(", ")}, which this host does not provide`)
    const instance = new WebAssembly.Instance(module, imports)
    this.instantiateMs = performance.now() - t0
    memory = instance.exports.memory as WebAssembly.Memory
    this.x = instance.exports as Exports
    for (const name of WASM_EXPORTS) if (!(name in this.x) && name !== "_initialize") throw new AbiMismatch(`the module does not export ${name}`)
    const t1 = performance.now()
    ;(this.x._initialize as Function | undefined)?.()
    this.initMs = performance.now() - t1
    this.abiId = dec.decode(new Uint8Array(memory.buffer, this.x.abi_id() as number, 64))
    const expected = options.expectedAbiId ?? ABI_ID
    if (this.abiId !== expected) throw new AbiMismatch(`abi_id ${this.abiId} is not the host's ${expected}`)
    this.sqlTable = this.readSqlTable()
  }

  /** The pig.read answer of the session whose step is running (WasmSession.run sets it). */
  activeReader?: (sql: number, params: Param[]) => Uint8Array
  /** The tap of the session whose step is running (WasmSession.run sets it). */
  activeTap?: StepTap
  private overflow?: Uint8Array

  private inPtr = 0
  private inCap = 0
  /** How many times the host asked the core for an input buffer. */
  reservations = 0

  /** The address of an input buffer of at least `n` bytes. The core keeps its input buffer until a larger one is asked for, so the host reserves again only when a payload outgrows the last reservation (ABI section 4.1), growing geometrically. */
  reserveInput(n: number): number {
    if (n > this.inCap) {
      const want = Math.max(n, 65536, this.inCap * 2)
      this.reservations++
      try { this.inPtr = this.x.in_reserve(want) as number } catch (e) {
        // Running out of memory at the module's cap is a trap like any other: the handle is discarded, the isolate lives.
        throw new CoreTrap(`the core trapped reserving ${want} bytes: ${(e as Error).message}`)
      }
      this.inCap = want
    }
    return this.inPtr
  }

  get memory() { return this.x.memory }
  get memoryBytes() { return this.x.memory.buffer.byteLength }

  /** The instance's heap after a full collection (mem_stats slot 6, which runs the collection) and its linear memory, which only grows. */
  heapStats(): { heap: number; reserved: number } {
    const ptr = this.x.mem_stats(0) as number
    return { heap: Number(new DataView(this.x.memory.buffer, ptr, 64).getBigUint64(48, true)), reserved: this.memoryBytes }
  }

  /** The instance's bytes allocated, allocations and completed collections since it started (alloc_stats, ABI section 4.1); no collection runs. */
  allocStats(): { bytes: number; mallocs: number; gcs: number } {
    const ptr = this.x.alloc_stats() as number
    const dv = new DataView(this.x.memory.buffer, ptr, 24)
    return { bytes: Number(dv.getBigUint64(0, true)), mallocs: Number(dv.getBigUint64(8, true)), gcs: Number(dv.getBigUint64(16, true)) }
  }

  private readSqlTable() {
    // sql_table may grow memory (it encodes the table on first call), so the views come after it.
    let p = this.x.sql_table() as number
    const mem = new Uint8Array(this.x.memory.buffer)
    const dv = new DataView(this.x.memory.buffer)
    const n = dv.getUint16(p, true); p += 2
    const out: string[] = []
    for (let i = 0; i < n; i++) { const len = dv.getUint32(p, true); p += 4; out.push(dec.decode(mem.subarray(p, p + len))); p += len }
    return out
  }

  newSession(): WasmSession {
    return new WasmSession(this, this.x.session_new() as number)
  }
}

/**
 * The bytes of the step whose part list (ABI section 4.1, step) is at ptr: u32 n, then n x (u32 ptr, u32 len). One part
 * is the step itself, read in place; several (a payload of 64 KiB or more is a part of its own, where the core built it)
 * are joined into one host buffer.
 */
export function stepBytes(buffer: ArrayBuffer, ptr: number): { mem: Uint8Array; at: number; length: number } {
  const dv = new DataView(buffer)
  const mem = new Uint8Array(buffer)
  const n = dv.getUint32(ptr, true)
  if (n === 1) return { mem, at: dv.getUint32(ptr + 4, true), length: dv.getUint32(ptr + 8, true) }
  let length = 0
  for (let i = 0; i < n; i++) length += dv.getUint32(ptr + 8 + 8 * i, true)
  const out = new Uint8Array(length)
  for (let i = 0, off = 0; i < n; i++) {
    const p = dv.getUint32(ptr + 4 + 8 * i, true), l = dv.getUint32(ptr + 8 + 8 * i, true)
    out.set(mem.subarray(p, p + l), off)
    off += l
  }
  return { mem: out, at: 0, length }
}

export class WasmSession {
  readonly core: WasmCore
  readonly handle: number
  private freed = false
  /** Wall time inside `step` (the core's own work) and decoding the result, milliseconds. */
  msStep = 0
  msDecode = 0
  steps = 0
  /** Keep the raw bytes of every step on `step.raw` (binding conformance only). */
  keepRaw = false
  /** Answers the core's pig.read calls (ABI section 4.2) with the encoded answer; the host loop sets it. */
  reader?: (sql: number, params: Param[]) => Uint8Array
  /** Sees the uuids and reads the core takes during each step; the flight recorder sets it. */
  tap?: StepTap

  constructor(core: WasmCore, handle: number) {
    this.core = core
    this.handle = handle
    if (!handle) throw new CoreTrap("session_new returned 0")
  }

  // The pointer first: reserving may grow memory, which detaches the buffer a view made earlier would name.
  private reserve = (n: number): Uint8Array => {
    const ptr = this.core.reserveInput(n)
    return new Uint8Array(this.core.x.memory.buffer, ptr, n)
  }

  /** Deliver an event whose payload is bytes (every kind but rows). `origin` tags the host's async context of the call (ABI section 5.4): 0 for none. */
  step(kind: number, payload: Uint8Array | undefined, now: number, origin = 0): Step {
    if (this.freed) throw new CoreTrap("the session handle is freed")
    const n = payload?.length ?? 0
    if (payload && n) this.reserve(n).set(payload)
    this.core.bytesCrossed += n
    return this.run(kind, n, now, origin)
  }

  /** Deliver a rows event. */
  rows(answers: Rows[], now: number): Step {
    if (this.freed) throw new CoreTrap("the session handle is freed")
    const n = encodeRows(answers, this.reserve)
    this.core.bytesCrossed += n
    return this.run(EVENT.rows, n, now, 0)
  }

  /** The effect ID and hop count of the last `hop` that answered 1. */
  hopId = 0
  hopTicks = 0
  private hopBuffer?: ArrayBuffer
  private hopView?: DataView

  /**
   * Deliver `hopped` for hops effect `effectId` through the `hop` export (ABI section 4.1): the event is its argument, so nothing is
   * copied into the core, and a step the host would not decode is an integer. 0: the step is empty; 1: its only output is one plain
   * hops chain (hopId, hopTicks); else the step.
   */
  hop(effectId: number, now: number): Step | 0 | 1 {
    if (this.freed) throw new CoreTrap("the session handle is freed")
    const x = this.core.x
    let ptr: number
    const outer = this.core.activeReader
    this.core.activeReader = this.reader
    try { ptr = x.hop(this.handle, effectId, now) as number } catch (e) {
      if (e instanceof CoreTrap) throw e
      throw new CoreTrap(`the core trapped: ${(e as Error).message}`)
    } finally { this.core.activeReader = outer }
    this.steps++
    if (ptr === 0) return 0
    const buffer = x.memory.buffer
    if (buffer !== this.hopBuffer) { this.hopBuffer = buffer; this.hopView = new DataView(buffer) }
    const view = this.hopView!
    if (view.getUint32(ptr, true) === 0) {
      this.hopId = view.getUint32(ptr + 4, true)
      this.hopTicks = view.getUint32(ptr + 8, true)
      this.core.bytesCrossed += 12
      return 1
    }
    const { mem, at, length } = stepBytes(buffer, ptr)
    this.core.bytesCrossed += length
    const step = decodeStep(mem, at, length)
    if (this.keepRaw) step.raw = mem.slice(at, at + length)
    return step
  }

  private run(kind: number, n: number, now: number, origin: number): Step {
    const x = this.core.x
    const t0 = performance.now()
    let ptr: number
    const outer = this.core.activeReader, outerTap = this.core.activeTap
    this.core.activeReader = this.reader
    this.core.activeTap = this.tap
    try { ptr = x.step(this.handle, kind, n, now, origin) as number } catch (e) {
      if (e instanceof CoreTrap) throw e
      throw new CoreTrap(`the core trapped: ${(e as Error).message}`)
    } finally { this.core.activeReader = outer; this.core.activeTap = outerTap }
    const t1 = performance.now()
    const { mem, at, length } = stepBytes(x.memory.buffer, ptr)
    this.core.bytesCrossed += length
    const step = decodeStep(mem, at, length)
    if (this.keepRaw) step.raw = mem.slice(at, at + length)
    this.msStep += t1 - t0
    this.msDecode += performance.now() - t1
    this.steps++
    return step
  }

  /** The eight counters of mem_stats (ABI section 4.1): arena, index, derived, documents, scratch, total live, heap in use, linear memory. */
  memStats(): { arena: number; index: number; derived: number; documents: number; scratch: number; live: number; heap: number; linear: number } {
    const ptr = this.core.x.mem_stats(this.handle) as number
    const dv = new DataView(this.core.x.memory.buffer, ptr, 64)
    const v = (i: number) => Number(dv.getBigUint64(i * 8, true))
    return { arena: v(0), index: v(1), derived: v(2), documents: v(3), scratch: v(4), live: v(5), heap: v(6), linear: v(7) || this.core.memoryBytes }
  }

  stats() { return { steps: this.steps, msStep: this.msStep, msDecode: this.msDecode } }

  free() {
    if (this.freed) return
    this.freed = true
    this.core.x.session_free(this.handle)
  }
}
