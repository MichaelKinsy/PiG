// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// No function of the TinyGo core is huge. V8 compiles a function that runs hot with TurboFan, on a background thread, at a cost
// that grows faster than its body: before the build limited Binaryen's inlining of one-caller functions, Session.Step,
// Session.dispatch and adapter.Commit were 20-23 KB each, and those three and session.Finish cost 114 ms of TurboFan in the
// first turns of a session (27 ms after). The module's largest bodies are now under 16 KiB.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { root, tempDir, toolchain } from "./helpers.ts"

const LIMIT = 16 * 1024

/** The body size of every function of a module by its name-section name (a function without one is "#index"). */
function bodySizes(bytes: Uint8Array): Map<string, number> {
  let p = 8
  const u32 = () => { let r = 0, shift = 0, b: number; do { b = bytes[p++]!; r |= (b & 127) << shift; shift += 7 } while (b & 128); return r >>> 0 }
  const sizes: number[] = []
  const names = new Map<number, string>()
  let imported = 0
  while (p < bytes.length) {
    const id = bytes[p++]!, len = u32(), end = p + len
    if (id === 2) {
      for (let n = u32(); n > 0; n--) {
        const moduleLen = u32(); p += moduleLen
        const fieldLen = u32(); p += fieldLen
        const kind = bytes[p++]!
        if (kind === 0) { u32(); imported++ } else if (kind === 1) { p++; const flags = bytes[p++]!; u32(); if (flags & 1) u32() } else if (kind === 2) { const flags = bytes[p++]!; u32(); if (flags & 1) u32() } else p += 2
      }
    } else if (id === 10) {
      for (let n = u32(); n > 0; n--) { const size = u32(); sizes.push(size); p += size }
    } else if (id === 0) {
      const nameLen = u32(), name = new TextDecoder().decode(bytes.subarray(p, p + nameLen)); p += nameLen
      if (name === "name") {
        while (p < end) {
          const sub = bytes[p++]!, subLen = u32(), subEnd = p + subLen
          if (sub === 1) for (let n = u32(); n > 0; n--) { const index = u32(), l = u32(); names.set(index, new TextDecoder().decode(bytes.subarray(p, p + l))); p += l }
          p = subEnd
        }
      }
    }
    p = end
  }
  if (sizes.length === 0) throw new Error("no code section")
  return new Map(sizes.map((size, i) => [names.get(i + imported) ?? `#${i + imported}`, size]))
}

// The tests guard the build (budget/build-wasm.sh) and the source, so they build the module themselves, once: a
// dist/core-tinygo.wasm left by an earlier build would answer for a script and source it was not built from.
let built: Map<string, number> | undefined
function moduleSizes(): Map<string, number> {
  if (built) return built
  const dir = tempDir("functionsize"), wasm = join(dir, "core-tinygo.wasm")
  try {
    execFileSync(join(root, "..", "..", "budget", "build-wasm.sh"), ["tinygo", join(root, "..", "..", "cmd", "corewasm"), wasm], { stdio: "inherit" })
    built = bodySizes(readFileSync(wasm))
  } finally { rmSync(dir, { recursive: true, force: true }) }
  return built
}

const tinygoOnly = { skip: toolchain !== "tinygo" && "TinyGo build only", timeout: 600_000 }

test("the TinyGo core has no function body over 16 KiB: a giant tiers up to TurboFan at a cost that outgrows its size", tinygoOnly, () => {
  const sizes = moduleSizes()
  assert.ok(sizes.size > 1000, `${sizes.size} functions`)
  assert.ok(Math.max(...sizes.values()) <= LIMIT, `the largest function body is ${Math.max(...sizes.values())} bytes`)
})

// The functions every step or every job runs are compiled by V8 whole as soon as they are hot, so each is kept small: the cold
// parts (the Open dump, the commit assembly, the released-record walk, the general number formatting) are apart from them. The
// limit is per function, close enough to its body that inlining one of the split-off parts back crosses it: StepStart with
// releaseHeld is 3.7 KB, Step with StepStart 2.6 KB, settleSuccess with settle 5.9 KB, AppendNumber with appendFloat 2.9 KB.
// A function may be inlined into a caller the list also names; its own limit then applies to nothing, and the caller's does.
// A name that matches no body and no such caller fails, so that a renamed function or a module without a name section does not
// pass by finding nothing.
const HOT: [name: string, limit: number, inlinedInto?: string][] = [
  ["core.Session).Step", 2048],
  ["core.Session).dispatch", 2048],
  ["core.adapter).Commit", 2048],
  ["session.Session).Finish", 2048],
  ["session.Tx).settleSuccess", 2048, "session.Session).Finish"],
  ["core.handler).StepStart", 2048],
  ["core.mirror).releaseApplied", 2048, "core.handler).StepStart"],
  ["jv.AppendNumber", 2048],
  ["jv.parser).value", 4096],
  ["jv.parser).number", 2048, "jv.parser).value"],
  // The commit path: each splits off the parts that most commits skip or that run on their own (document actions, the watch and
  // view frames, the operation walk). Each limit is crossed by inlining back the smallest part split off it: Store.Commit is 3.1 KB
  // and 4.0 KB with applyDocument, publish 2.2 KB and 3.4 KB with commitChanges, publishWatches 1.6 KB and 5.4 KB with
  // advanceTaskGraph, Prepare 0.6 KB and 4.9 KB with emitOperations, emitOperations 4.4 KB and 5.8 KB with emitObject, and
  // mirror.Commit 1.0 KB and 2.6 KB with applyDocActions.
  ["store.Store).Commit", 3584],
  ["core.handler).publish$bound", 3072],
  ["core.handler).publishWatches", 2048],
  ["delta.Change).Prepare", 2048],
  ["delta.emitOperations", 5120],
  ["core.mirror).Commit", 2048],
]

test("the functions every step runs stay small: V8 compiles a hot function whole", tinygoOnly, () => {
  const sizes = moduleSizes()
  const bodies = (hot: string) => [...sizes].filter(([name]) => name.replaceAll("\\28", "(").replaceAll("\\29", ")").replaceAll("*", "").endsWith(hot))
  for (const [hot, limit, inlinedInto] of HOT) {
    const found = bodies(hot)
    if (found.length === 0) {
      assert.ok(inlinedInto !== undefined && bodies(inlinedInto).length > 0, `no function body is named ${hot}`)
      continue
    }
    for (const [name, size] of found) assert.ok(size <= limit, `${name} is ${size} bytes, over ${limit}`)
  }
})
