// transaction.ts submission and submissionByRequest read through host.storage: a Storage that overrides either method is asked for
// each call a transaction makes (the core's storageGate), as it is for conversation, entry and task.
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { execFileSync } from "node:child_process"
import { existsSync } from "node:fs"
import { join } from "node:path"
import { createRegistry, Harness, MemoryStorage } from "../src/facade/index.ts"
import { root, toolchain } from "./helpers.ts"

const context = BACKGROUND_CONTEXT
const wasm = join(root, "dist", `core-${toolchain}.wasm`)

class CountingStorage extends MemoryStorage {
  calls: string[] = []
  override async submission(id: never, ctx: never) {
    this.calls.push(`submission:${id}`)
    return super.submission(id, ctx)
  }
  override async submissionByRequest(conversationId: never, requestId: string, ctx: never) {
    this.calls.push(`submissionByRequest:${conversationId}:${requestId}`)
    return super.submissionByRequest(conversationId, requestId, ctx)
  }
}

test("Tx.submission and Tx.submissionByRequest ask an overriding Storage", async () => {
  if (!existsSync(wasm)) execFileSync(join(root, "..", "..", "budget", "build-wasm.sh"), [toolchain, join(root, "..", "..", "cmd", "corewasm"), wasm], { stdio: "inherit" })
  process.env.DCORE_WASM = wasm
  const storage = new CountingStorage()
  const models = createModels()
  models.setProvider(fauxProvider().provider)
  const harness = await Harness.open(storage, { models: models as any, registry: createRegistry(), onReport: () => {} } as any, context)
  const conversation = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } } as any)
  storage.calls.length = 0
  const found = await conversation.commit(async tx => [await tx.submission(9999 as never), await tx.submissionByRequest(conversation.id, "r1")], context)
  assert.deepEqual(found, [undefined, undefined])
  assert.deepEqual(storage.calls, ["submission:9999", `submissionByRequest:${conversation.id}:r1`])
})
