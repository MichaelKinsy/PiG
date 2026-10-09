// Loads a core as a WASI reactor and wraps its ABI 1 exports (ABI section 4). One WasmCore is one Wasm instance and may
// serve many sessions (one handle per Durable Object). Lane-specific imports (ABI section 10) are supplied by the options.
import { uuidv7 } from "@earendil-works/pi-ai"
import { ABI_ID, EVENT, WASM_EXPORTS } from "./abi.ts"
import { decodeStep, encodeRows, type Rows, type Step } from "./wire.ts"

export type Exports = Record<string, any> & { memory: WebAssembly.Memory }

export interface CoreOptions {
  /** Entropy for the core's uuidv7 (WASI random_get). Capture runs pass a seeded generator. */
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
  /** Milliseconds spent in `new WebAssembly.Instance` plus `_initialize` (M1: instantiate and runtime init). */
  readonly instantiateMs: number
  readonly initMs: number
  private readonly log: (text: string) => void

  constructor(module: WebAssembly.Module, options: CoreOptions = {}) {
    const t0 = performance.now()
    let memory!: WebAssembly.Memory
    const u8 = () => new Uint8Array(memory.buffer), dv = () => new DataView(memory.buffer)
    this.log = options.log ?? (text => console.log("[core]", text))
    const random = options.random ?? (out => { crypto.getRandomValues(out) })
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
    const pig = { uuidv7: (dst: number) => { const text = makeUuid(); if (text.length !== 36) throw new CoreTrap(`uuidv7 returned ${text.length} bytes`); new Uint8Array(memory.buffer, dst, 36).set(enc.encode(text)) } }
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

  get memory() { return this.x.memory }
  get memoryBytes() { return this.x.memory.buffer.byteLength }

  private readSqlTable() {
    const mem = new Uint8Array(this.x.memory.buffer)
    const dv = new DataView(this.x.memory.buffer)
    let p = this.x.sql_table() as number
    const n = dv.getUint16(p, true); p += 2
    const out: string[] = []
    for (let i = 0; i < n; i++) { const len = dv.getUint32(p, true); p += 4; out.push(dec.decode(mem.subarray(p, p + len))); p += len }
    return out
  }

  newSession(): WasmSession {
    return new WasmSession(this, this.x.session_new() as number)
  }
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

  constructor(core: WasmCore, handle: number) {
    this.core = core
    this.handle = handle
    if (!handle) throw new CoreTrap("session_new returned 0")
  }

  private reserve = (n: number): Uint8Array => {
    const ptr = this.core.x.in_reserve(n) as number
    return new Uint8Array(this.core.x.memory.buffer, ptr, n)
  }

  /** Deliver an event whose payload is bytes (every kind but rows). */
  step(kind: number, payload: Uint8Array | undefined, now: number): Step {
    if (this.freed) throw new CoreTrap("the session handle is freed")
    const n = payload?.length ?? 0
    if (payload && n) this.reserve(n).set(payload)
    return this.run(kind, n, now)
  }

  /** Deliver a rows event. */
  rows(answers: Rows[], now: number): Step {
    if (this.freed) throw new CoreTrap("the session handle is freed")
    const n = encodeRows(answers, this.reserve)
    return this.run(EVENT.rows, n, now)
  }

  private run(kind: number, n: number, now: number): Step {
    const x = this.core.x
    const t0 = performance.now()
    let length: number
    try { length = x.step(this.handle, kind, n, now) as number } catch (e) {
      if (e instanceof CoreTrap) throw e
      throw new CoreTrap(`the core trapped: ${(e as Error).message}`)
    }
    const t1 = performance.now()
    const ptr = x.out_ptr() as number
    const step = decodeStep(new Uint8Array(x.memory.buffer), ptr, length)
    if (this.keepRaw) step.raw = new Uint8Array(x.memory.buffer).slice(ptr, ptr + length)
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
