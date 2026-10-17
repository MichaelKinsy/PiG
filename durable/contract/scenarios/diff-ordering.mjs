// The differential ordering scenario (rows F-<profile>-<seed>): one seed draws a whole random schedule of parallel tools, hooks,
// observers and inbox arrivals, then plays it against a Harness on SQLite storage. Pi and a candidate play the same seed; the gate
// compares every stored row, every commit, every model context, everything the program prints and the order of API calls and
// commits (CONTRACT 2.1 and 5.8), so any difference in who runs first, who waits for whom, or which commit lands between two
// others fails the row. Run by lib/run.mjs under the hooks:  diff-ordering.mjs --seed N
//
// Every random choice is drawn before the Harness opens and printed as the first line (the plan). A choice made while the
// schedule runs would follow the run's order, and a candidate that ordered differently would then see different inputs and
// hide the difference instead of failing on it. Waits are virtual timers (detenv) and microtask ticks, so they are exact.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxText, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineDoc, defineExtension, defineTool, GenerationTask, Harness, hook, ToolTask, watchEvents } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const argv = process.argv.slice(2)
const arg = (name, fallback) => { const i = argv.indexOf(`--${name}`); return i < 0 ? fallback : argv[i + 1] }
const seed = Number(arg("seed", "1"))
if (!Number.isInteger(seed) || seed < 0) throw new Error(`--seed must be a non-negative integer, got ${arg("seed")}`)

// ─── The plan: every random choice ──────────────────────────────────────────

/** mulberry32: a 32-bit generator whose sequence is the same on every engine. */
function generator(s) {
	let a = (s ^ 0x9e3779b9) >>> 0
	return () => {
		a = (a + 0x6d2b79f5) >>> 0
		let t = a
		t = Math.imul(t ^ (t >>> 15), t | 1)
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296
	}
}
const rng = generator(seed)
const int = (lo, hi) => lo + Math.floor(rng() * (hi - lo + 1))
const pick = list => list[int(0, list.length - 1)]
const chance = p => rng() < p
/** A wait: microtask ticks first, then a virtual timer of `ms` (0 means none). */
const waitSpec = (tickMax = 8, msChoices = [0, 0, 0, 1, 2, 5, 10, 25, 50, 100]) => ({ ticks: int(0, tickMax), ms: pick(msChoices) })

const TOOLS = ["work", "commitwork", "streamer", "boom", "serial"]
const toolCall = id => ({ id, name: pick(TOOLS), ticks: int(0, 10), ms: pick([0, 0, 1, 5, 10, 20, 50, 100, 200]), n: int(1, 3) })

/** One user input: the planned rounds of tool calls the model announces, then its answer. */
let inputCount = 0
function inputPlan(conv, kind) {
	const id = `in${inputCount++}`
	const rounds = Array.from({ length: int(0, 3) }, (_, r) => Array.from({ length: int(1, 5) }, (_, c) => toolCall(`${id}-r${r}-c${c}`)))
	return { id, conv, kind, rounds, answer: `answer ${id} ${"lorem ipsum dolor ".repeat(int(1, 10)).trim()}` }
}

let conversationCount = chance(0.4) ? 2 : 1
const plan = {
	seed,
	streaming: chance(0.35),
	settings: { followUpMode: pick(["all", "one-at-a-time"]), toolExecution: chance(0.8) ? "parallel" : "sequential", partialIntervalMs: pick([50, 100, 250]) },
	inputs: [],
	// Hooks wait for a time drawn from the call (or request) they see, so the draw does not depend on the run's order.
	hooks: {
		beforeTool: chance(0.7) ? Array.from({ length: 4 }, () => ({ ...waitSpec(), block: chance(0.12) })) : undefined,
		afterTool: chance(0.6) ? Array.from({ length: 4 }, () => waitSpec()) : undefined,
		beforeRequest: chance(0.5) ? Array.from({ length: 3 }, () => waitSpec()) : undefined,
		afterResponse: chance(0.4) ? Array.from({ length: 3 }, () => waitSpec()) : undefined,
		afterTools: chance(0.4) ? Array.from({ length: 3 }, () => waitSpec()) : undefined,
	},
	observers: [],
	arrivals: [],
}
for (let c = 0; c < conversationCount; c++) plan.inputs.push({ ...inputPlan(c, "start"), start: waitSpec(4, [0, 0, 1, 5, 20]) })
for (let o = 0, n = int(0, 3); o < n; o++) plan.observers.push({ kind: pick(["events", "view"]), conv: int(0, conversationCount - 1), at: waitSpec(6, [0, 1, 5, 20, 100, 300]), listener: waitSpec(4, [0, 0, 5, 30]) })
for (let a = 0, n = int(0, 5); a < n; a++) {
	const kind = pick(["followUp", "followUp", "steer", "write", "reject"])
	plan.arrivals.push({ ...inputPlan(int(0, conversationCount - 1), kind), at: waitSpec(6, [0, 1, 5, 10, 25, 50, 100, 200, 400]), withdraw: chance(0.25) ? waitSpec(4, [0, 0, 1, 10]) : undefined })
}
// Reductions: every draw above is made, then the plan is cut down.
const csv = (name) => { const v = arg(name); return v === undefined ? undefined : v === "none" ? [] : v.split(",") }
const keepHooks = csv("hooks"), keepObservers = csv("observers"), keepArrivals = csv("arrivals")
if (keepHooks) for (const name of Object.keys(plan.hooks)) if (!keepHooks.includes(name)) plan.hooks[name] = undefined
if (keepObservers) plan.observers = plan.observers.filter((_, i) => keepObservers.includes(String(i)))
if (keepArrivals) plan.arrivals = plan.arrivals.filter((_, i) => keepArrivals.includes(String(i)))
if (arg("conversations") === "1") {
	conversationCount = 1
	plan.inputs = plan.inputs.filter(p => p.conv === 0)
	plan.observers = plan.observers.filter(o => o.conv === 0)
	plan.arrivals = plan.arrivals.filter(a => a.conv === 0)
}
if (arg("streaming") === "off") plan.streaming = false
for (const p of [...plan.inputs, ...plan.arrivals]) {
	if (arg("rounds") !== undefined) p.rounds = p.rounds.slice(0, Number(arg("rounds")))
	if (arg("calls") !== undefined) p.rounds = p.rounds.map(round => round.slice(0, Number(arg("calls"))))
}
const plans = new Map([...plan.inputs, ...plan.arrivals].map(p => [p.id, p]))
console.log(`plan ${JSON.stringify(plan)}`)

