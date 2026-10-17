// The QuickJS host of the codemode Worker (src/codemode/quickjs.ts): the primitives the codemode Go module drives, run against
// the embedded quickjs.wasm the same way codemode/engine.go's vm drives it on wazero. Independent of the Durable core.
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { test } from "node:test"
import { fileURLToPath } from "node:url"
import { QJS_EXPORTS, QuickJSInstance, QuickJSTrap, quickjsWasi, type QuickJSHooks } from "../src/codemode/quickjs.ts"

const here = dirname(fileURLToPath(import.meta.url))
const quickjsWasmPath = join(here, "..", "..", "..", "..", "..", "codemode", "assets", "quickjs.wasm")
const module = new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(quickjsWasmPath))

const enc = new TextEncoder()
const dec = new TextDecoder()
const bits = (f: number) => { const v = new DataView(new ArrayBuffer(8)); v.setFloat64(0, f); return v.getBigUint64(0) }
const unbits = (b: bigint) => { const v = new DataView(new ArrayBuffer(8)); v.setBigUint64(0, b); return v.getFloat64(0) }

/** The vm of codemode/engine.go, in JavaScript: the same call sequence over the same exports. */
class Vm {
  readonly q: QuickJSInstance
  readonly callbacks = new Map<string, (self: number, args: number[]) => number>()
  polls = 0
  interruptAfter = Infinity
  constructor(opts: { memoryLimit?: number; stack?: number; clock?: () => number } = {}) {
    const hooks: QuickJSHooks = {
      hostCall: (namePtr, nameLen, self, argc, argvPtr) => {
        const name = dec.decode(this.q.read(namePtr, nameLen))
        const argv = new DataView(this.q.read(argvPtr, argc * 4).buffer)
        const args = Array.from({ length: argc }, (_, i) => argv.getUint32(i * 4, true))
        const fn = this.callbacks.get(name)
        return fn ? this.c("qjs_dup_value", fn(self, args)) : 0
      },
      interrupt: () => ++this.polls > this.interruptAfter,
    }
    this.q = new QuickJSInstance(module, hooks, { clock: opts.clock })
    this.c("qjs_init")
    if (opts.memoryLimit) this.c("qjs_set_memory_limit", opts.memoryLimit)
    this.c("qjs_set_max_stack_size", opts.stack ?? 512 * 1024)
    this.c("qjs_set_interrupt_handler", 1)
  }
  c(name: (typeof QJS_EXPORTS)[number], ...args: number[]): number {
    const a = args.map(x => BigInt(x))
    while (a.length < 4) a.push(0n)
    return Number(BigInt.asUintN(32, this.q.call(QJS_EXPORTS.indexOf(name), args.length, a[0]!, a[1]!, a[2]!, a[3]!)))
  }
  str(s: string): [number, number] {
    const bytes = enc.encode(s)
    const p = this.c("wasm_malloc", bytes.length + 1)
    this.q.write(p, Uint8Array.from([...bytes, 0]))
    return [p, bytes.length]
  }
  newString(s: string) { const [p, n] = this.str(s); const h = this.c("qjs_new_string", p, n); this.c("wasm_free", p); return h }
  newNumber(f: number) { return Number(this.q.call(QJS_EXPORTS.indexOf("qjs_new_number"), 1, bits(f), 0n, 0n, 0n)) }
  toNumber(h: number) { return unbits(this.q.call(QJS_EXPORTS.indexOf("qjs_get_float64"), 1, BigInt(h), 0n, 0n, 0n)) }
  toString(h: number): string {
    const lenPtr = this.c("wasm_malloc", 4)
    const p = this.c("qjs_get_string_len", h, lenPtr)
    const n = new DataView(this.q.read(lenPtr, 4).buffer).getUint32(0, true)
    this.c("wasm_free", lenPtr)
    return p === 0 ? "<null>" : dec.decode(this.q.read(p, n))
  }
  /** Returns the handle, or the pending exception as "name: message". */
  checked(h: number): { handle: number } | { error: string } {
    if (this.c("qjs_is_exception", h) === 0) return { handle: h }
    this.c("qjs_free_value", h)
    const ex = this.c("qjs_get_exception")
    const prop = (name: string) => { const [p] = this.str(name); const v = this.c("qjs_get_prop_string", ex, p); this.c("wasm_free", p); return v }
    return { error: `${this.toString(prop("name"))}: ${this.toString(prop("message"))}` }
  }
  eval(code: string) {
    const [cp, cn] = this.str(code), [fp] = this.str("test.js")
    const h = this.c("qjs_eval", cp, cn, fp, 0)
    this.c("wasm_free", cp); this.c("wasm_free", fp)
    return this.checked(h)
  }
  value(code: string): number {
    const r = this.eval(code)
    assert.ok("handle" in r, "error" in r ? r.error : "")
    return r.handle
  }
  number(code: string): number { return this.toNumber(this.value(code)) }
  string(code: string): string { return this.toString(this.value(code)) }
  callFunction(fn: number, self: number, ...args: number[]) {
    const argv = args.length ? this.c("wasm_malloc", 4 * args.length) : 0
    if (args.length) this.q.write(argv, new Uint8Array(new Uint32Array(args).buffer))
    const h = this.c("qjs_call", fn, self, args.length, argv)
    if (argv) this.c("wasm_free", argv)
    return this.checked(h)
  }
  newFunction(name: string, fn: (self: number, args: number[]) => number): number {
    this.callbacks.set(name, fn)
    const [p, n] = this.str(name)
    const h = this.c("qjs_new_host_function", p, n, 0)
    this.c("wasm_free", p)
    return h
  }
}

