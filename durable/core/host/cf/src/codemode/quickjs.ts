// The QuickJS side of the codemode Worker: one instance of quickjs-wasi's quickjs.wasm, driven by the codemode Go module
// through numbered calls (the Go side is codemode/engine_hostimport.go, which lists the same exports in the same order).
//
// Workers cannot compile Wasm from bytes, so the Module is a static import made once at global scope and every execution
// gets its own Instance over it. Linear memory never shrinks: an instance is dropped after the execution, and always after
// an interrupt, an out-of-memory or a trap.

/** The QuickJS exports the Go module calls, in the order of its `qjsExports` table. A call names an export by index. */
export const QJS_EXPORTS = [
  "qjs_init",
  "qjs_set_memory_limit",
  "qjs_set_max_stack_size",
  "qjs_set_interrupt_handler",
  "qjs_get_undefined",
  "qjs_get_false",
  "qjs_get_true",
  "qjs_free_value",
  "qjs_dup_value",
  "qjs_new_string",
  "qjs_new_number",
  "qjs_get_float64",
  "qjs_is_undefined",
  "qjs_get_bool",
  "qjs_get_string_len",
  "qjs_get_prop_string",
  "qjs_get_exception",
  "qjs_is_exception",
  "qjs_eval",
  "qjs_call",
  "qjs_new_host_function",
  "qjs_is_job_pending",
  "qjs_execute_pending_job",
  "wasm_malloc",
  "wasm_free",
  "qjs_init2",
  "qjs_get_quickjs_version",
] as const

/** The one export that takes a double (the others take i32): its argument index, as the raw IEEE 754 bits a Go caller passes. */
const FLOAT_ARGUMENT: Partial<Record<(typeof QJS_EXPORTS)[number], number>> = { qjs_new_number: 0 }
/** The one export that returns a double. */
const FLOAT_RESULT: (typeof QJS_EXPORTS)[number] = "qjs_get_float64"

/** The most arguments an export takes (qjs_eval, qjs_call). */
export const QJS_MAX_ARGS = 4

/** What QuickJS calls back into: the guest's `host_call` (a function made by qjs_new_host_function) and `host_interrupt`. */
export interface QuickJSHooks {
  /** quickjs-wasi `host_call(namePtr, nameLen, this, argc, argvPtr)`: returns the result handle. */
  hostCall(namePtr: number, nameLen: number, thisHandle: number, argc: number, argvPtr: number): number
  /** `host_interrupt()`: true stops the running script (QuickJS throws an uncatchable InternalError: interrupted). */
  interrupt(): boolean
}

export class QuickJSTrap extends Error {}

/** The wall clock in ms, as the quickjs-wasi WASI shim reports it for both the realtime and the monotonic clock. */
export type Clock = () => number

export interface QuickJSOptions {
  clock?: Clock
  random?: (out: Uint8Array) => void
}

/**
 * What a deterministic VM reads instead of the host's clock, random bytes and timezone (Pi's vm.ts deterministicWasi): the module
 * pushes the clock readings in nanoseconds (the realtime clock, then every other clock) and the seed of the random_get PRNG.
 */
interface Virtual {
  clock: boolean
  realtime: bigint
  monotonic: bigint
  random: (() => number) | undefined
}

/** mulberry32, one 32-bit draw per call (Pi's vm.ts prng; the Go module's deterministic.go prng). */
export function prng(seed: number): () => number {
  let state = seed >>> 0
  return () => {
    state = (state + 0x6d2b79f5) >>> 0
    let value = Math.imul(state ^ (state >>> 15), 1 | state)
    value = (value + Math.imul(value ^ (value >>> 7), 61 | value)) ^ value
    return (value ^ (value >>> 14)) >>> 0
  }
}

const ERRNO_SUCCESS = 0
const ERRNO_BADF = 8
const ERRNO_NOSYS = 52
const CLOCK_REALTIME = 0
const CLOCK_MONOTONIC = 1

