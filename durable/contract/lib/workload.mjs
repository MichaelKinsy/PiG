// The durable-bench workload (CONTRACT section 6, W1-W4): one conversation, a scripted model on pi-ai's faux provider and a
// `lookup` tool. The semantics are those of the published runner (badlogic/durable-bench src/plan.ts and src/pi-head.ts at
// the pin in durable-bench/PIN of the S0 lane), so seed fingerprints equal the published ones.
import { createHash } from "node:crypto"

export const CYCLE = [1, 1, 0]
export const MEASURED_TOOLS = 8
export const MEASURED_TURNS = 10
export const SYSTEM = "You are a benchmark agent. Call lookup as instructed, then answer briefly."

/** Seed fingerprints of the published fixtures: the context the last seeding model call saw (BAKEOFF section 4). */
export const SEED_FINGERPRINTS = { 50: "b017b487524e44a4", 250: "dcea9f30b0917245", 1000: "ac520308146f2a8f", 3500: "0a8c8e4b0d9a0794" }

export const turn = (id, tools) => ({ id, text: `turn ${id} tools=${tools}` })
/** The seeded history's turns `from` up to `to` (exclusive): a one-tool, one-tool, zero-tool cycle. */
export const history = (from, to) => Array.from({ length: to - from }, (_, i) => turn(`h${from + i}`, CYCLE[(from + i) % CYCLE.length]))
/** The `lookup` tool's result text; `kb` (W4c) makes every result that many KiB instead of the 256 byte / 8 KiB mix. */
export const payload = (n, kb = 0) => {
	const head = `record ${n}: `
	return head + "x".repeat((kb ? kb * 1024 : n % 97 === 0 ? 8192 : 256) - head.length)
}

/** What the scripted model does next, from the whole conversation so far. */
export function next(context) {
	const last = context.findLastIndex(m => m.role === "user")
	const planned = Number(/tools=(\d+)/.exec(context[last]?.text ?? "")?.[1] ?? 0)
	const total = context.filter(m => m.role === "tool").length
	const done = context.slice(last + 1).filter(m => m.role === "tool").length
	return done < planned ? { call: total + 1 } : { answer: `done after ${done} lookups` }
}

/** The incremental scripted model: the same decision as next(), from counters over only the new messages (BAKEOFF W2). */
export class Incremental {
	scanned = 0; planned = 0; total = 0; done = 0
	step(context, role, text) {
		if (context.length < this.scanned) this.scanned = this.planned = this.total = this.done = 0
		for (let i = this.scanned; i < context.length; i++) {
			const r = role(context[i])
			if (r === "user") { this.planned = Number(/tools=(\d+)/.exec(text(context[i]))?.[1] ?? 0); this.done = 0 }
			else if (r === "tool") { this.total++; this.done++ }
		}
		this.scanned = context.length
		return this.done < this.planned ? { call: this.total + 1 } : { answer: `done after ${this.done} lookups` }
	}
}

const blocksOf = m => (typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content)
const textOf = m => blocksOf(m).filter(b => b.type === "text").map(b => b.text).join("")
export const kept = m => ["user", "assistant", "toolResult"].includes(m.role)
const roleOf = m => (m.role === "toolResult" ? "tool" : m.role === "user" || m.role === "assistant" ? m.role : undefined)
export const convert = m => {
	const calls = blocksOf(m).filter(b => b.type === "toolCall").map(b => b.arguments.n)
	return { role: m.role === "toolResult" ? "tool" : m.role, text: textOf(m), ...(calls.length ? { calls } : {}) }
}
/** The bench fingerprint's messages for a pi-ai message list. */
export const seenOf = messages => messages.filter(kept).map(convert)
/** The bench fingerprint: 8 bytes of sha256 over [role, text, calls] of every message (CONTRACT section 5.4). */
export const fingerprint = context => createHash("sha256").update(JSON.stringify(context.map(m => [m.role, m.text, m.calls ?? []]))).digest("hex").slice(0, 16)

