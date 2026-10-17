// A failed Storage call fails the Session (Pi 42a, types.ts SessionEnd; test/harness-storage-failure.test.ts): the first error wins,
// the call that hit it rejects with the storage error itself, later calls reject `SessionFailed` with it as cause, waits end, and the
// watch and event stream close `session_failed` with the error. The scenario runs one conversation (a submit with a tool call, an
// event stream attached, a follow-up) on a Storage whose `commit` number --fail <k> throws, and prints what each caller saw. The
// store file left behind is compared by the gate like any other run, so a commit the failure must not have written differs too.
// Run by lib/run.mjs under the hooks:  storage-failure.mjs --fail <k>   (k = 0: no failure, prints the commit count)
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineExtension, defineTool, Harness, watchEvents } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const argv = process.argv.slice(2)
const failAt = Number(argv[argv.indexOf("--fail") + 1] ?? 0)
const log = (...parts) => console.log(parts.join(" "))
const textOf = m => (typeof m.content === "string" ? m.content : m.content.filter(b => b.type === "text").map(b => b.text).join(""))

const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
const respond = transcript => {
	faux.appendResponses([respond])
	const last = transcript.messages.findLast(m => m.role === "user")
	const done = transcript.messages.filter(m => m.role === "toolResult").length
	if (last && /lookup/.test(textOf(last)) && done === 0) return fauxAssistantMessage(fauxToolCall("lookup", { n: 1 }, { id: "call-1" }), { stopReason: "toolUse" })
	return fauxAssistantMessage(`answer after ${done} lookups`)
}
faux.setResponses([respond])
const lookup = defineTool({ name: "lookup", description: "Look up record n", parameters: Type.Object({ n: Type.Number() }), execute: async ({ n }) => ({ output: [{ type: "text", text: `record ${n}` }] }) })
const registry = createRegistry()
registry.install(defineExtension({ name: "sf", tools: [lookup] }))

const failure = new Error("disk gone")
let commits = 0
const storage = await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "storage-failure.sqlite"))
const failing = new Proxy(storage, { get(t, k, r) {
	const v = Reflect.get(t, k, r)
	if (k !== "commit" || typeof v !== "function") return typeof v === "function" ? v.bind(t) : v
	return async (...a) => { commits++; if (commits === failAt) throw failure; return v.apply(t, a) }
} })

const describe = e => (e === failure ? "the storage error" : e instanceof Error ? `${e.name}(cause ${e.cause === failure ? "the storage error" : String(e.cause)})` : String(e))
const outcome = async (name, p) => { try { const v = await p; log(`${name}: ok`) ; return v } catch (e) { log(`${name}: rejected ${describe(e)}`) } }
const ending = e => (e.reason === "failed" ? `failed(${e.error === failure ? "the storage error" : String(e.error)})` : e.reason)

const harness = await Harness.open(failing, { models, registry }, context)
const root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
const stream = await watchEvents(harness, root.id, context)
let batches = 0
stream.start(async () => { batches++ })
await outcome("first submit", root.submit({ type: "input", content: "lookup please" }, context))
await outcome("idle 1", harness.waitForIdle(context))
await outcome("second submit", root.submit({ type: "input", content: "and again" }, context))
await outcome("idle 2", harness.waitForIdle(context))
await outcome("get task", harness.getTask("t1", context))
const quiet = new Promise(r => setTimeout(() => r({ reason: "open" }), 20))
log("stream:", ending(await Promise.race([stream.closed.catch(e => ({ reason: `threw ${describe(e)}` })), quiet])))
log("harness:", ending(await Promise.race([harness.closed, quiet])))
log("commits seen:", failAt === 0 ? commits : "n/a", "events delivered:", batches > 0 ? "some" : "none")
await outcome("close", harness.close(context))
