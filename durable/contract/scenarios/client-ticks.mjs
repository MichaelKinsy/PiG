// The client call tick costs (rows F-ticks-<profile>): how many microtask passes each public call of a Harness takes to settle, printed
// per call. Pi orders concurrent work by Promise hops (CONTRACT 5.8), so the number of hops a client call spends decides which commit it
// lands before: a candidate whose `submission.wait()` on a settled submission resolves in 1 pass where Pi spends 22-29 lets the script's next
// call overtake the commit Pi runs first (the F-* seeds that differ). The count is exact: a self-requeuing microtask counts the passes of
// the microtask queue while the call is pending, and every wait is a tick or a detenv virtual timer. Run by lib/run.mjs under the hooks.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, Harness } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
const respond = () => { faux.appendResponses([respond]); return fauxAssistantMessage("answer") }
faux.setResponses([respond])
const harness = await Harness.open(await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "ticks.sqlite")), { models, registry: createRegistry() }, context)
const root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const measure = async (name, call) => {
	passes = 0; counting = true; queueMicrotask(spin)
	const result = await call()
	counting = false
	console.log(`${name}: ${passes}`)
	return result
}
// A virtual timer between calls lets the previous call's background work finish, so each count starts from an idle Harness.
const settle = () => new Promise(resolve => setTimeout(resolve, 1000))

const first = await measure("submit (cold conversation)", () => root.submit({ type: "input", content: "first" }, context))
await measure("wait (running)", () => first.wait(context))
await settle()
await measure("wait (settled)", () => first.wait(context))
await measure("wait (settled, again)", () => first.wait(context))
await measure("entries", () => root.entries({}, 5, undefined, context))
await measure("waitForIdle (idle)", () => harness.waitForIdle(context))
await measure("inspect", () => harness.inspect(context))
const second = await measure("submit (idle conversation)", () => root.submit({ type: "input", content: "second" }, context))
await measure("abort (running)", () => second.abort(context))
await measure("wait (aborted or settled)", () => second.wait(context))
await settle()
await measure("snapshot of a missing document", () => harness.snapshot({ definition: { kind: "diff.none", version: 1, scope: "conversation", history: "latest", initial: () => ({}) } }, root.id, context).catch(() => undefined))
await harness.close(context)
