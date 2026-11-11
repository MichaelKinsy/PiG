// The microtask passes between the steps of three parallel tool calls (rows F-ticks-tools-<profile>): the pass at which each call's
// beforeTool hook, `execute`, return and afterTool hook runs, counted from the submission. Pi starts parallel tool tasks and runs their
// commits through Promise hops, so the gap between two calls' hooks decides whose hook-wait timer is created first and therefore which
// tool starts first (the F-gate seeds 26, 61, 67 and 77: a beforeTool wait of 8 passes plus 100 ms on call 0 and 1 pass plus 100 ms on
// call 1 starts call 0 first in Pi, call 1 first in a candidate whose second task starts fewer than 8 passes after the first).
// Every step is a count of a self-requeuing microtask, so the number is exact. Run by lib/run.mjs under the hooks.
// A self-requeuing microtask starves the event loop, and since Pi's writer publishes through `hop` (session/writer.ts: `await new Promise(setImmediate)`)
// the third call's execute waits for a macrotask: the count would never leave that wait (the reference ran at 100% CPU until its timeout killed it). The
// count therefore pauses while a setImmediate is pending, which lets the macrotask run, and goes on in the turn that ran it: a mark counts the passes
// of the microtasks around it, not the wait of a macrotask, which is the same for the reference and a candidate that queues its own macrotask there.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { obj } from "@earendil-works/pi-schema"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxText, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineExtension, defineTool, Harness, hook, ToolTask } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

let passes = 0, counting = false, immediates = 0, paused = false
const spin = () => {
	if (!counting) return
	if (immediates > 0) { paused = true; return }
	passes++
	queueMicrotask(spin)
}
const setImmediateOf = globalThis.setImmediate, clearImmediateOf = globalThis.clearImmediate
const pending = new Set()
const settled = () => { if (paused && immediates === 0) { paused = false; queueMicrotask(spin) } }
globalThis.setImmediate = (callback, ...args) => {
	immediates++
	const handle = setImmediateOf((...rest) => {
		pending.delete(handle)
		immediates--
		try { callback(...rest) } finally { settled() }
	}, ...args)
	pending.add(handle)
	return handle
}
// A cleared immediate never runs, so it stops counting as pending here.
globalThis.clearImmediate = handle => {
	if (pending.delete(handle)) { immediates--; settled() }
	clearImmediateOf(handle)
}
const mark = label => console.log(`${label}: ${passes}`)

const parameters = obj({})
const tool = defineTool({
	name: "probe", description: "probe", parameters, replay: "safe",
	execute: async (_args, api) => { mark(`execute ${api.callId}`); return { content: [{ type: "text", text: `ok ${api.callId}` }] } },
})
const hooks = [hook(ToolTask, {
	beforeTool: async call => { mark(`beforeTool ${call.id}`); return undefined },
	afterTool: async call => { mark(`afterTool ${call.id}`); return undefined },
})]
const registry = createRegistry()
registry.install(defineExtension({ name: "probe", tools: [tool], hooks }))

const faux = fauxProvider({ models: [{ id: "probe-1", contextWindow: 1e9 }], tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
const respond = transcript => {
	faux.appendResponses([respond])
	const assistants = transcript.messages.filter(m => m.role === "assistant").length
	return assistants === 0
		? fauxAssistantMessage([0, 1, 2].map(i => fauxToolCall("probe", {}, { id: `c${i}` })), { stopReason: "toolUse" })
		: fauxAssistantMessage([fauxText("done")])
}
faux.setResponses([respond])

const storage = await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "tool-ticks.sqlite"))
const harness = await Harness.open(storage, { models, registry, settings: { toolExecution: "parallel" } }, context)
const model = { provider: faux.getModel().provider, modelId: faux.getModel().id }
const root = await harness.root(context, { agent: { model } })

counting = true
queueMicrotask(spin)
const submission = await root.submit({ type: "input", content: "go" }, context)
mark("submitted")
await submission.wait(context)
mark("settled")
counting = false
await harness.close(context)
