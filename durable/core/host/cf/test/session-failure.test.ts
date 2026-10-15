// A failed Storage call fails the Session (Pi session.ts GuardedStorage #call, harness-storage-failure.test.ts) through the
// facade over the real core: the call that hit it rejects with the Storage's own error, every later call rejects SessionFailed
// with that cause, and `closed` ends `failed` with it once the Session has closed.
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, Harness, MemoryStorage, SessionFailed } from "../src/facade/index.ts"
import { execFileSync } from "node:child_process"
import { existsSync } from "node:fs"
import { join } from "node:path"
import { root, toolchain } from "./helpers.ts"

const context = BACKGROUND_CONTEXT
/** The production core, built on demand with the suite's toolchain, as the Harness finds it through DCORE_WASM. */
const wasm = join(root, "dist", `core-${toolchain}.wasm`)
const models = (faux = fauxProvider()) => { const m = createModels(); m.setProvider(faux.provider); return m as any }

class FailingStorage extends MemoryStorage {
  failing = false
  override async commit(writes: never, ctx: never) {
    if (this.failing) throw new Error("disk gone")
    return super.commit(writes, ctx)
  }
}

test("a failed commit rejects its caller with the Storage's error, later calls with SessionFailed, and `closed` ends failed", async () => {
  if (!existsSync(wasm)) execFileSync(join(root, "..", "..", "budget", "build-wasm.sh"), [toolchain, join(root, "..", "..", "cmd", "corewasm"), wasm], { stdio: "inherit" })
  process.env.DCORE_WASM = wasm
  const storage = new FailingStorage()
  const reports: unknown[] = []
  const harness = await Harness.open(storage, { models: models(), registry: createRegistry(), onReport: (e: unknown) => reports.push(e) } as any, context)
  const conversation = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } } as any)
  storage.failing = true
  await assert.rejects(conversation.commit(tx => tx.appendEntry(conversation.id, { kind: "message", model: [{ role: "user", content: "x", timestamp: 0 }] } as any), context), /disk gone/)
  await assert.rejects(harness.root(context, {} as any), (error: unknown) => error instanceof SessionFailed && (error.cause as Error)?.message === "disk gone")
  await assert.rejects(conversation.entries({}, 5, undefined, context), SessionFailed)
  const end = await (harness as unknown as { closed: Promise<{ reason: string; error?: Error }> }).closed
  assert.equal(end.reason, "failed")
  assert.equal(end.error?.message, "disk gone")
})

test("a wait on a turn in flight rejects SessionFailed when the turn's own commit fails the Session", async () => {
  if (!existsSync(wasm)) execFileSync(join(root, "..", "..", "budget", "build-wasm.sh"), [toolchain, join(root, "..", "..", "cmd", "corewasm"), wasm], { stdio: "inherit" })
  process.env.DCORE_WASM = wasm
  const storage = new FailingStorage()
  const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
  faux.setResponses([() => { storage.failing = true; return fauxAssistantMessage("answer") }])
  const harness = await Harness.open(storage, { models: models(faux), registry: createRegistry(), onReport: () => {} } as any, context)
  const conversation = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } } as any)
  const submission = await conversation.submit({ type: "input", content: "go" } as any, context)
  await assert.rejects(submission.wait(context), (error: unknown) => error instanceof SessionFailed && (error.cause as Error)?.message === "disk gone")
  assert.equal((await (harness as unknown as { closed: Promise<{ reason: string }> }).closed).reason, "failed")
})