/**
 * quickjs-wasi's WASI shim as Pi's codemode VM uses it (upstream: quickjs-wasi 3.6.2 dist/wasi-shim.js createWasiShim, with
 * fd_write replaced by vm.ts discardOutput): the host clock in whole milliseconds for the realtime and the monotonic clock, a
 * cryptographic random source, standard output and error discarded but reported as written, and no files.
 */
export function quickjsWasi(memory: () => WebAssembly.Memory, options: QuickJSOptions = {}, virtual: Virtual = { clock: false, realtime: 0n, monotonic: 0n, random: undefined }) {
  const view = () => new DataView(memory().buffer)
  const clock = options.clock ?? (() => Date.now())
  const random = options.random ?? (out => { crypto.getRandomValues(out) })
  return {
    clock_time_get: (id: number, _precision: bigint, out: number) => {
      if (id !== CLOCK_REALTIME && id !== CLOCK_MONOTONIC) return ERRNO_NOSYS
      if (virtual.clock) {
        view().setBigUint64(out, id === CLOCK_REALTIME ? virtual.realtime : virtual.monotonic, true)
        return ERRNO_SUCCESS
      }
      view().setBigUint64(out, BigInt(Math.trunc(clock())) * 1_000_000n, true)
      return ERRNO_SUCCESS
    },
    random_get: (ptr: number, n: number) => {
      const bytes = new Uint8Array(memory().buffer, ptr, n)
      if (virtual.random) {
        for (let index = 0; index < n; index += 4) {
          const value = virtual.random()
          for (let byte = 0; byte < 4 && index + byte < n; byte++) bytes[index + byte] = value >>> (byte * 8)
        }
      } else random(bytes)
      return ERRNO_SUCCESS
    },
    fd_write: (_fd: number, iovs: number, n: number, written: number) => {
      let total = 0
      for (let i = 0; i < n; i++) total += view().getUint32(iovs + i * 8 + 4, true)
      view().setUint32(written, total, true)
      return ERRNO_SUCCESS
    },
    fd_close: (_fd: number) => ERRNO_NOSYS,
    fd_fdstat_get: (fd: number, buf: number) => {
      if (fd !== 1 && fd !== 2) return ERRNO_BADF
      const v = view()
      v.setUint8(buf, 2)
      v.setUint16(buf + 2, 0, true)
      v.setBigUint64(buf + 8, 0n, true)
      v.setBigUint64(buf + 16, 0n, true)
      return ERRNO_SUCCESS
    },
    fd_seek: (_fd: number, _offset: bigint, _whence: number, _result: number) => ERRNO_NOSYS,
  }
}

const float = new DataView(new ArrayBuffer(8))

export class QuickJSInstance {
  private readonly exports: Record<string, Function>
  private readonly functions: Function[]
  private readonly memory: WebAssembly.Memory
  private readonly floatArgument: (number | undefined)[]
  private readonly floatResult: number
  private readonly virtual: Virtual = { clock: false, realtime: 0n, monotonic: 0n, random: undefined }

