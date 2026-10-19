// A model call carries the async context of the code Pi runs it from, as Pi Durable's in-process scheduler does: a store set around
// submit reaches the turn's model call, and a turn that Harness.open resumes runs in open's context, whatever context a later host call
// was made in. AsyncLocalStorage propagation is observable to a Pi program, so the host must not run core work in the context of
// whichever call happened to schedule its pump (the wasm-go crash flake at k=52/53: a resumed turn inherited a deferred fetch's context).
import assert from "node:assert/strict"
import { AsyncLocalStorage } from "node:async_hooks"
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
const als = new AsyncLocalStorage<string>()
const module = new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(join(root, "dist", `core-${toolchain}.wasm`)))

/** The conversation the "noop" tool submits a follow-up into, inside als.run("follow-up"), when set. */
const followUp: { conversation?: any } = {}
const toolSpec = {
  name: "noop",
  description: "does nothing",
  parameters: { type: "object" },
  execute: async () => {
    const conversation = followUp.conversation
    followUp.conversation = undefined
    if (conversation) await als.run("follow-up", () => conversation.submit({ type: "input", content: "follow up" }, context))
    return { output: [{ type: "text", text: "done" }] }
  },
}
const piRegistry = () => { const r = Pi.createRegistry(); r.install(Pi.defineExtension({ name: "als", tools: [Pi.defineTool(toolSpec as any)] })); return r }
const candidateRegistry = () => { const r = createRegistry(); r.install(defineExtension({ name: "als", tools: [defineTool(toolSpec as any)] })); return r }

type Impl = { name: string; open: (path: string, models: unknown, extra?: object) => Promise<any> }
const impls: Impl[] = [
  { name: "pi", open: async (path, models, extra) => Pi.Harness.open(await piSqlite(path), { models, registry: piRegistry(), ...extra } as any, context) },
  {
    name: "candidate",
    open: async (path, models, extra) => {
      const core = new WasmCore(module)
      return Harness.open(await openNodeSqliteStorage(path), { models, registry: candidateRegistry(), core: { newSession: () => core.newSession(), sqlTable: core.sqlTable }, ...extra } as any, context)
    },
  },
]

/** A faux model whose every call records the async context it runs in; `hold` makes the next answer wait forever. */
function recordingModels() {
  const faux = fauxProvider({ models: [{ id: "als-1" }], tokenSize: { min: 1e9, max: 1e9 } })
  const seen: (string | undefined)[] = []
  let wrapped = false
  const state = { hold: false, tools: 0 }
  const respond = async (_context: unknown, options?: { signal?: AbortSignal }) => {
    faux.appendResponses([respond])
    if (!wrapped) seen.push(als.getStore())
    if (state.hold) await new Promise((_, reject) => options?.signal?.addEventListener("abort", () => reject(new Error("aborted")), { once: true }))
    if (state.tools > 0) { state.tools--; return fauxAssistantMessage([fauxToolCall("noop", {})], { stopReason: "toolUse" }) }
    return fauxAssistantMessage("answer")
  }
  faux.setResponses([respond])
  const models = createModels()
  models.setProvider(faux.provider)
  /** Run every model call inside `als.run(store)`, as the contract tape runs completeSimple and fetchDeferred in its own context;
   * the call records the context it was made in first. */
  const wrapIn = (store: string) => {
    const call = models.streamSimple.bind(models)
    wrapped = true
    ;(models as any).streamSimple = (...args: unknown[]) => { seen.push(als.getStore()); return als.run(store, () => (call as any)(...args)) }
  }
  return { models, seen, state, wrapIn, model: { provider: faux.getModel().provider, modelId: faux.getModel().id } }
}

/** What each implementation's model calls saw, for one program. */
async function observe(program: (impl: Impl, path: string) => Promise<(string | undefined)[]>) {
  const out = {} as { pi: (string | undefined)[]; candidate: (string | undefined)[] }
  for (const impl of impls) {
    const dir = tempDir("als")
    try { (out as Record<string, (string | undefined)[]>)[impl.name] = await program(impl, join(dir, "store.sqlite")) } finally { rmSync(dir, { recursive: true, force: true }) }
  }
  return out
}

test("a store set around submit reaches the turn's model call, as in Pi", async () => {
  const got = await observe(async (impl, path) => {
    const r = recordingModels()
    const harness = await impl.open(path, r.models)
    const conversation = await harness.root(context, { agent: { model: r.model } })
    const submission = await als.run("submit", () => conversation.submit({ type: "input", content: "hello" }, context))
    await submission.wait(context)
    await harness.close(context)
    return r.seen
  })
  assert.deepEqual(got.pi, ["submit"], "Pi's model call runs in submit's context")
  assert.deepEqual(got.candidate, got.pi)
})

