// The core frees every origin it was given (ABI section 5.4): after turns that were submitted in contexts of their own, and the work
// they started has ended, the host holds no async context, and each turn's model calls and tool ran in the context of its submit.
import assert from "node:assert/strict"
import { AsyncLocalStorage } from "node:async_hooks"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import * as Pi from "@earendil-works/pi-durable"
import { openNodeSqliteStorage as piSqlite } from "@earendil-works/pi-durable/storage/sqlite/node"
import { createRegistry, defineExtension, defineTool } from "../src/facade/index.ts"
import { Harness } from "../src/facade/harness.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { WasmCore } from "../src/wasm.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const als = new AsyncLocalStorage<string>()

const module = new (WebAssembly.Module as unknown as new (b: Uint8Array) => WebAssembly.Module)(readFileSync(join(root, "dist", `core-${toolchain}.wasm`)))

/** Four turns, each submitted in a context of its own, with a tool round in each: what every model call and tool saw, and the contexts the host still held at the end. */
async function run(impl: "pi" | "candidate") {
  const faux = fauxProvider({ models: [{ id: "o-1" }], tokenSize: { min: 1e9, max: 1e9 } })
  const modelSaw: (string | undefined)[] = []
  const toolSaw: (string | undefined)[] = []
  const respond = async (transcript: { messages: { role: string }[] }) => {
    faux.appendResponses([respond as never])
    modelSaw.push(als.getStore())
    const lastUser = transcript.messages.findLastIndex(m => m.role === "user")
    const results = transcript.messages.slice(lastUser + 1).filter(m => m.role === "toolResult").length
    return results < 1 ? fauxAssistantMessage(fauxToolCall("probe", {}), { stopReason: "toolUse" }) : fauxAssistantMessage("done")
  }
  faux.setResponses([respond as never])
  const models = createModels()
  models.setProvider(faux.provider)
  const execute = async () => { toolSaw.push(als.getStore()); return { output: [{ type: "text", text: "ok" }] } }
  const spec = { name: "probe", description: "records its context", parameters: Type.Object({}), execute }
  const path = join(tempDir("origins"), "h.sqlite")
  let harness: any
  if (impl === "pi") {
    const registry = Pi.createRegistry()
    registry.install(Pi.defineExtension({ name: "o", tools: [Pi.defineTool(spec as any)] }))
    harness = await Pi.Harness.open(await piSqlite(path), { models, registry } as any, context)
  } else {
    const registry = createRegistry()
    registry.install(defineExtension({ name: "o", tools: [defineTool(spec as any)] }))
    const core = new WasmCore(module)
    harness = await Harness.open(await openNodeSqliteStorage(path), { models, registry, core: { newSession: () => core.newSession(), sqlTable: core.sqlTable } } as any, context)
  }
  const conversation = await harness.root(context, { agent: { model: { provider: faux.getModel().provider, modelId: faux.getModel().id } } })
  for (let i = 0; i < 4; i++) {
    const submission = await als.run(`turn-${i}`, () => conversation.submit({ type: "input", content: `hello ${i}` }, context))
    await submission.wait(context)
  }
  await harness.waitForIdle(context)
  for (let i = 0; i < 20; i++) await new Promise(resolve => setImmediate(resolve))
  const held = impl === "pi" ? 0 : (harness as { host: { originsHeld: number } }).host.originsHeld
  await harness.close(context)
  return { modelSaw, toolSaw, held }
}

test("turns submitted in their own contexts run in the contexts Pi gives them, and the host holds no context once the work ended", async () => {
  const pi = await run("pi")
  const candidate = await run("candidate")
  assert.equal(pi.modelSaw.length, 8, "Pi: a tool round and an answer in each of four turns")
  assert.equal(pi.modelSaw[0], "turn-0", "Pi: the first turn runs in the context it was submitted in")
  assert.deepEqual(candidate.modelSaw, pi.modelSaw)
  assert.deepEqual(candidate.toolSaw, pi.toolSaw)
  assert.equal(candidate.held, 0, "every origin was freed")
})
