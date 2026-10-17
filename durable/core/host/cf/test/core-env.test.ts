// A Harness opened without a core finds the module DCORE_WASM names (the conformance gate's vitest rows have no setup module).
import assert from "node:assert/strict"
import { afterEach, test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { coreFromEnvironment } from "../src/facade/core-env.ts"
import { createRegistry, Harness, MemoryStorage } from "../src/facade/index.ts"
import { probeWasm } from "./helpers.ts"

const previous = process.env.DCORE_WASM
afterEach(() => { if (previous === undefined) delete process.env.DCORE_WASM; else process.env.DCORE_WASM = previous })

const models = () => { const m = createModels(); m.setProvider(fauxProvider().provider); return m as any }
const open = () => Harness.open(new MemoryStorage(), { models: models(), registry: createRegistry() } as any, BACKGROUND_CONTEXT)

test("DCORE_WASM names the core a Harness uses when none was selected", async () => {
  process.env.DCORE_WASM = probeWasm()
  const module = await coreFromEnvironment()
  assert.ok(module instanceof WebAssembly.Module)
  assert.equal(await coreFromEnvironment(), module, "one compiled module per path")
  const harness = await open()
  await harness.close(BACKGROUND_CONTEXT)
})

test("without DCORE_WASM or a selected core, opening a Harness names the three ways to choose one", async () => {
  delete process.env.DCORE_WASM
  assert.equal(await coreFromEnvironment(), undefined)
  await assert.rejects(open(), /options\.core, call useCore\(module\) or set DCORE_WASM/)
})