const UNIT = "the quick brown fox jumps over the lazy dog. "
/** W3: `turn x tools=N stream=C` answers with C characters of text, so a response streams in many chunks. */
const answerOf = (user, done) => {
	const chars = Number(/stream=(\d+)/.exec(user ?? "")?.[1] ?? 0)
	return chars > 0 ? UNIT.repeat(Math.ceil(chars / UNIT.length)).slice(0, chars) : `done after ${done} lookups`
}

/**
 * The scripted model on pi-ai's faux provider. `streaming` is W3: eight-token chunks at 400 tokens per second, so a
 * 40-60 chunk answer streams over 1-2 virtual seconds. tokenSize.min == max in the other variant (CONTRACT section 2.2).
 */
export function scriptedModel({ fauxProvider, fauxAssistantMessage, fauxToolCall, createModels }, { variant = "full", streaming = false, fault, contextWindow = 1e9 } = {}) {
	const faux = fauxProvider({ models: [{ id: "scripted-1", contextWindow }], ...(streaming ? { tokenSize: { min: 8, max: 8 }, tokensPerSecond: 400 } : { tokenSize: { min: 1e9, max: 1e9 } }) })
	const models = createModels()
	models.setProvider(faux.provider)
	const incremental = new Incremental()
	const state = { last: [], calls: 0 }
	const respond = transcript => {
		faux.appendResponses([respond])
		// A compaction's summarization request (W4a seed) is not a scripted turn: answer it with text and leave the script's state alone.
		if (transcript.messages.some(m => m.role === "system" && textOf(m).startsWith("You are a context summarization assistant"))) return fauxAssistantMessage("Summary of the earlier turns.")
		state.last = transcript.messages
		state.calls++
		// W4g: one retryable provider error (isRetryableAssistantError matches "503") on the second call, then the script continues.
		if (fault === "retryable-error" && state.calls === 2) return fauxAssistantMessage("", { stopReason: "error", errorMessage: "503 service unavailable" })
		let step
		if (variant === "full") {
			const seen = transcript.messages.filter(kept).map(convert)
			step = next(seen)
			if ("answer" in step) step.answer = answerOf(seen.findLast(m => m.role === "user")?.text, Number(/\d+/.exec(step.answer)?.[0] ?? 0))
		} else {
			step = incremental.step(transcript.messages, roleOf, textOf)
			if ("answer" in step) step.answer = answerOf(textOf(transcript.messages.findLast(m => m.role === "user") ?? { content: "" }), Number(/\d+/.exec(step.answer)?.[0] ?? 0))
		}
		// W4e: `subagent=1` turns delegate to the `subagent` tool, which runs the task in a child conversation the tool call owns.
		const userText = textOf(transcript.messages.findLast(m => m.role === "user") ?? { content: "" })
		if (/subagent=1/.test(userText) && "call" in step) return fauxAssistantMessage(fauxToolCall("subagent", { task: `task for ${/turn (\S+)/.exec(userText)?.[1]}` }, { id: `call-sub-${state.calls}` }), { stopReason: "toolUse" })
		if ("call" in step) {
			// W4d: `parallel=1` turns announce every planned lookup in one assistant message, so the round runs its tools in parallel.
			const user = textOf(transcript.messages.findLast(m => m.role === "user") ?? { content: "" })
			const planned = /parallel=1/.test(user) ? Number(/tools=(\d+)/.exec(user)?.[1] ?? 0) : 1
			const doneAfterUser = transcript.messages.slice(transcript.messages.findLastIndex(m => m.role === "user") + 1).filter(m => m.role === "toolResult").length
			const calls = planned > 1 && doneAfterUser === 0 ? Array.from({ length: planned }, (_, i) => step.call + i) : [step.call]
			return fauxAssistantMessage(calls.map(n => fauxToolCall("lookup", { n }, { id: `call-${n}` })), { stopReason: "toolUse" })
		}
		return fauxAssistantMessage(step.answer)
	}
	faux.setResponses([respond])
	return { faux, models, state, model: () => faux.getModel(), benchFingerprint: () => fingerprint(seenOf(state.last)) }
}
