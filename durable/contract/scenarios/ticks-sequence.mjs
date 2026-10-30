// The client call tick costs of durable/core/host/cf/test/ticks.test.ts, in that test's own sequence (rows F-ticks-seq-<profile>): the passes
// each call of a Harness spends, from a fresh conversation, counted as client-ticks.mjs counts them. The test pins the core's numbers; this
// scenario is where Pi's are measured (`node durable/contract/cli/contract.mjs run F-ticks-seq-gate --impl pi --out DIR`, the `out` lines of
// DIR/ref/F-ticks-seq-gate.trace), and the row compares the candidate's against them. Run by lib/run.mjs under the hooks.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, Harness } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
faux.setResponses([() => fauxAssistantMessage("answer")])
const harness = await Harness.open(await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "ticks.sqlite")), { models, registry: createRegistry() }, context)
const root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
let last
const measure = async (name, call) => {
	passes = 0; counting = true; queueMicrotask(spin)
	last = await call()
	counting = false
	console.log(`${name}: ${passes}`)
	return last
}

await measure("inspect (fresh)", () => harness.inspect(context))
await measure("conversation", () => harness.conversation(root.id, context))
const first = await measure("submit (cold conversation)", () => root.submit({ type: "input", content: "first" }, context))
await measure("wait (running)", () => first.wait(context))
await new Promise(resolve => setTimeout(resolve, 100))
await measure("wait (settled)", () => first.wait(context))
await measure("wait (settled, again)", () => first.wait(context))
await measure("entries", () => root.entries({}, 5, undefined, context))
await measure("getTask", () => harness.getTask(1, context))
await measure("getTask (missing)", () => harness.getTask(999, context))
await measure("status", () => first.status(context))
await measure("submission", () => harness.submission(first.id, context))
await measure("snapshot of a missing document", () => harness.snapshot({ definition: { kind: "diff.none", version: 1, scope: "conversation", history: "latest", initial: () => ({}) } }, root.id, context).catch(() => undefined))
await harness.close(context)
