// A provider receives the stream options Pi Durable's generation builds, in Pi's key order: generation.ts builds
// `{ ...streamOptions, signal: runtime.signal, sessionId, ...(reasoning) }`, so `signal` comes before `sessionId`. Key order is
// observable to a provider that enumerates, serializes or spreads its options into a request.
import assert from "node:assert/strict"
import { readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import * as Pi from "@earendil-works/pi-durable"
import { openNodeSqliteStorage as piSqlite } from "@earendil-works/pi-durable/storage/sqlite/node"
import { createRegistry } from "../src/facade/index.ts"
import { Harness } from "../src/facade/harness.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { WasmCore } from "../src/wasm.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const context = BACKGROUND_CONTEXT
const module = new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(join(root, "dist", `core-${toolchain}.wasm`)))

const impls = [
  { name: "pi", open: async (path: string, models: unknown) => Pi.Harness.open(await piSqlite(path), { models, registry: Pi.createRegistry() } as any, context) },
  {
    name: "candidate",
    open: async (path: string, models: unknown) => {
      const core = new WasmCore(module)
      return Harness.open(await openNodeSqliteStorage(path), { models, registry: createRegistry(), core: { newSession: () => core.newSession(), sqlTable: core.sqlTable } } as any, context)
    },
  },
]

/** The option keys of each model call one turn makes, as the provider's streamSimple receives them. */
async function optionKeys(impl: (typeof impls)[number], thinking?: string): Promise<string[][]> {
  const dir = tempDir("options")
  try {
    const faux = fauxProvider({ models: [{ id: "opt-1", reasoning: true }], tokenSize: { min: 1e9, max: 1e9 } })
    const respond = () => { faux.appendResponses([respond]); return fauxAssistantMessage("answer") }
    faux.setResponses([respond])
    const models = createModels()
    models.setProvider(faux.provider)
    const seen: string[][] = []
    const call = models.streamSimple.bind(models)
    ;(models as any).streamSimple = (model: unknown, ctx: unknown, options: object) => { seen.push(Object.keys(options ?? {})); return (call as any)(model, ctx, options) }
    const harness = await impl.open(join(dir, "store.sqlite"), models)
    const agent: Record<string, unknown> = { model: { provider: faux.getModel().provider, modelId: faux.getModel().id } }
    if (thinking !== undefined) agent.thinkingLevel = thinking
    const conversation = await harness.root(context, { agent })
    const submission = await conversation.submit({ type: "input", content: "hello" }, context)
    await submission.wait(context)
    await harness.close(context)
    return seen
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

for (const thinking of [undefined, "high"]) {
  test(`a provider receives Pi's stream options in Pi's key order (thinking ${thinking ?? "default"})`, async () => {
    const pi = await optionKeys(impls[0]!, thinking)
    const candidate = await optionKeys(impls[1]!, thinking)
    assert.equal(pi.length, 1, "one model call")
    assert.ok(pi[0]!.indexOf("signal") < pi[0]!.indexOf("sessionId"), `Pi passes signal before sessionId: ${pi[0]}`)
    assert.deepEqual(candidate.map(k => k.join(",")), pi.map(k => k.join(",")))
  })
}
