// The core's runtime entropy (WASI random_get) is a private fixed-seed stream: instantiating a core and opening sessions draws nothing
// from crypto.getRandomValues, so a seeded run's ids do not depend on how many cores it created.
import assert from "node:assert/strict"
import { test } from "node:test"
import { WasmCore } from "../src/wasm.ts"
import { probeModule } from "./helpers.ts"

test("a core instance never draws the host's random stream", () => {
  const real = crypto.getRandomValues.bind(crypto)
  let draws = 0
  Object.defineProperty(crypto, "getRandomValues", { configurable: true, writable: true, value: (array: ArrayBufferView) => { draws++; return real(array as Uint8Array) } })
  try {
    const core = new WasmCore(probeModule())
    core.newSession()
    assert.equal(draws, 0)
  } finally {
    Object.defineProperty(crypto, "getRandomValues", { configurable: true, writable: true, value: real })
  }
})
