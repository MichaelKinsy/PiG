// The TinyGo heap policy of the Durable core (build-wasm.sh and the runtime fork in tinygo/src/runtime/arch_tinygowasm.go): the heap starts
// at the initial memory, grows by a fixed step (a heap smaller than the step doubles) instead of doubling without end, and stops at the ceiling with a trap instead of growing.
//
//   node --test durable/core/budget/growheap.test.mjs
//
// Each test builds the probe module (testdata/growheap/main.go) with build-wasm.sh and drives it from Node, counting only deterministic
// things: linear memory size after every allocation, allocations that succeed, collection cycles.
import assert from "node:assert/strict"
import { execFile, execFileSync, spawnSync } from "node:child_process"
import { chmodSync, closeSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { after, describe, test } from "node:test"
import { fileURLToPath } from "node:url"
import { WASI } from "node:wasi"

const here = dirname(fileURLToPath(import.meta.url))
const MiB = 1048576
/** The default heap step of build-wasm.sh. */
const STEP = 4 * MiB
const out = mkdtempSync(join(tmpdir(), "growheap-"))
after(() => rmSync(out, { recursive: true, force: true }))

/**
 * Build the probe with build-wasm.sh, called the way durable/core/Makefile calls it (relative paths from durable/core); env sets
 * CORE_INITIAL_MEMORY, CORE_MAX_MEMORY and CORE_HEAP_STEP (bytes).
 */
function build(name, env = {}) {
  const wasm = join(out, `${name}.wasm`)
  execFileSync("sh", ["budget/build-wasm.sh", "tinygo", "budget/testdata/growheap", wasm], { cwd: join(here, ".."), stdio: ["ignore", "inherit", "inherit"], env: { ...process.env, ...env } })
  return wasm
}

/** The memory section of a module: [initial, maximum] in bytes. */
function memoryLimits(path) {
  const b = readFileSync(path)
  let p = 8
  const leb = () => { let r = 0, s = 0, x; do { x = b[p++]; r += (x & 0x7f) * 2 ** s; s += 7 } while (x & 0x80); return r }
  while (p < b.length) {
    const id = b[p++]
    const end = leb() + p
    if (id === 5) { leb(); const flags = b[p++]; const min = leb(); const max = flags & 1 ? leb() : undefined; return [min * 65536, max === undefined ? undefined : max * 65536] }
    p = end
  }
  throw new Error(`${path} has no memory section`)
}

let instances = 0

/** An instance of the module; `output()` is what its runtime wrote to stdout (where TinyGo's runtimeFatal prints). */
function instantiate(path) {
  const log = join(out, `instance-${instances++}.out`)
  const stdout = openSync(log, "w")
  after(() => closeSync(stdout))
  const wasi = new WASI({ version: "preview1", stdout })
  const instance = new WebAssembly.Instance(new WebAssembly.Module(readFileSync(path)), wasi.getImportObject())
  wasi.initialize(instance)
  const x = instance.exports
  return { x, size: () => x.memory.buffer.byteLength, output: () => readFileSync(log, "utf8") }
}

/** The trap is the allocator giving up at the ceiling, not some other fatal error of the runtime. */
function assertOutOfMemory(m, failure) {
  assert.ok(failure instanceof WebAssembly.RuntimeError, `the failure was ${failure}`)
  assert.equal(m.output(), "fatal error: out of memory\n")
}

/** Keep `chunk`-byte allocations until one traps or `limit` of them succeeded; the memory size after each one and the failure. */
function fill(path, chunk, limit = 1 << 20) {
  const m = instantiate(path)
  const sizes = [m.size()]
  let failure
  try {
    for (let i = 0; i < limit; i++) { m.x.alloc(chunk); sizes.push(m.size()) }
  } catch (e) { failure = e }
  return { m, sizes, failure, kept: sizes.length - 1 }
}

/** The growth steps of a size series that are not what the policy gives: the step, or the size of a heap smaller than the step. */
const offPolicy = (sizes, step = STEP) => sizes.slice(1).map((s, i) => [s - sizes[i], Math.min(step, sizes[i])]).filter(([d, want]) => d > 0 && d !== want)

/** The distinct growth steps of a size series, in bytes. */
const steps = sizes => sizes.slice(1).map((s, i) => s - sizes[i]).filter(d => d > 0)

describe("the TinyGo build of the core", () => {
  const attempt = env => spawnSync("sh", ["budget/build-wasm.sh", "tinygo", "budget/testdata/growheap", join(out, "refused.wasm")], { cwd: join(here, ".."), encoding: "utf8", env: { ...process.env, ...env } })

  test("refuses a TinyGo that is not the version pinned in toolchains.lock", () => {
    const pinned = readFileSync(join(here, "..", "..", "..", "automation", "dev", "toolchains.lock"), "utf8").match(/^tinygo\t([^\t]+)\t/m)[1]
    const fake = join(out, "tinygo-other")
    writeFileSync(fake, "#!/bin/sh\necho 'tinygo version 0.0.1 linux/amd64 (using go version go1.27.1 and LLVM version 22.1.4)'\n")
    chmodSync(fake, 0o755)
    const r = attempt({ TINYGO: fake })
    assert.equal(r.status, 2)
    assert.match(r.stderr, new RegExp(`tinygo 0\\.0\\.1, the core's runtime fork is for ${pinned.replaceAll(".", "\\.")}`))
  })

  test("builds of one output path at the same time all succeed and leave a whole module", async () => {
    // A test suite whose files each build the probe on demand starts several builds of dist/probe-tinygo.wasm at once.
    const wasm = join(out, "concurrent.wasm")
    const one = () => new Promise((resolve, reject) => execFile("sh", ["budget/build-wasm.sh", "tinygo", "budget/testdata/growheap", wasm], { cwd: join(here, "..") }, (e, _o, stderr) => (e ? reject(new Error(stderr)) : resolve())))
    await Promise.all([one(), one(), one()])
    assert.equal(instantiate(wasm).size(), memoryLimits(wasm)[0])
  })

  test("refuses memory settings that are not whole Wasm pages, or an initial memory past the maximum", () => {
    assert.match(attempt({ CORE_HEAP_STEP: "1000000" }).stderr, /1000000 is not a positive multiple of 64 KiB/)
    assert.match(attempt({ CORE_MAX_MEMORY: "lots" }).stderr, /decimal byte counts, got 'lots'/)
    assert.match(attempt({ CORE_INITIAL_MEMORY: String(8 * MiB), CORE_MAX_MEMORY: String(4 * MiB) }).stderr, /passes the maximum/)
    assert.match(attempt({ CORE_RUNTIME_ASSERTS: "yes" }).stderr, /CORE_RUNTIME_ASSERTS is 1 or unset/)
  })
})

describe("the TinyGo heap of the core", () => {
  const defaults = build("default")

  test("starts at the initial memory and is capped by the linker", () => {
    // The core fits in 1 MiB at 50 and at 3,500 turns and the memory is retained for the object's life: the default is exactly that.
    assert.deepEqual(memoryLimits(defaults), [1 * MiB, 96 * MiB])
    assert.equal(instantiate(defaults).size(), 1 * MiB)
  })

  test("grows by a fixed step, not by doubling", () => {
    // 64 KiB allocations until the heap is well past the point where doubling would have grown it to 16 MiB or more.
    const { sizes, failure } = fill(defaults, 64 * 1024, 800)
    assert.equal(failure, undefined)
    const grown = steps(sizes)
    assert.ok(grown.length >= 10, `the heap grew ${grown.length} times`)
    // A heap smaller than the step doubles until it reaches the step; from there on every growth is the step.
    assert.deepEqual(offPolicy(sizes), [])
    assert.equal(grown.at(-1), STEP)
    assert.ok(grown[0] < STEP, `the first growth was ${grown[0]} bytes`)
  })

  test("an allocation larger than the step is satisfied", () => {
    // 20 MiB is five steps: repeated steps never join into one free range, so the step doubles while one allocation waits.
    const m = instantiate(defaults)
    m.x.alloc(20 * MiB)
    assert.ok(m.size() >= 20 * MiB && m.size() <= 96 * MiB, `memory is ${m.size()} bytes`)
    m.x.alloc(1024) // The heap is intact.
  })

  test("after an allocation larger than the step, the heap grows by the fixed step again", () => {
    // The doubling belongs to the one allocation that waits for a large enough range; the next allocation starts at one step.
    const m = instantiate(defaults)
    m.x.alloc(6 * MiB)
    const sizes = [m.size()]
    for (let i = 0; i < 800; i++) { m.x.alloc(64 * 1024); sizes.push(m.size()) }
    const grown = steps(sizes)
    assert.ok(grown.length >= 5, `the heap grew ${grown.length} times`)
    assert.deepEqual(offPolicy(sizes), [])
  })

  test("exceeding the ceiling traps and leaves the memory at the ceiling", () => {
    const { m, sizes, failure, kept } = fill(defaults, 256 * 1024)
    assertOutOfMemory(m, failure)
    assert.match(String(failure.message), /unreachable/)
    assert.ok(kept * 256 * 1024 <= 96 * MiB, `${kept} allocations of 256 KiB were kept`)
    // The last step that fits is within one metadata-safe step (1/64 of the heap) of the ceiling.
    assert.ok(m.size() <= 96 * MiB && m.size() >= 94 * MiB, `memory stopped at ${m.size()} bytes`)
    assert.equal(sizes.at(-1), m.size(), "a failed allocation did not grow the memory")
  })

  test("one allocation past the ceiling traps", () => {
    const m = instantiate(defaults)
    let failure
    try { m.x.alloc(97 * MiB) } catch (e) { failure = e }
    assertOutOfMemory(m, failure)
    assert.ok(m.size() <= 96 * MiB)
  })

  test("the step, the initial memory and the ceiling are settings of the build", () => {
    const wasm = build("tuned", { CORE_HEAP_STEP: String(256 * 1024), CORE_INITIAL_MEMORY: String(2 * MiB), CORE_MAX_MEMORY: String(10 * MiB + 65536 * 8) })
    assert.deepEqual(memoryLimits(wasm), [2 * MiB, 10.5 * MiB])
    const { m, sizes, failure } = fill(wasm, 16 * 1024)
    assertOutOfMemory(m, failure)
    for (const d of steps(sizes)) assert.equal(d, 256 * 1024, `a growth step of ${d} bytes`)
    assert.ok(m.size() <= 10.5 * MiB && m.size() >= 10.5 * MiB - 256 * 1024 - 65536 * 4, `memory stopped at ${m.size()} bytes`)
  })

  test("a step that passes the ceiling halves until it fits", () => {
    // 4 MiB + 3 * 3 MiB passes 14 MiB: the last steps halve (1.5 MiB, 768 KiB, ...) down to the 1/64-of-the-heap minimum (4 pages here).
    const wasm = build("uneven", { CORE_HEAP_STEP: String(3 * MiB), CORE_INITIAL_MEMORY: String(4 * MiB), CORE_MAX_MEMORY: String(14 * MiB) })
    const { m, sizes, failure } = fill(wasm, 16 * 1024)
    assertOutOfMemory(m, failure)
    assert.deepEqual(steps(sizes).slice(0, 2), [3 * MiB, 3 * MiB])
    assert.ok(m.size() <= 14 * MiB && m.size() >= 14 * MiB - 4 * 65536, `memory stopped at ${m.size()} bytes`)
  })

  test("the contents of the heap survive growth past 64 MiB", () => {
    // Every growth moves the GC metadata to the new end of the heap; the objects below it keep their bytes.
    const m = instantiate(defaults)
    for (let i = 0; i < 80 * 16; i++) m.x.alloc(64 * 1024)
    assert.ok(m.size() > 80 * MiB)
    assert.equal(m.x.damaged(), 0)
    m.x.release()
    for (let i = 0; i < 40 * 16; i++) m.x.alloc(64 * 1024)
    assert.equal(m.x.damaged(), 0)
  })

  test("past 64 MiB every step keeps the old and new GC metadata apart (TinyGo's runtime assertions on)", () => {
    // With a step below 1/64 of the heap, setHeapEnd's memcpy would overlap and the asserting runtime stops with
    // "gc: heap did not grow enough at once" instead of reaching the ceiling. The default step (4 MiB) is above 1/64 of any heap
    // under the 96 MiB ceiling, so the rule binds only for a smaller CORE_HEAP_STEP: the build sets 1 MiB.
    const { m, sizes, failure } = fill(build("asserts", { CORE_RUNTIME_ASSERTS: "1", CORE_HEAP_STEP: String(MiB) }), 64 * 1024)
    assertOutOfMemory(m, failure)
    assert.ok(sizes.at(-1) >= 94 * MiB, `memory stopped at ${sizes.at(-1)} bytes`)
  })

  test("a growing heap collects garbage before it grows", () => {
    const m = instantiate(defaults)
    for (let round = 0; round < 8; round++) { for (let i = 0; i < 200; i++) m.x.alloc(64 * 1024); m.x.release() }
    assert.ok(m.x.num_gc() > 0, "no collection ran")
    assert.ok(m.size() <= 24 * MiB, `garbage made the heap ${m.size()} bytes`)
  })
})
