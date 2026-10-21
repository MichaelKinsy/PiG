// Harness.wake() and Harness.retryTask() of Pi after 1.3.0 (harness.ts :282-285, :324-327) through the facade against a scripted core.
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { EVENT } from "../src/abi.ts"
import { createRegistry } from "../src/facade/index.ts"
import { MemoryStorage } from "../src/facade/storage.ts"
import { Harness } from "../src/facade/harness.ts"
import { apiResult, ScriptedCore } from "./scripted.ts"

const context = BACKGROUND_CONTEXT
const models = () => { const faux = fauxProvider(); const m = createModels(); m.setProvider(faux.provider); return m as any }

async function open(respond: ConstructorParameters<typeof ScriptedCore>[0]) {
  const core = new ScriptedCore(respond)
  const harness = await Harness.open(new MemoryStorage(), { models: models(), registry: createRegistry(), core: { newSession: () => core, sqlTable: [] } } as any, context)
  return { core, harness }
}

test("wake returns the core's answer synchronously, with and without an earliest sleep", async () => {
  let answer: { active: boolean; at?: number } = { active: true }
  const { core, harness } = await open(ev => ev.kind === EVENT.api && ev.body.op === "wake" ? { notices: [apiResult(ev.id!, { ok: answer })] } : undefined)
  assert.deepEqual(harness.wake(), { active: true })
  answer = { active: false, at: 1234 }
  assert.deepEqual(harness.wake(), { active: false, at: 1234 })
  assert.equal(core.ofKind(EVENT.api).filter(e => e.body.op === "wake").length, 2, "every call asks the core: the value is the scheduler's at that moment")
})

test("wake throws on a closed Harness, as assertUsable does", async () => {
  const { harness } = await open(() => undefined)
  await harness.close(context)
  assert.throws(() => harness.wake(), /Harness is closed/)
})

test("retryTask sends the task ID, resolves with undefined when the core answers, and rejects with the core's error for an unknown task", async () => {
  const { core, harness } = await open(ev => {
    if (ev.kind !== EVENT.api || ev.body.op !== "retryTask") return undefined
    return { notices: [apiResult(ev.id!, ev.body.taskId === 7 ? { ok: null } : { error: { name: "Error", message: `Task ${ev.body.taskId} does not exist` } })] }
  })
  assert.equal(await harness.retryTask(7, context), undefined)
  await assert.rejects(harness.retryTask(8, context), /Task 8 does not exist/)
  assert.deepEqual(core.ofKind(EVENT.api).filter(e => e.body.op === "retryTask").map(e => e.body.taskId), [7, 8])
})

test("retryTask throws synchronously on a closed Harness, not through the returned promise", async () => {
  const { harness } = await open(() => undefined)
  await harness.close(context)
  assert.throws(() => harness.retryTask(1, context), /Harness is closed/)
})
