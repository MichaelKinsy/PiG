// The microtask passes of a warm turn against the reference's numbers: five turns of eight sequential lookups on the bench workload (a scripted model, the
// `lookup` tool and a `preamble` prompt section), each turn's `submit` and `wait` counted as the contract rows F-ticks-warm-node and F-ticks-warm-gate count them
// (durable/contract/scenarios/warm-ticks.mjs). Every number below is the reference run's printed count, Pi 1.5.0+c23b9ec1's Harness under the contract hooks:
// `node durable/contract/cli/contract.mjs run F-ticks-warm-node --impl native --out DIR`, DIR/ref/F-ticks-warm-node.trace (Pi) against DIR/native/ (the core).
// The wait of a turn is the sum of every await of Pi's turn: the section render of each of its nine model calls once spent four passes more than Pi's `await
// section.render()` (the host resolved the agent again for the section's input, a Session read generation.ts does not make), +36 per turn that the cold
// submit and the short wait of ticks.test.ts do not show.
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "typebox"
import type { AssistantMessage } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { Harness } from "../src/facade/harness.ts"
import { createRegistry, defineExtension, defineTool, section } from "../src/facade/index.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const LOOKUPS = 8, TURNS = 5

/** The `submit` and `wait` passes of each of the turns, as scenarios/warm-ticks.mjs counts them. */
async function warmTurns(storageProfile: "node-sqlite" | "gate-capsql"): Promise<{ submit: number[]; wait: number[] }> {
  const module = new (WebAssembly.Module as unknown as new (b: Uint8Array) => WebAssembly.Module)(readFileSync(join(root, "dist", `core-${toolchain}.wasm`)))
  const faux = fauxProvider({ models: [{ id: "scripted-1", contextWindow: 1e9 }], tokenSize: { min: 1e9, max: 1e9 } })
  const models = createModels()
  models.setProvider(faux.provider)
  let calls = 0
  const record = (n: number) => { const head = `record ${n}: `; return head + "x".repeat((n % 97 === 0 ? 8192 : 256) - head.length) }
  const lookup = defineTool({ name: "lookup", description: "Look up record number n", parameters: Type.Object({ n: Type.Number() }), execute: async ({ n }: { n: number }) => ({ output: [{ type: "text", text: record(n) }] }) })
  const registry = createRegistry()
  registry.install(defineExtension({ name: "bench", tools: [lookup], sections: [section("preamble", () => "You are a benchmark agent. Call lookup as instructed, then answer briefly.", { tag: false })] }))
  const respond = (transcript: { messages: { role: string }[] }): AssistantMessage => {
    faux.appendResponses([respond as never])
    const lastUser = transcript.messages.findLastIndex(m => m.role === "user")
    const done = transcript.messages.slice(lastUser + 1).filter(m => m.role === "toolResult").length
    return done < LOOKUPS ? fauxAssistantMessage(fauxToolCall("lookup", { n: ++calls }, { id: `call-${calls}` }), { stopReason: "toolUse" }) : fauxAssistantMessage("done")
  }
  faux.setResponses([respond as never])
  const harness = await Harness.open(await openNodeSqliteStorage(join(tempDir("warm-ticks"), "h.sqlite")), { models, registry, core: module, storageProfile } as never, context)
  const model = faux.getModel()
  const r = await harness.root(context, { agent: { model: { provider: model.provider, modelId: model.id } } })
  let passes = 0, counting = false
  const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
  const measure = async <T,>(call: () => Promise<T>): Promise<[T, number]> => { passes = 0; counting = true; queueMicrotask(spin); const result = await call(); counting = false; return [result, passes] }
  const out = { submit: [] as number[], wait: [] as number[] }
  for (let i = 0; i < TURNS; i++) {
    const [submission, submit] = await measure(() => r.submit({ type: "input", content: `turn m${i} tools=${LOOKUPS}`, requestId: `m${i}` }, context))
    const [, wait] = await measure(() => submission.wait(context))
    out.submit.push(submit)
    out.wait.push(wait)
    await r.waitForIdle(context)
    // The scenario's `settle`: a real timer lets the turn's background work finish before the next count (a deterministic clock would hide the work it waits for).
    const { promise, resolve } = Promise.withResolvers<void>()
    setTimeout(resolve, 10)
    await promise
  }
  await harness.close(context)
  return out
}

// Pi c23b9ec1: `submit (turn N)` and `wait (turn N)` of F-ticks-warm-node. The first turn's wait is 19 more than a warm one's (the cold conversation's reads).
test("a warm turn of eight lookups spends the passes Pi's turn spends (node SQLite profile)", async () => {
  const { submit, wait } = await warmTurns("node-sqlite")
  assert.deepEqual(submit, [104, 104, 104, 104, 104])
  assert.deepEqual(wait, [6939, 6920, 6920, 6920, 6920])
})

// Pi c23b9ec1: F-ticks-warm-gate (SqliteStorage over the contract gate's capsql facade, the profile "gate-capsql" as ticks.test.ts counts it).
test("a warm turn of eight lookups spends the passes Pi's turn spends (gate profile)", async () => {
  const { submit, wait } = await warmTurns("gate-capsql")
  assert.deepEqual(submit, [106, 106, 106, 106, 106])
  assert.deepEqual(wait, [6878, 6853, 6853, 6853, 6853])
})
