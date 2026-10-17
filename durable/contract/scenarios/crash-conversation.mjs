// The crash scenario of `robust crash` (lib/robust.mjs): one scripted conversation that exercises tools, a foreground
// sub-agent, the inbox (a follow-up, a steer and a write that arrive while a turn runs), background compaction and a fork,
// written so that a process killed after any commit can be restarted with the same program and finishes the conversation.
// Every submission carries a requestId (a resubmission after a restart finds the first one), the fork is found by its parent
// before it is created, and the sub-agent finds the child conversation its tool call made. The model is a function of the
// transcript only. Run by lib/run.mjs under the hooks:  crash-conversation.mjs --db <store.sqlite>
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { AssistantEntry, configure, createRegistry, defineExtension, defineTool, Harness, LiveDoc } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const argv = process.argv.slice(2)
const arg = (name, fallback) => { const i = argv.indexOf(`--${name}`); return i < 0 ? fallback : argv[i + 1] }
const db = arg("db")
const log = (...parts) => console.log(parts.join(" "))

const blocksOf = m => (typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content)
const textOf = m => (m ? blocksOf(m).filter(b => b.type === "text").map(b => b.text).join("") : "")

// The model: a summary when asked to summarize; otherwise the last user message says what to do:
//   turn <id> tools=<k> [subagent] [hold]   k lookups one after another (after the sub-agent call), then an answer; `hold`
//                                           makes the first response of the turn take 50 virtual ms, so the inbox fills
//   task for <id>                           the sub-agent's child conversation: an answer
const faux = fauxProvider({ models: [{ id: "crash-1", contextWindow: 3000, maxTokens: 1000 }], tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
// --no-cache (robust recycle): the provider's prompt cache lives in the process, so a request the host repeats after it recycled its
// core hits it and reports other usage than the first attempt did, which moves the compaction thresholds. A restarted process has no
// such cache. The runs of a recycle comparison all send cacheRetention "none", so the repeat is the first attempt again.
if (argv.includes("--no-cache")) {
	const stream = models.streamSimple.bind(models)
	models.streamSimple = (model, context, options) => stream(model, context, { ...options, cacheRetention: "none" })
}
const respond = async transcript => {
	faux.appendResponses([respond])
	const messages = transcript.messages
	if (messages.some(m => m.role === "system" && textOf(m).startsWith("You are a context summarization assistant"))) return fauxAssistantMessage("Summary of the earlier turns.")
	const last = messages.findLastIndex(m => m.role === "user")
	const user = textOf(messages[last])
	const task = /^task for (\S+)/.exec(user)
	if (task) return fauxAssistantMessage(`child answer for ${task[1]}`)
	const id = /^turn (\S+)/.exec(user)?.[1] ?? "?"
	const planned = Number(/tools=(\d+)/.exec(user)?.[1] ?? 0)
	const after = messages.slice(last + 1)
	const calls = after.filter(m => m.role === "assistant").length
	const done = after.filter(m => m.role === "toolResult" && m.toolName === "lookup").length
	if (/\bhold\b/.test(user) && calls === 0) await new Promise(resolve => setTimeout(resolve, 50))
	if (/\bsubagent\b/.test(user) && calls === 0) return fauxAssistantMessage(fauxToolCall("subagent", { task: `task for ${id}` }, { id: `call-${id}-sub` }), { stopReason: "toolUse" })
	if (done < planned) return fauxAssistantMessage(fauxToolCall("lookup", { n: done + 1 }, { id: `call-${id}-${done}` }), { stopReason: "toolUse" })
	return fauxAssistantMessage(`done ${id} after ${done} lookups`)
}
faux.setResponses([respond])

const lookup = defineTool({
	name: "lookup",
	description: "Look up record number n",
	parameters: Type.Object({ n: Type.Number() }),
	execute: async ({ n }) => ({ output: [{ type: "text", text: `record ${n}: ${"x".repeat(1000)}` }] }),
})
// The foreground sub-agent of example 22 and durable-bench W4e: the child conversation is owned by the tool call's task, and a
// rerun after a crash finds the child and the submission it already made.
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
			const handle = await api.conversation(child, callContext)
			const settled = await (await handle.submit({ type: "input", content: args.task, requestId: `subagent:${api.taskId}` }, callContext)).wait(callContext)
			if (settled.status !== "done" || settled.type !== "input") throw new Error(`Subagent failed: ${settled.status}`)
			const entry = await api.commit(tx => tx.entry(AssistantEntry, settled.answer), callContext)
			return { output: [{ type: "text", text: entry.model[0].content.flatMap(c => (c.type === "text" ? [c.text] : [])).join("") }], details: { conversationId: child } }
		},
	})],
})
const registry = createRegistry()
registry.install(defineExtension({ name: "crash", tools: [lookup] }))
registry.install(Subagent)

