// Time travel on a long conversation (plan 2 item 9, robust-durable): open a W1 store (durable-bench's history, e.g. the
// 3,500-turn fixture), find history turn h<K>, and run Pi's as-of reads and a fork there. Each operation prints one line of
// what a client observes (counts and a fingerprint of the result) and adds its wall time to --timings.
//   time-travel.mjs --db <store> [--at-turn 100] [--scrub 8] [--timings file]
// Operations, in this order:
//   locate     Conversation.entries pages in ascending order until the user entry of h<K> and the answer that ends it
//   asof       Conversation.context({ at }) at the answer that ends h<K>
//   snapshot   Harness.snapshotAsOf(AgentDoc, root, at): the agent document as of that entry, and as of the first entry
//   scrub      context({ at }) at S entries spread evenly from the first to the newest (a UI scrubbing a timeline)
//   fork       Conversation.fork(at) and one turn on the fork; then the fork's context and the root's newest context
// Run by lib/run.mjs under the hooks (the clock is virtual there; the timings read the real one).
import { appendFileSync } from "node:fs"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { AgentDoc, createRegistry, defineExtension, defineTool, Harness, section } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"
import { createHash } from "node:crypto"
import { fingerprint, payload, scriptedModel, seenOf, SYSTEM, turn } from "../lib/workload.mjs"

const argv = process.argv.slice(2)
const arg = (name, fallback) => { const i = argv.indexOf(`--${name}`); return i < 0 ? fallback : argv[i + 1] }
const db = arg("db"), atTurn = Number(arg("at-turn", "100")), scrubs = Number(arg("scrub", "8")), timings = arg("timings")
const log = (...parts) => console.log(parts.join(" "))
const hash = value => createHash("sha256").update(JSON.stringify(value)).digest("hex").slice(0, 16)
const real = () => Number(process.hrtime.bigint()) / 1e6
async function timed(op, work) {
	const t0 = real()
	const result = await work()
	if (timings) appendFileSync(timings, JSON.stringify({ op, ms: real() - t0 }) + "\n")
	return result
}

const script = scriptedModel({ fauxProvider, fauxAssistantMessage, fauxToolCall, createModels }, { variant: "full" })
const lookup = defineTool({ name: "lookup", description: "Look up record number n", parameters: Type.Object({ n: Type.Number() }), execute: async ({ n }) => ({ output: [{ type: "text", text: payload(n, 0) }] }) })
const registry = createRegistry()
registry.install(defineExtension({ name: "bench", tools: [lookup], sections: [section("preamble", () => SYSTEM, { tag: false })] }))
const harness = await timed("open", async () => Harness.open(await openNodeSqliteStorage(db), { models: script.models, registry }, context))
const model = script.model()
const root = await timed("root", () => harness.root(context, { agent: { model: { provider: model.provider, modelId: model.id } } }))

const userText = e => e.kind === "pi.user" ? e.model?.[0]?.content : undefined
const contextLine = view => `messages ${view.messages.length} fp ${fingerprint(seenOf(view.messages))}`

// locate: the user entry of h<K>, and the last entry before the user entry of h<K+1> (the answer that ends h<K>).
const { start, end, scanned, users } = await timed("locate", async () => {
	let cursor, start, end, prev, scanned = 0
	const users = []
	for (;;) {
		const page = await root.entries({ order: "ascending" }, 500, cursor, context)
		for (const e of page.items) {
			scanned++
			const text = userText(e)
			if (text !== undefined) {
				users.push(e.id)
				if (start !== undefined) { end = prev; break }
				if (text.startsWith(`turn h${atTurn} `)) start = e.id
			}
			prev = e.id
		}
		if (end !== undefined || page.nextCursor === undefined) break
		cursor = page.nextCursor
	}
	return { start, end, scanned, users }
})
if (start === undefined || end === undefined) throw new Error(`time-travel: the store has no turn h${atTurn} followed by another turn`)
log("locate", `h${atTurn}`, "user", start, "answer", end, "scanned", scanned)

log("asof", contextLine(await timed("asof", () => root.context(context, { at: end }))))

const agentAt = await timed("snapshotAsOf", () => harness.snapshotAsOf(AgentDoc, root.id, end, context))
const agentFirst = await timed("snapshotAsOf-first", () => harness.snapshotAsOf(AgentDoc, root.id, users[0], context))
log("snapshot agent", hash(agentAt ?? null), "first", hash(agentFirst ?? null))

// scrub: S points spread evenly over the conversation's entry IDs, from its first entry to its newest; each point reads the
// first entry at or after it (one page of one entry, as a timeline UI does) and the context as of that entry.
const newest = (await root.entries({ order: "descending" }, 1, undefined, context)).items[0].id
for (let i = 0; i < scrubs; i++) {
	const target = users[0] + Math.floor(i * (newest - users[0]) / Math.max(1, scrubs - 1))
	const view = await timed("scrub", async () => {
		const at = (await root.entries({ minEntryId: target, order: "ascending" }, 1, undefined, context)).items[0].id
		return { at, view: await root.context(context, { at }) }
	})
	log("scrub", i, "at", view.at, contextLine(view.view))
}

const fork = await timed("fork", () => root.fork(end, { ownership: { kind: "ownerless" } }, context))
log("fork", fork.id, contextLine(await timed("fork-context", () => fork.context(context))))
const t = turn(`f${atTurn}`, 1)
const settled = await timed("fork-turn", async () => {
	const s = await (await fork.submit({ type: "input", content: t.text, requestId: t.id }, context)).wait(context)
	await fork.waitForIdle(context)
	return s
})
log("fork turn", settled.status, contextLine(await fork.context(context)))
log("root", contextLine(await timed("root-context", () => root.context(context))))
await harness.close(context)