// ─── Behaviour of the plan's tools, hooks and observers ─────────────────────

const log = (...parts) => console.log(parts.join(" "))
const pause = async ({ ticks, ms }) => {
	for (let i = 0; i < ticks; i++) await Promise.resolve()
	if (ms > 0) await new Promise(resolve => setTimeout(resolve, ms))
}
/** A stable index from a string, so a hook's draw depends on the call or request it sees. */
const slot = (text, size) => { let h = 0; for (const ch of String(text)) h = (h * 31 + ch.charCodeAt(0)) >>> 0; return h % size }

const Notes = defineDoc({ kind: "diff.notes", version: 1, scope: "conversation", history: "latest", fork: "current", initial: () => ({ lines: [] }) })
const parameters = Type.Object({ ticks: Type.Number(), ms: Type.Number(), n: Type.Number() })
const tool = (name, execute, extra = {}) => defineTool({ name, description: `diff tool ${name}`, parameters, replay: "safe", ...extra, execute })
const text = value => ({ output: [{ type: "text", text: value }] })
const tools = [
	tool("work", async (args, api) => { log("tool work start", api.callId); await pause(args); log("tool work end", api.callId); return text(`work ${api.callId}`) }),
	tool("commitwork", async (args, api, callContext) => {
		log("tool commitwork start", api.callId)
		await pause(args)
		const lines = await api.commit(async tx => { const notes = await tx.doc(Notes, api.conversationId); notes.lines.push(api.callId); return notes.lines.length }, callContext)
		log("tool commitwork committed", api.callId, lines)
		await pause({ ticks: args.ticks, ms: 0 })
		return text(`committed ${api.callId} ${lines}`)
	}),
	tool("streamer", async (args, api) => {
		log("tool streamer start", api.callId)
		for (let i = 0; i < args.n; i++) { api.output(`chunk ${i} of ${api.callId}\n`); await pause({ ticks: args.ticks, ms: args.ms }) }
		return {}
	}),
	tool("boom", async (args, api) => { await pause(args); log("tool boom throws", api.callId); throw new Error(`boom ${api.callId}`) }),
	tool("serial", async (args, api) => { log("tool serial start", api.callId); await pause(args); return text(`serial ${api.callId}`) }, { executionMode: "sequential" }),
]
const hooks = []
const toolHooks = {}
if (plan.hooks.beforeTool) {
	toolHooks.beforeTool = async call => {
		const spec = plan.hooks.beforeTool[slot(call.id, plan.hooks.beforeTool.length)]
		log("hook beforeTool", call.id)
		await pause(spec)
		return spec.block ? { block: `blocked ${call.id}` } : undefined
	}
}
if (plan.hooks.afterTool) toolHooks.afterTool = async call => { log("hook afterTool", call.id); await pause(plan.hooks.afterTool[slot(call.id, plan.hooks.afterTool.length)]); return undefined }
if (Object.keys(toolHooks).length) hooks.push(hook(ToolTask, toolHooks))
const generationHooks = {}
const textOf = message => (typeof message?.content === "string" ? message.content : (message?.content ?? []).map(block => block.text ?? "").join(""))
const lastUserText = messages => textOf(messages.findLast(m => m.role === "user"))
if (plan.hooks.beforeRequest) {
	generationHooks.beforeRequest = async request => {
		const key = lastUserText(request.messages)
		log("hook beforeRequest", key, request.messages.length)
		await pause(plan.hooks.beforeRequest[slot(`${key}${request.messages.length}`, plan.hooks.beforeRequest.length)])
		return undefined
	}
}
if (plan.hooks.afterResponse) {
	generationHooks.afterResponse = async message => {
		const size = JSON.stringify(message.content).length
		log("hook afterResponse", message.stopReason, size)
		await pause(plan.hooks.afterResponse[slot(size, plan.hooks.afterResponse.length)])
	}
}
if (plan.hooks.afterTools) {
	generationHooks.afterTools = async (assistant, results) => {
		log("hook afterTools", assistant, results.length)
		await pause(plan.hooks.afterTools[slot(assistant, plan.hooks.afterTools.length)])
	}
}
if (Object.keys(generationHooks).length) hooks.push(hook(GenerationTask, generationHooks))

