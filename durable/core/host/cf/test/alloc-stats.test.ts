// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// alloc_stats through the host's two bindings (ABI section 4.1): WasmCore and NativeCore decode three u64, the third the
// completed collections, which reading alloc_stats does not move and mem_stats (a full collection) does. The count gate's
// `gcs` (durable/contract/lib/counts.mjs) is this field summed per phase.
import assert from "node:assert/strict"
import * as childProcess from "node:child_process"
import * as fs from "node:fs"
import * as os from "node:os"
import * as path from "node:path"
import { test } from "node:test"
import { NativeCore } from "../src/native.ts"
import { WasmCore } from "../src/wasm.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const repo = path.join(root, "..", "..", "..", "..")
const UUID = "0190a6b2-0000-7000-8000-000000000001"

/** Each binding's counters: reading twice keeps the collection count, mem_stats moves it, and collections stay far fewer than allocations. */
function checkAllocStats(core: { allocStats(): { bytes: number; mallocs: number; gcs: number }; heapStats(): unknown }) {
  const a = core.allocStats()
  const b = core.allocStats()
  assert.ok(Number.isInteger(a.gcs) && a.gcs >= 0, JSON.stringify(a))
  assert.equal(b.gcs, a.gcs, "reading alloc_stats runs no collection")
  core.heapStats()
  const c = core.allocStats()
  assert.ok(c.gcs > b.gcs, `mem_stats runs a full collection: ${JSON.stringify([b, c])}`)
  assert.ok(c.bytes >= b.bytes && c.mallocs >= b.mallocs && c.mallocs > 0 && c.gcs < c.mallocs, JSON.stringify([b, c]))
}

test(`WasmCore.allocStats (${toolchain}) reads the completed collections from the third counter`, () => {
  const wasm = path.join(root, "dist", `core-${toolchain}.wasm`)
  if (!fs.existsSync(wasm)) childProcess.execFileSync(path.join(root, "..", "..", "budget", "build-wasm.sh"), [toolchain, path.join(root, "..", "..", "cmd", "corewasm"), wasm], { stdio: "inherit" })
  const module = new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(fs.readFileSync(wasm))
  checkAllocStats(new WasmCore(module, { uuidv7: () => UUID, log: () => {} }))
})

test("NativeCore.allocStats reads the completed collections from the third counter of op 9", () => {
  const exe = path.join(tempDir("alloc-stats"), "corenative")
  childProcess.execFileSync("go", ["build", "-trimpath", "-buildvcs=false", "-o", exe, "./durable/core/cmd/corenative"], { cwd: repo, stdio: "inherit", env: { ...process.env, GOTOOLCHAIN: process.env.GOTOOLCHAIN ?? `go${fs.readFileSync(path.join(repo, ".go-version"), "utf8").trim()}` } })
  checkAllocStats(new NativeCore(exe, { fs, childProcess, os, path }, { uuidv7: () => UUID }))
})
