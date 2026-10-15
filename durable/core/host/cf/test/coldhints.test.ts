// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// The core carries compilation hints for the functions its first turn runs (budget/coldhints, budget/coldset.txt), and a Node host
// that enables them compiles those functions with the module, not one at a time on first call. The tests build the core, run a
// child Node process that compiles it as a host does and, for the second run, opens a session and runs one eight-lookup turn,
// and compare the functions V8 reports compiling with Liftoff (--trace-wasm-compilation-times) in the two runs. V8 prints the
// trace from C stdio, so the runs are compared as sets, not by position in the output.
//
// The child is TypeScript, and Node strips its types with a Wasm module of its own (amaro, more than 4,000 functions, about
// 730 of them compiled in every run), so the size of a module's trace does not identify the core. The core's trace is the
// one whose every function index has the body size the core's code section gives that index.
import assert from "node:assert/strict"
import { execFileSync, spawnSync } from "node:child_process"
import { readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { root, tempDir, toolchain } from "./helpers.ts"

const tinygoOnly = { skip: toolchain !== "tinygo" && "TinyGo build only", timeout: 600_000 }

let built: string | undefined, dir: string | undefined
function core(): string {
  if (built) return built
  dir = tempDir("coldhints")
  built = join(dir, "core-tinygo.wasm")
  execFileSync(join(root, "..", "..", "budget", "build-wasm.sh"), ["tinygo", join(root, "..", "..", "cmd", "corewasm"), built], { stdio: "inherit" })
  return built
}
test.after(() => { if (dir) rmSync(dir, { recursive: true, force: true }) })

function uleb(b: Uint8Array, p: number): [number, number] {
  let r = 0
  for (let shift = 0; ; shift += 7) {
    const x = b[p++]!
    r += (x & 0x7f) * 2 ** shift
    if (!(x & 0x80)) return [r, p]
  }
}

/** The body size of every function of a module, by function index (imports have none). */
function bodySizes(bytes: Uint8Array): Map<number, number> {
  const mod = new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(bytes)
  let index = WebAssembly.Module.imports(mod).filter(i => i.kind === "function").length
  const sizes = new Map<number, number>()
  for (let p = 8; p < bytes.length;) {
    const id = bytes[p]!
    const [size, body] = uleb(bytes, p + 1)
    if (id === 10) {
      let [n, q] = uleb(bytes, body)
      for (; n > 0; n--, index++) {
        const [len, start] = uleb(bytes, q)
        sizes.set(index, len)
        q = start + len
      }
    }
    p = body + size
  }
  return sizes
}

/** The function indexes V8 compiled with Liftoff in the child's core module: the trace of exactly one module must match the core. */
function liftoffed(wasm: string, hints: boolean, turn: boolean): Set<number> {
  const env = { ...process.env, DCORE_WASM: wasm, DURABLE_COMPILE_HINTS: hints ? "1" : "0", ...(turn ? {} : { COLDHINTS_COMPILE_ONLY: "1" }) }
  const r = spawnSync(process.execPath, ["--trace-wasm-compilation-times", join(root, "test", "coldhints-child.ts")], { env, encoding: "utf8", maxBuffer: 64 << 20, timeout: 120_000 })
  // A compile whose promise never settles (V8 14.1 with hints on an asynchronous compile) ends here, not at the suite timeout.
  assert.equal(r.error, undefined, `child did not finish: ${r.error?.message}`)
  assert.equal(r.status, 0, `child failed: ${r.stderr}`)
  const sizes = bodySizes(readFileSync(wasm))
  const byModule = new Map<string, { indexes: Set<number>; core: boolean }>()
  for (const m of r.stdout.matchAll(/Compiled function (0x[0-9a-f]+)#(\d+) using Liftoff,[^\n]*?bodysize (\d+)/g)) {
    const seen = byModule.get(m[1]!) ?? { indexes: new Set<number>(), core: true }
    byModule.set(m[1]!, seen)
    seen.indexes.add(Number(m[2]))
    seen.core &&= sizes.get(Number(m[2])) === Number(m[3])
  }
  const core = [...byModule.values()].filter(s => s.core)
  assert.ok(core.length <= 1, `${core.length} traced modules match the core's body sizes`)
  return core[0]?.indexes ?? new Set<number>()
}

/** The number of names in budget/coldset.txt: the most functions the hints can mark. */
function coldsetNames(): number {
  return readFileSync(join(root, "..", "..", "budget", "coldset.txt"), "utf8").split("\n").filter(l => l !== "" && !l.startsWith("#")).length
}

test("the TinyGo core carries both hint sections", tinygoOnly, () => {
  const mod = new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(core()))
  for (const name of ["compilationHints", "metadata.code.compilation_priority"]) {
    assert.equal(WebAssembly.Module.customSections(mod, name).length, 1, `one ${name} section`)
  }
})

test("without hints, the first turn compiles the functions it runs one by one on first call", tinygoOnly, () => {
  const compiledWithModule = liftoffed(core(), false, false)
  assert.equal(compiledWithModule.size, 0, `${compiledWithModule.size} core functions compiled with the module although no hints are enabled: the sections must be inert`)
  const compiledByTurn = liftoffed(core(), false, true)
  assert.ok(compiledByTurn.size >= 600, `${compiledByTurn.size} core functions compiled in a run with no hints; the run is not reaching the core`)
})

test("with hints, the module compile covers the functions of the first turn: at most 5% of them compile later", tinygoOnly, () => {
  const withModule = liftoffed(core(), true, false)
  const afterTurn = liftoffed(core(), true, true)
  assert.ok(withModule.size >= 600, `${withModule.size} functions compiled with the module: the hints are not applied`)
  // Only the marked functions: a compile of every function (eager compilation instead of hints) is not what the hints do.
  const marked = coldsetNames()
  assert.ok(withModule.size <= marked, `${withModule.size} functions compiled with the module, more than the ${marked} that coldset.txt marks`)
  const late = [...afterTurn].filter(i => !withModule.has(i))
  assert.ok(late.length <= Math.ceil(0.05 * withModule.size), `${late.length} functions compiled after the module (${late.slice(0, 8).join(", ")}), over 5% of ${withModule.size}: regenerate budget/coldset.txt`)
})
