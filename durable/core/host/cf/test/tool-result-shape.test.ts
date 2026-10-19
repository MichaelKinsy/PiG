// A tool result has Pi 1.3.0's shape: the model reads `output`. A result in the 1.0.4 shape (`content`) is not translated: the call
// settles with the output its api reported (none here), as pi-durable 1.3.0's tool.ts finalResult does for a result without `output`.
// Each case runs on Pi and on the candidate; the tool message of the second model call and the stored tool-result entry must agree.
import assert from "node:assert/strict"
import { readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import * as Pi from "@earendil-works/pi-durable"
import { openNodeSqliteStorage as piSqlite } from "@earendil-works/pi-durable/storage/sqlite/node"
import { createRegistry, defineExtension, defineTool } from "../src/facade/index.ts"
import { Harness } from "../src/facade/harness.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { WasmCore } from "../src/wasm.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const context = BACKGROUND_CONTEXT
const module = new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(join(root, "dist", `core-${toolchain}.wasm`)))

type Result = Record<string, unknown>
type Opened = {
  root(context: unknown, options: { agent: { model: { provider: string; modelId: string } } }): Promise<{ submit(input: { type: "input"; content: string }, context: unknown): Promise<{ wait(context: unknown): Promise<unknown> }> }>
  waitForIdle(context: unknown): Promise<unknown>
  close(context: unknown): Promise<unknown>
}
const spec = (result: Result, report: string | undefined) => ({
  name: "tool",
  description: "returns a fixed result",
  parameters: { type: "object" },
  execute: async (_args: unknown, api: { output: (chunk: string) => void }) => {
    if (report !== undefined) api.output(report)
    return result
  },
})

/** What the model reads of the tool's result on the turn's second call, and the tool-result messages the conversation stores. */
async function run(impl: "pi" | "candidate", result: Result, report: string | undefined) {
  const dir = tempDir("shape")
  try {
    const faux = fauxProvider({ models: [{ id: "shape-1" }], tokenSize: { min: 1e9, max: 1e9 } })
    const toolMessages: unknown[] = []
    let calls = 0
    const respond = async (ctx: { messages: { role: string }[] }) => {
      if (calls++ === 0) return fauxAssistantMessage([fauxToolCall("tool", {})], { stopReason: "toolUse" })
      toolMessages.push(...ctx.messages.filter(m => m.role === "toolResult"))
      return fauxAssistantMessage("done")
    }
    faux.setResponses([respond, respond])
    const models = createModels()
    models.setProvider(faux.provider)
    const model = { provider: faux.getModel().provider, modelId: faux.getModel().id }
    const path = join(dir, "store.sqlite")
    let harness: Opened
    if (impl === "pi") {
      const registry = Pi.createRegistry()
      registry.install(Pi.defineExtension({ name: "shape", tools: [Pi.defineTool(spec(result, report) as unknown as Parameters<typeof Pi.defineTool>[0])] }))
      harness = await Pi.Harness.open(await piSqlite(path), { models, registry } as unknown as Parameters<typeof Pi.Harness.open>[1], context) as unknown as Opened
    } else {
      const registry = createRegistry()
      registry.install(defineExtension({ name: "shape", tools: [defineTool(spec(result, report) as unknown as Parameters<typeof defineTool>[0])] }))
      const core = new WasmCore(module)
      harness = await Harness.open(await openNodeSqliteStorage(path), { models, registry, core: { newSession: () => core.newSession(), sqlTable: core.sqlTable } } as unknown as Parameters<typeof Harness.open>[1], context) as unknown as Opened
    }
    const conversation = await harness.root(context, { agent: { model } })
    await (await conversation.submit({ type: "input", content: "go" }, context)).wait(context)
    await harness.waitForIdle(context)
    await harness.close(context)
    return JSON.stringify(toolMessages, (key, value) => (key === "timestamp" || key === "toolCallId" || key === "durationMs" ? undefined : value))
  } finally { rmSync(dir, { recursive: true, force: true }) }
}

const cases: [string, Result, string | undefined][] = [
  ["a result with `output` is what the model reads", { output: [{ type: "text", text: "new" }] }, undefined],
  ["a result in the 1.0.4 `content` shape yields an empty tool result", { content: [{ type: "text", text: "old" }] }, undefined],
  ["a result in the 1.0.4 shape keeps the output its api reported", { content: [{ type: "text", text: "old" }] }, "reported"],
  ["a result with both reads `output`", { content: [{ type: "text", text: "old" }], output: [{ type: "text", text: "new" }] }, undefined],
]
for (const [name, result, report] of cases) {
  test(`${name}, as in Pi`, async () => {
    const pi = await run("pi", result, report)
    assert.notEqual(pi, "[]", "the turn reached its second model call with a tool result")
    assert.equal(await run("candidate", result, report), pi)
  })
}

test("Pi 1.3.0 gives a tool result in the 1.0.4 shape no text", async () => {
  assert.doesNotMatch(await run("pi", { content: [{ type: "text", text: "old" }] }, undefined), /old/)
})