// ─── The scripted model: the next message is a function of the transcript only ──

const faux = fauxProvider({ models: [{ id: "diff-1", contextWindow: 1e9 }], ...(plan.streaming ? { tokenSize: { min: 4, max: 8 }, tokensPerSecond: 400 } : { tokenSize: { min: 1e9, max: 1e9 } }) })
const models = createModels()
models.setProvider(faux.provider)
const respond = transcript => {
	faux.appendResponses([respond])
	const messages = transcript.messages
	const user = messages.findLastIndex(m => m.role === "user")
	const input = plans.get(lastUserText(messages).split(" ")[0])
	if (input === undefined) return fauxAssistantMessage("no plan")
	const round = messages.slice(user + 1).filter(m => m.role === "assistant").length
	if (round < input.rounds.length) {
		return fauxAssistantMessage(input.rounds[round].map(call => fauxToolCall(call.name, { ticks: call.ticks, ms: call.ms, n: call.n }, { id: call.id })), { stopReason: "toolUse" })
	}
	return fauxAssistantMessage([fauxText(input.answer)])
}
faux.setResponses([respond])

// ─── Play the plan ───────────────────────────────────────────────────────────

const registry = createRegistry()
registry.install(defineExtension({ name: "diff", tools, hooks }))
const storage = await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "diff.sqlite"))
const settings = { followUpMode: plan.settings.followUpMode, toolExecution: plan.settings.toolExecution, progress: { partialIntervalMs: plan.settings.partialIntervalMs } }
const harness = await Harness.open(storage, { models, registry, settings }, context)
const model = { provider: faux.getModel().provider, modelId: faux.getModel().id }
const conversations = [await harness.root(context, { agent: { model } })]
for (let c = 1; c < conversationCount; c++) conversations.push(await harness.createConversation({ ownership: { kind: "ownerless" }, agent: { model } }, context))

const submissions = []
const input = (p, whenBusy) => ({ type: "input", content: `${p.id} ${p.kind}`, ...(whenBusy ? { whenBusy } : {}) })
// A write carries a model message so it queues while busy: a write of only kind and data is appended at once (Pi e6b9a6af).
const arrive = async p => {
	await pause(p.at)
	const conversation = conversations[p.conv]
	let submission
	try {
		submission = p.kind === "write"
			? await conversation.submit({ type: "write", entry: { kind: "diff.note", data: p.id, model: [{ role: "user", content: `${p.id} note`, timestamp: 0 }] } }, context)
			: await conversation.submit(input(p, p.kind === "steer" ? "steer" : p.kind === "reject" ? "reject" : undefined), context)
	} catch (error) { log("arrival", p.id, p.kind, "rejected", error.message); return }
	log("arrival", p.id, p.kind, "submitted")
	submissions.push([p.id, submission])
	if (p.withdraw) { await pause(p.withdraw); log("withdraw", p.id, await submission.abort(context)) }
}
const observers = []
const observe = async o => {
	await pause(o.at)
	const conversation = conversations[o.conv]
	if (o.kind === "events") {
		const stream = await watchEvents(harness, conversation.id, context)
		log("events attached", o.conv, stream.snapshot.entries.length)
		stream.start(async events => { for (const e of events) log("event", o.conv, e.type); await pause(o.listener) })
		observers.push(() => stream.stop())
	} else {
		const view = await conversation.viewState(context)
		log("view attached", o.conv, view.value.entries.length)
		view.subscribe(value => log("view", o.conv, value.entries.length))
		observers.push(() => view.dispose())
	}
}
const start = async p => {
	await pause(p.start)
	const submission = await conversations[p.conv].submit(input(p), context)
	submissions.push([p.id, submission])
	log("start", p.id, "submitted")
}
await Promise.all([...plan.inputs.map(start), ...plan.arrivals.map(arrive), ...plan.observers.map(observe)])
for (const [id, submission] of submissions) {
	const settled = await submission.wait(context)
	log("settled", id, settled.status, settled.status === "unanswered" ? settled.reason : "")
}
await harness.waitForIdle(context)
for (const stop of observers) await stop()
for (const [c, conversation] of conversations.entries()) log("transcript", c, (await conversation.entries({}, 200, undefined, context)).items.map(e => e.kind).reverse().join(","))
await harness.close(context)
