// The tick cost of a tool's streamed output and of its result (rows F-ticks-output-<profile>): how many microtask passes lie between the
// tool's `api.output(chunk)` call and the event that reports it, and between the tool's return and the event that reports its end, on an
// event stream attached to the conversation. Pi orders concurrent commits by Promise hops (CONTRACT 5.8): a streamed chunk that publishes
// later than Pi's lands behind a sibling tool's completion (F-gate-11, commits 87/88). A self-requeuing microtask counts the passes; every
// wait is a tick or a detenv virtual timer. Run by lib/run.mjs under the hooks.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineExtension, defineTool, Harness, watchEvents } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

let passes = 0, counting = false, mark = 0
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const since = () => passes - mark
const log = text => console.log(text)

const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
const respond = transcript => {
	faux.appendResponses([respond])
	const done = transcript.messages.some(m => m.role === "toolResult")
	return done ? fauxAssistantMessage("done") : fauxAssistantMessage(fauxToolCall("streamer", {}, { id: "call-1" }), { stopReason: "toolUse" })
}
faux.setResponses([respond])
const streamer = defineTool({
	name: "streamer", description: "streams one chunk, waits, returns", parameters: Type.Object({}), replay: "safe",
	execute: async (_args, api) => {
		mark = passes; counting = true; queueMicrotask(spin)
		api.output("chunk 0\n")
		log("tool: output() returned")
		// The count stops at the first event (a spinning microtask would starve the timer below).
		while (counting) await new Promise(resolve => setImmediate(resolve))
		await new Promise(resolve => setTimeout(resolve, 100))
		counting = true; mark = passes; queueMicrotask(spin)
		log("tool: returning")
		return { output: [{ type: "text", text: "result" }] }
	},
})
const registry = createRegistry()
registry.install(defineExtension({ name: "output-ticks", tools: [streamer] }))
const harness = await Harness.open(await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "output-ticks.sqlite")), { models, registry }, context)
const root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
const stream = await watchEvents(harness, root.id, context)
stream.start(async events => {
	for (const e of events) if (counting) { log(`event ${e.type}${e.toolName ? ` ${e.toolName}` : ""} +${since()}`); counting = false }
})
const submission = await root.submit({ type: "input", content: "go" }, context)
await submission.wait(context)
counting = false
await stream.stop()
await harness.close(context)
