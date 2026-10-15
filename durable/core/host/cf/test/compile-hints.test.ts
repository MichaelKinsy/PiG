// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

import assert from "node:assert/strict"
import { test } from "node:test"
import { hintsCompileSync, hintsFlags } from "../src/compile-hints.ts"

test("the hints flag follows the V8 release: Node 24 (13.6), Node 26 (14.6), V8 15.1, V8 15.2 and later", () => {
  assert.deepEqual(hintsFlags("13.6.233.17-node.38"), ["--experimental-wasm-compilation-hints", "--no-wasm-lazy-compilation"])
  assert.deepEqual(hintsFlags("14.6.202.34-node.20"), ["--experimental-wasm-compilation-hints"])
  assert.deepEqual(hintsFlags("15.1.9"), ["--experimental-wasm-compilation-hints"])
  assert.deepEqual(hintsFlags("15.2.0"), ["--wasm-compilation-hints"])
  assert.deepEqual(hintsFlags("15.6.75"), ["--wasm-compilation-hints"])
  assert.deepEqual(hintsFlags("16.0.1"), ["--wasm-compilation-hints"])
})

test("the hinted compile is synchronous below V8 14.6: V8 13 reads the marks only without lazy compilation, and V8 14.1 never settles the promise", () => {
  assert.equal(hintsCompileSync("12.4.254.21-node.33"), true)
  assert.equal(hintsCompileSync("13.6.233.17-node.38"), true)
  assert.equal(hintsCompileSync("14.1.146.11-node.25"), true)
  assert.equal(hintsCompileSync("14.6.202.34-node.20"), false)
  assert.equal(hintsCompileSync("15.2.0"), false)
})
