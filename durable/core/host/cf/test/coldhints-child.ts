// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// The process coldhints.test.ts traces: compile the core ($DCORE_WASM) as a Node host does, then, unless
// COLDHINTS_COMPILE_ONLY is set, run open and one eight-lookup turn (W1's shape) on an empty store. The test compares the
// functions V8 reports compiling in the two runs.
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { compileCoreModule } from "../src/compile-hints.ts"
import { Harness } from "../src/facade/harness.ts"
import { createRegistry, defineExtension, defineTool } from "../src/facade/index.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { tempDir } from "./helpers.ts"

const module = await compileCoreModule(readFileSync(process.env.DCORE_WASM!), process.env.DURABLE_COMPILE_HINTS === "1")
const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
let calls = 0
const lookup = defineTool({ name: "lookup", description: "Look up record number n", parameters: Type.Object({ n: Type.Number() }), execute: async ({ n }: { n: number }) => ({ output: [{ type: "text", text: `record ${n}` }] }) })
const registry = createRegistry()
registry.install(defineExtension({ name: "bench", tools: [lookup] }))
const respond = (transcript: { messages: { role: string }[] }): ReturnType<typeof fauxAssistantMessage> => {
  faux.appendResponses([respond as never])
  const lastUser = transcript.messages.findLastIndex(m => m.role === "user")
  const done = transcript.messages.slice(lastUser + 1).filter(m => m.role === "toolResult").length
  return done < 8 ? fauxAssistantMessage(fauxToolCall("lookup", { n: ++calls }, { id: `c${calls}` }), { stopReason: "toolUse" }) : fauxAssistantMessage("done")
}
faux.setResponses([respond as never])
if (process.env.COLDHINTS_COMPILE_ONLY) process.exit(0)
const harness = await Harness.open(await openNodeSqliteStorage(join(tempDir("coldhints"), "h.sqlite")), { models, registry, core: module } as never, context)
const root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
const submission = await root.submit({ type: "input", content: "turn 0" }, context)
await submission.wait(context)
await root.waitForIdle(context)
await harness.close(context)
process.exit(0)
