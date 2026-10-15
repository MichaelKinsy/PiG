// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// The step export answers with a part list (ABI section 4.1): one part is the step, read in place; several are joined in
// order into one host buffer, so a model request the core did not copy into its output buffer reaches decodeStep whole.
import assert from "node:assert/strict"
import { test } from "node:test"
import { stepBytes } from "../src/wasm.ts"

function memoryWith(parts: [number, number[]][], listAt: number, list: [number, number][]): ArrayBuffer {
  const buffer = new ArrayBuffer(256)
  const mem = new Uint8Array(buffer), dv = new DataView(buffer)
  for (const [at, bytes] of parts) mem.set(bytes, at)
  dv.setUint32(listAt, list.length, true)
  list.forEach(([p, l], i) => { dv.setUint32(listAt + 4 + 8 * i, p, true); dv.setUint32(listAt + 8 + 8 * i, l, true) })
  return buffer
}

test("one part is the step in place", () => {
  const buffer = memoryWith([[40, [1, 2, 3]]], 0, [[40, 3]])
  const { mem, at, length } = stepBytes(buffer, 0)
  assert.equal(mem.buffer, buffer)
  assert.deepEqual([...mem.subarray(at, at + length)], [1, 2, 3])
})

test("several parts are joined in their order, not their addresses'", () => {
  const buffer = memoryWith([[100, [1, 2]], [40, [3, 4, 5]], [200, [6]]], 0, [[100, 2], [40, 3], [200, 1]])
  const { mem, at, length } = stepBytes(buffer, 0)
  assert.notEqual(mem.buffer, buffer)
  assert.deepEqual([...mem.subarray(at, at + length)], [1, 2, 3, 4, 5, 6])
})