test("a turn that Harness.open resumes runs in open's context, not in the context of a host call made after open", async () => {
  const got = await observe(async (impl, path) => {
    const first = recordingModels()
    first.state.hold = true
    const harness = await impl.open(path, first.models)
    const conversation = await harness.root(context, { agent: { model: first.model } })
    await conversation.submit({ type: "input", content: "hello" }, context)
    while (first.seen.length === 0) await new Promise(resolve => setImmediate(resolve))
    await harness.close(context)
    const second = recordingModels()
    const reopened = await impl.open(path, second.models)
    await als.run("later", () => reopened.inspect(context))
    await reopened.waitForIdle(context)
    await reopened.close(context)
    return second.seen
  })
  assert.equal(got.pi.length, 1, "Pi resumes the held turn once")
  assert.deepEqual(got.candidate, got.pi)
})

test("a model call made inside another context does not lend that context to the next model call of the same turn", async () => {
  const got = await observe(async (impl, path) => {
    const r = recordingModels()
    r.state.tools = 3
    r.wrapIn("model")
    const harness = await impl.open(path, r.models)
    const conversation = await harness.root(context, { agent: { model: r.model } })
    const submission = await conversation.submit({ type: "input", content: "hello" }, context)
    await submission.wait(context)
    await harness.close(context)
    return r.seen
  })
  assert.equal(got.pi.length, 4, "Pi makes one model call per tool round and one for the answer")
  assert.deepEqual(got.candidate, got.pi)
})

/** Six long turns with example 25's compaction settings; each model call records whether it summarizes and the async context it
 * sees. completeSimple (the summary call) runs inside `als.run("summary")`, as the contract tape runs it in its own context. */
async function compactingTurns(impl: Impl, path: string): Promise<string[]> {
  const faux = fauxProvider({ models: [{ id: "als-c", contextWindow: 3000, maxTokens: 1000 }], tokenSize: { min: 1e9, max: 1e9 } })
  const seen: string[] = []
  const respond = async (ctx: any) => {
    faux.appendResponses([respond])
    const summarizing = ctx.messages.some((m: any) => m.role === "system" && JSON.stringify(m.content).includes("summarization"))
    seen.push(`${summarizing ? "summary" : "turn"}:${als.getStore() ?? "-"}`)
    return fauxAssistantMessage(summarizing ? "Summary." : "answer " + "details ".repeat(200))
  }
  faux.setResponses([respond])
  const models = createModels()
  models.setProvider(faux.provider)
  const complete = (models as any).completeSimple.bind(models)
  ;(models as any).completeSimple = (...args: unknown[]) => als.run("summary", () => complete(...args))
  const settings = { compaction: { reserveTokens: 1000, keepRecentTokens: 400, backgroundTokens: 800 } }
  const harness = await impl.open(path, models, { settings })
  const conversation = await harness.root(context, { agent: { model: { provider: faux.getModel().provider, modelId: faux.getModel().id } } })
  for (let i = 0; i < 6; i++) await (await conversation.submit({ type: "input", content: `question ${i}` }, context)).wait(context)
  await harness.waitForIdle(context)
  await harness.close(context)
  return seen
}
let compacting: ReturnType<typeof observe> | undefined
const compactingRun = () => (compacting ??= observe(compactingTurns))

test("a compaction summary made inside another context does not lend it to the turns that follow, as in Pi", async () => {
  const got = await compactingRun()
  assert.ok(got.pi.some(s => s?.startsWith("summary")), "Pi compacts")
  const contexts = (calls: (string | undefined)[]) => [...new Set(calls)].sort()
  assert.deepEqual(contexts(got.pi), ["summary:summary", "turn:-"], "Pi's turns see no store, its summaries see the summary's")
  assert.deepEqual(contexts(got.candidate), contexts(got.pi))
})

// Six long turns compact three times in Pi 1.2.0 and later (t,t,t,S,t,S,t,S,t); the oracle was npm Pi 1.0.4 (two compactions) until the
// host's pi-* packages moved to 1.3.0. Independent of the async context: the same without the completeSimple wrapper.
test("six long turns with example 25's compaction settings compact as often as Pi's", async () => {
  const got = await compactingRun()
  const kinds = (calls: (string | undefined)[]) => calls.map(s => s?.split(":")[0])
  assert.deepEqual(kinds(got.candidate), kinds(got.pi))
})

test("an input submitted from inside a tool, in another context, reaches its turn's model call in the context Pi gives it", async () => {
  const got = await observe(async (impl, path) => {
    const r = recordingModels()
    r.state.tools = 1
    const harness = await impl.open(path, r.models)
    const conversation = await harness.root(context, { agent: { model: r.model } })
    followUp.conversation = conversation
    await (await conversation.submit({ type: "input", content: "hello" }, context)).wait(context)
    await harness.waitForIdle(context)
    await harness.close(context)
    return r.seen
  })
  assert.equal(got.pi.length, 3, "Pi: the tool round, the answer, the follow-up's answer")
  assert.deepEqual(got.candidate, got.pi)
})