  constructor(module: WebAssembly.Module, hooks: QuickJSHooks, options: QuickJSOptions = {}) {
    let memory!: WebAssembly.Memory
    const virtual = this.virtual
    const wasi = quickjsWasi(() => memory, options, virtual)
    const env = {
      host_call: (namePtr: number, nameLen: number, thisHandle: number, argc: number, argvPtr: number) => hooks.hostCall(namePtr, nameLen, thisHandle, argc, argvPtr) >>> 0,
      host_interrupt: () => (hooks.interrupt() ? 1 : 0),
      // No rejection handler is installed (qjs_set_promise_rejection_handler is never called), so the guest does not reach here.
      host_promise_rejection: () => {},
      host_module_normalize: () => 0,
      host_module_load: () => 0,
      // quickjs-wasi's default `timezoneOffset: "host"`: the host's UTC offset in seconds east of UTC at the given second,
      // which arrives as the high and low halves of a signed 64-bit count (upstream: quickjs-wasi dist/index.js
      // hostGetTimezoneOffset). A Worker's offset is always UTC.
      host_get_timezone_offset: (hi: number, lo: number) => virtual.clock ? 0 : -new Date(Number((BigInt(hi) << 32n) | BigInt(lo >>> 0)) * 1000).getTimezoneOffset() * 60,
    }
    const instance = new WebAssembly.Instance(module, { wasi_snapshot_preview1: wasi, env })
    this.exports = instance.exports as unknown as Record<string, Function>
    memory = instance.exports.memory as WebAssembly.Memory
    this.memory = memory
    ;(this.exports._initialize as Function)()
    this.functions = QJS_EXPORTS.map(name => {
      const fn = this.exports[name]
      if (typeof fn !== "function") throw new QuickJSTrap(`quickjs.wasm does not export ${name}`)
      return fn
    })
    this.floatArgument = QJS_EXPORTS.map(name => FLOAT_ARGUMENT[name])
    this.floatResult = QJS_EXPORTS.indexOf(FLOAT_RESULT)
  }

  /**
   * Calls export `index` of {@link QJS_EXPORTS} with `argc` of the raw 64-bit arguments (a double travels as its bits) and
   * returns the raw 64-bit result (zero for no result). A trap, including a `host_call` that throws, propagates.
   */
  call(index: number, argc: number, a0: bigint, a1: bigint, a2: bigint, a3: bigint): bigint {
    const fn = this.functions[index]
    if (!fn) throw new QuickJSTrap(`quickjs export index ${index} is out of range`)
    const raw = [a0, a1, a2, a3]
    const floatAt = this.floatArgument[index]
    const args: (number | bigint)[] = new Array(argc)
    for (let i = 0; i < argc; i++) {
      const bits = raw[i]!
      if (i === floatAt) {
        float.setBigUint64(0, BigInt.asUintN(64, bits))
        args[i] = float.getFloat64(0)
      } else {
        args[i] = Number(BigInt.asUintN(32, bits))
      }
    }
    const result = fn(...args) as number | undefined
    if (result === undefined) return 0n
    if (index === this.floatResult) {
      float.setFloat64(0, result)
      return float.getBigUint64(0)
    }
    return BigInt(result >>> 0)
  }

  /** A deterministic VM's clock readings in nanoseconds: the realtime clock and every other clock; they replace the host's clock. */
  setClock(realtime: bigint, monotonic: bigint): void {
    this.virtual.clock = true
    this.virtual.realtime = realtime
    this.virtual.monotonic = monotonic
  }

  /** random_get draws from the PRNG that starts at `seed`, and the timezone is UTC, as in a deterministic VM. */
  setRandomSeed(seed: number): void {
    this.virtual.random = prng(seed)
  }

  /** Copies `length` bytes of QuickJS linear memory out. */
  read(ptr: number, length: number): Uint8Array {
    this.check(ptr, length)
    return new Uint8Array(this.memory.buffer, ptr, length).slice()
  }

  /** Copies `target.length` bytes of QuickJS linear memory into `target`. */
  readInto(ptr: number, target: Uint8Array): void {
    this.check(ptr, target.length)
    target.set(new Uint8Array(this.memory.buffer, ptr, target.length))
  }

  /** Copies bytes into QuickJS linear memory. */
  write(ptr: number, data: Uint8Array): void {
    this.check(ptr, data.length)
    new Uint8Array(this.memory.buffer, ptr, data.length).set(data)
  }

  /** Linear memory in use by the instance, in bytes. */
  get memoryBytes(): number {
    return this.memory.buffer.byteLength
  }

  private check(ptr: number, length: number) {
    if (!Number.isInteger(ptr) || !Number.isInteger(length) || ptr < 0 || length < 0 || ptr + length > this.memory.buffer.byteLength) {
      throw new QuickJSTrap(`quickjs memory access ${ptr}+${length} is out of range`)
    }
  }
}