test("every export the Go module may call exists in quickjs.wasm, and the table has no duplicates", () => {
  const exported = new Set(WebAssembly.Module.exports(module).map(e => e.name))
  for (const name of QJS_EXPORTS) assert.ok(exported.has(name), `${name} is exported`)
  assert.equal(new Set(QJS_EXPORTS).size, QJS_EXPORTS.length)
})

test("a script evaluates, and numbers, strings with non-ASCII text and exceptions cross as the Go vm reads them", () => {
  const vm = new Vm()
  assert.equal(vm.number("1 + 2 * 3.5"), 8)
  assert.equal(vm.string("'a' + 'é' + '\\ud83d\\ude00'"), "aé😀")
  assert.deepEqual(vm.eval("throw new RangeError('boom')"), { error: "RangeError: boom" })
  assert.ok("error" in vm.eval("(") && (vm.eval("(") as { error: string }).error.startsWith("SyntaxError"))
})

test("a host function gets its receiver and argument handles, and its result handle returns to the script (host_call re-enters the VM)", () => {
  const vm = new Vm()
  const seen: { argc: number; self: number }[] = []
  const add = vm.newFunction("add", (self, args) => {
    seen.push({ argc: args.length, self })
    // The callback calls back into QuickJS while QuickJS is inside host_call.
    return vm.newNumber(vm.toNumber(args[0]!) + vm.toNumber(args[1]!) + vm.number("100"))
  })
  const run = vm.value("(add, x) => add(x, 2) * 2")
  const r = vm.callFunction(run, vm.c("qjs_get_undefined"), add, vm.newNumber(40))
  assert.ok("handle" in r)
  assert.equal(vm.toNumber(r.handle), 284)
  assert.deepEqual(seen.map(s => s.argc), [2])
})

test("an exception thrown by the host_call hook propagates out of the QuickJS call that reached it", () => {
  const vm = new Vm()
  const boom = vm.newFunction("boom", () => { throw new Error("hook failed") })
  const run = vm.value("(f) => f()")
  assert.throws(() => vm.callFunction(run, vm.c("qjs_get_undefined"), boom), /hook failed/)
})

test("the interrupt hook is polled while a script runs and stops it with an uncatchable error", () => {
  const vm = new Vm()
  vm.interruptAfter = 3
  // Bounded, so a host that never interrupts fails here instead of hanging the file.
  const r = vm.eval("let i = 0; try { while (i++ < 5e6) {} } catch (e) { 'caught' }")
  assert.ok("error" in r, "the interrupt cannot be caught by the script")
  assert.match(r.error, /interrupted/)
  assert.ok(vm.polls > 3)
})

test("the QuickJS memory limit fails an allocation with an out-of-memory error the script cannot outrun", () => {
  const vm = new Vm({ memoryLimit: 4 * 1024 * 1024 })
  const r = vm.eval("const a = []; while (true) a.push(new Array(1000).fill(1))")
  assert.ok("error" in r)
  assert.match(r.error, /out of memory/i)
})

test("the stack limit turns runaway recursion into a catchable RangeError instead of a Wasm trap", () => {
  const vm = new Vm({ stack: 256 * 1024 })
  assert.equal(vm.string("function f() { return f() + 1 } try { f() } catch (e) { e.constructor.name }"), "RangeError")
})

