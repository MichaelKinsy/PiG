// The pi-durable side of the durable-bench rows (W1-W4, X1, H) as a scenario program: open a store (a copy of a fixture),
// run N measured turns of the scripted model and tool, print what a client observes. Run by lib/run.mjs under the hooks.
//   bench.mjs --db <store.sqlite> [--seed N] [--turns N] [--tools 8] [--variant full|incr] [--stream chars] [--partial-ms 100]
//             [--recover] [--parallel N] [--compaction] [--label m]
//   --seed N        write N turns of history (h0..h<N-1>, tools cycle 1,1,0) before the measured turns
//   --recover       open the store, let the interrupted turn finish, and stop (the second run of a crash handoff)
//   --parallel      each measured turn announces its `tools` lookups in one round (W4d) instead of one after another
//   --fault retryable-error   W4g: the second model call of the process fails with a retryable provider error, then succeeds
//   --compaction --window N   W4a seed: compaction on, a model with an N-token window; head markers then bound the active range
//   --subagent      W4e: each measured turn delegates to a foreground sub-agent (a child conversation the tool call owns)
//   --payload-kb K  every lookup result is K KiB (W4c)
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { AssistantEntry, configure, createRegistry, defineExtension, defineTool, Harness, section } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"
import { fingerprint, history, MEASURED_TOOLS, payload, scriptedModel, seenOf, SYSTEM, turn } from "../lib/workload.mjs"

const argv = process.argv.slice(2)
const arg = (name, fallback) => { const i = argv.indexOf(`--${name}`); return i < 0 ? fallback : argv[i + 1] }
const flag = name => argv.includes(`--${name}`)
const db = arg("db"), seed = Number(arg("seed", "0")), turns = Number(arg("turns", "0")), tools = Number(arg("tools", String(MEASURED_TOOLS)))
const variant = arg("variant", "full"), stream = Number(arg("stream", "0")), partialMs = arg("partial-ms")
const label = arg("label", "m"), payloadKb = Number(arg("payload-kb", "0"))

const script = scriptedModel({ fauxProvider, fauxAssistantMessage, fauxToolCall, createModels }, { variant, streaming: stream > 0, fault: arg("fault"), contextWindow: Number(arg("window", "0")) || undefined })
// The bench fingerprint of every model call goes into the trace next to the ctx fingerprint (CONTRACT section 5.4).
globalThis[Symbol.for("pig.contract.benchfp")] = messages => fingerprint(seenOf(messages))
const lookup = defineTool({ name: "lookup", description: "Look up record number n", parameters: Type.Object({ n: Type.Number() }), execute: async ({ n }) => ({ content: [{ type: "text", text: payload(n, payloadKb) }] }) })
const registry = createRegistry()
registry.install(defineExtension({ name: "bench", tools: [lookup], sections: [section("preamble", () => SYSTEM, { tag: false })] }))
// W4e: the foreground sub-agent tool of example 22. The child conversation is owned by the tool call's task; a rerun after a
// crash finds the child and the submission it already made.
const Subagent = defineExtension({
	name: "subagent",
	tools: [defineTool({
		name: "subagent",
		description: "Delegate a self-contained task to a subagent and get its answer back.",
		parameters: Type.Object({ task: Type.String() }),
		replay: "safe",
		execute: async (args, api, callContext) => {
			const child = await api.commit(async tx => {
				const existing = (await tx.scanConversations({ ownerTaskId: api.taskId }, 1)).items[0]
				if (existing !== undefined) return existing.id
				const created = await tx.createConversation({ ownership: { kind: "task", taskId: api.taskId } })
				await configure(tx, created.id, { extensions: { remove: [Subagent] } })
				return created.id
			}, callContext)
			await api.details({ conversationId: child }, callContext)
			const handle = await api.conversation(child, callContext)
			const settled = await (await handle.submit({ type: "input", content: args.task, requestId: `subagent:${api.taskId}` }, callContext)).wait(callContext)
			if (settled.status !== "done" || settled.type !== "input") throw new Error(`Subagent failed: ${settled.status}`)
			const entry = await api.commit(tx => tx.entry(AssistantEntry, settled.answer), callContext)
			const text = entry.model[0].content.flatMap(c => (c.type === "text" ? [c.text] : [])).join("")
			return { content: [{ type: "text", text }], details: { conversationId: child } }
		},
	})],
})
if (flag("subagent")) registry.install(Subagent)

const settings = { compaction: { enabled: flag("compaction") }, ...(partialMs ? { progress: { partialIntervalMs: Number(partialMs) } } : {}) }
const harness = await Harness.open(await openNodeSqliteStorage(db), { models: script.models, registry, settings }, context)
const model = script.model()
const root = await harness.root(context, { agent: { model: { provider: model.provider, modelId: model.id } } })

async function run(t) {
	const submission = await root.submit({ type: "input", content: t.text, requestId: t.id }, context)
	const settled = await submission.wait(context)
	if (settled.status !== "done") throw new Error(JSON.stringify(settled))
	await root.waitForIdle(context)
}

if (flag("recover")) await root.waitForIdle(context)
for (const t of history(0, seed)) await run(t)
for (let i = 0; i < turns; i++) await run({ ...turn(`${label}${i}`, tools), text: `turn ${label}${i} tools=${tools}${stream ? ` stream=${stream}` : ""}${flag("parallel") ? " parallel=1" : ""}${flag("subagent") ? " subagent=1" : ""}` })
// A background compaction may still be running; a fixture is written at rest.
if (flag("compaction")) for (const task of (await harness.inspect(context)).tasks) if (task.record.state.status !== "terminal") await harness.waitForTask(task.record.id, context)
await harness.close(context)
console.log(JSON.stringify({ seeded: seed, turns, calls: script.state.calls, bench: script.benchFingerprint() }))