// Compaction as example 25: a 3000-token window, so generation compacts in the background above 2000 - 800 tokens.
const settings = { followUpMode: "all", compaction: { reserveTokens: 1000, keepRecentTokens: 400, backgroundTokens: 800 } }
const harness = await Harness.open(await openNodeSqliteStorage(db), { models, registry, settings }, context)
const model = { provider: faux.getModel().provider, modelId: faux.getModel().id }
const root = await harness.root(context, { agent: { model } })

const submitted = new Map()
const submit = async (conversation, requestId, draft) => {
	const submission = await conversation.submit({ ...draft, requestId }, context)
	submitted.set(requestId, submission)
	return submission
}
const settle = async requestId => {
	const record = await submitted.get(requestId).wait(context)
	log("settled", requestId, record.status, record.status === "unanswered" ? record.reason : "")
}
/** Wait for every background compaction of a conversation (its summary is placed when the conversation is idle). */
const compactions = async conversation => {
	for (;;) {
		const running = (await harness.snapshot(LiveDoc, conversation.id, context))?.compactions ?? []
		if (running.length === 0) return
		for (const { taskId } of running) await harness.waitForTask(taskId, context)
		await conversation.waitForIdle(context)
	}
}
const say = async (conversation, id, text, extra = {}) => submit(conversation, id, { type: "input", content: `turn ${id} ${text}`, ...extra })

// 1. Tool turns.
await say(root, "t1", "tools=2"); await settle("t1")
// 2. A foreground sub-agent, then a lookup.
await say(root, "t2", "tools=1 subagent"); await settle("t2")
// 3. The inbox: the first answer of t3 takes 50 virtual ms; a follow-up, a steer and a write arrive meanwhile. The write carries a model
// message: a write of only kind and data is appended at once (Pi e6b9a6af), a write that affects model context queues.
await say(root, "t3", "tools=1 hold")
await say(root, "t4", "tools=1")
await say(root, "t5", "tools=0", { whenBusy: "steer" })
await submit(root, "w1", { type: "write", entry: { kind: "crash.note", data: "noted while busy", model: [{ role: "user", content: "noted while busy", timestamp: 0 }] } })
for (const id of ["t3", "t4", "t5", "w1"]) await settle(id)
await compactions(root)
// 4. More turns: the context crosses the background threshold again.
for (const id of ["t6", "t7"]) { await say(root, id, "tools=2"); await settle(id) }
await compactions(root)
// 5. A fork from the answer of t1, and a turn on it.
const answerOf = async (conversation, id) => (await conversation.entries({}, 200, undefined, context)).items.find(e => e.kind === "pi.assistant" && JSON.stringify(e).includes(`done ${id} `))
const parent = (await harness.commit(tx => tx.scanConversations({}, 50), context)).items.find(c => c.parent?.conversationId === root.id)
const fork = parent ? await harness.conversation(parent.id, context) : await root.fork((await answerOf(root, "t1")).id, { ownership: { kind: "ownerless" } }, context)
await say(fork, "f1", "tools=1"); await settle("f1")
await compactions(fork)
await harness.waitForIdle(context)

const conversations = (await harness.commit(tx => tx.scanConversations({}, 50), context)).items
for (const c of conversations) {
	const handle = await harness.conversation(c.id, context)
	const entries = (await handle.entries({}, 500, undefined, context)).items.reverse()
	log("conversation", c.id, c.parent ? `fork of ${c.parent.conversationId}` : c.owner ? `owned by ${c.owner.taskId}` : "root", entries.map(e => e.kind).join(","))
}
await harness.close(context)