test("promise jobs run when the host asks and the host sees them pending", () => {
  const vm = new Vm()
  vm.value("globalThis.out = []; Promise.resolve().then(() => out.push(1)); 0")
  assert.equal(vm.c("qjs_is_job_pending"), 1)
  while (vm.c("qjs_is_job_pending")) assert.ok(new Int32Array([vm.c("qjs_execute_pending_job")])[0]! >= 0)
  assert.equal(vm.number("out.length"), 1)
})

test("Date.now and performance.now follow the host clock, and Math.random is seeded from it (quickjs-wasi's WASI clock shim)", () => {
  let now = 1_700_000_000_123
  const vm = new Vm({ clock: () => now })
  assert.equal(vm.number("Date.now()"), 1_700_000_000_123)
  now += 5
  assert.equal(vm.number("Date.now()"), 1_700_000_000_128)
  const first = (t: number) => new Vm({ clock: () => t }).number("Math.random()")
  assert.equal(first(1000), first(1000))
  assert.notEqual(first(1000), first(1001))
})

test("quickjs.wasm's own WASI imports are the six the shim implements, and its host imports are the six env functions", () => {
  const imports = WebAssembly.Module.imports(module).map(i => `${i.module}.${i.name}`).sort()
  assert.deepEqual(imports, [
    "env.host_call", "env.host_get_timezone_offset", "env.host_interrupt", "env.host_module_load", "env.host_module_normalize", "env.host_promise_rejection",
    "wasi_snapshot_preview1.clock_time_get", "wasi_snapshot_preview1.fd_close", "wasi_snapshot_preview1.fd_fdstat_get", "wasi_snapshot_preview1.fd_seek",
    "wasi_snapshot_preview1.fd_write", "wasi_snapshot_preview1.random_get",
  ])
})

test("a linear-memory access outside the instance is a QuickJSTrap, not a partial copy", () => {
  const vm = new Vm()
  const end = vm.q.memoryBytes
  assert.throws(() => vm.q.read(end - 2, 4), QuickJSTrap)
  assert.throws(() => vm.q.write(end - 2, new Uint8Array(4)), QuickJSTrap)
  assert.throws(() => vm.q.read(-1, 1), QuickJSTrap)
  assert.equal(vm.q.read(end - 4, 4).length, 4)
})

test("the WASI shim answers as quickjs-wasi 3.6.2 dist/wasi-shim.js with Pi's discardOutput fd_write", () => {
  const memory = new WebAssembly.Memory({ initial: 1 })
  const view = new DataView(memory.buffer)
  const filled: number[] = []
  const wasi = quickjsWasi(() => memory, { clock: () => 1234.9, random: out => { out.fill(7); filled.push(out.length) } })
  assert.equal(wasi.clock_time_get(0, 0n, 64), 0)
  assert.equal(view.getBigUint64(64, true), 1_234_000_000n)
  assert.equal(wasi.clock_time_get(1, 0n, 64), 0)
  view.setBigUint64(64, 5n, true)
  assert.equal(wasi.clock_time_get(2, 0n, 64), 52, "CLOCK_PROCESS_CPUTIME_ID is ENOSYS")
  assert.equal(view.getBigUint64(64, true), 5n)
  assert.equal(wasi.random_get(128, 16), 0)
  assert.deepEqual(filled, [16])
  assert.equal(new Uint8Array(memory.buffer, 128, 16).every(b => b === 7), true)
  view.setUint32(256 + 4, 3, true); view.setUint32(256 + 12, 5, true)
  for (const fd of [1, 2, 7]) { assert.equal(wasi.fd_write(fd, 256, 2, 300), 0); assert.equal(view.getUint32(300, true), 8) }
  for (const fd of [0, 1, 2, 3]) assert.equal(wasi.fd_close(fd), 52)
  for (const fd of [0, 3]) assert.equal(wasi.fd_fdstat_get(fd, 400), 8)
  assert.equal(wasi.fd_fdstat_get(1, 400), 0)
  assert.equal(view.getUint8(400), 2)
  assert.equal(wasi.fd_seek(1, 0n, 0, 500), 52)
})

test("Date's timezone offset is the host's at the given instant (quickjs-wasi's default timezoneOffset: \"host\")", () => {
  const previous = process.env.TZ
  try {
    process.env.TZ = "America/New_York"
    const vm = new Vm()
    assert.equal(vm.number("new Date(1700000000000).getTimezoneOffset()"), new Date(1700000000000).getTimezoneOffset())
    assert.equal(vm.number("new Date(1700000000000).getTimezoneOffset()"), 300)
    assert.equal(vm.number("new Date(1690000000000).getTimezoneOffset()"), 240, "the offset follows the instant (DST)")
  } finally {
    if (previous === undefined) delete process.env.TZ
    else process.env.TZ = previous
  }
})
