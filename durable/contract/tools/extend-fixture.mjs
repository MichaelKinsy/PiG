#!/usr/bin/env node
// extend-fixture <store.sqlite> <from> <to>: append the W1 history turns h<from> .. h<to - 1> (lib/workload.mjs `history`) to a
// store that holds turns h0 .. h<from - 1>, with pi-durable from the reference checkout (CONTRACT_PI_SOURCE). The store is
// the scenario's own SQLite file: a contract fixture, or a durable-bench fixture's Durable Object database (the same schema
// beside workerd's _cf_ tables), so a 10,000- or 25,000-turn history is written once instead of being seeded from turn 0.
//
// The workload is bench.mjs's (and durable-bench's pi-head target's): the same section, tool, payloads, settings and request
// ids. The scripted model is the incremental variant (Incremental: the decisions of next() from counters over the new
// messages), so a turn costs what pi-durable costs rather than a walk of the whole transcript per call. The last model call's
// context is fingerprinted the way the published seeds are (SEED_FINGERPRINTS): extending the 1,000-turn fixture to 3,500
// turns must print the published 3,500-turn fingerprint.
//
// It runs without the contract's hooks (no capture, no virtual clock): only a resolve hook that maps a pi-ai subpath export
// whose dist/ file is not built to its src/ source.
import { register } from "node:module"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"
import { fingerprint, history, Incremental, payload, seenOf, SYSTEM } from "../lib/workload.mjs"

const [db, fromArg, toArg] = process.argv.slice(2)
const from = Number(fromArg), to = Number(toArg)
if (!db || !Number.isInteger(from) || !Number.isInteger(to) || from < 0 || to <= from) {
	console.error("usage: node --conditions=source --experimental-strip-types --no-warnings extend-fixture.mjs <store.sqlite> <from> <to>")
	process.exit(2)
}
const pi = resolve(process.env.CONTRACT_PI_SOURCE ?? "")
// Bare imports of this file resolve from the reference checkout; a pi-ai subpath whose dist/ file is not built resolves to src/.
const parent = pathToFileURL(join(pi, "packages", "durable", "src", "index.ts")).href
const hook = `export async function resolve(specifier, context, next) {
	if (!/^(\\.{0,2}\\/|[a-z][a-z0-9+.-]*:)/i.test(specifier) && !context.parentURL?.startsWith(${JSON.stringify(pathToFileURL(pi).href)})) context = { ...context, parentURL: ${JSON.stringify(parent)} }
	try { return await next(specifier, context) } catch (error) {
		const url = error?.url ?? ""
		if (error?.code === "ERR_MODULE_NOT_FOUND" && url.includes("/dist/")) return next(url.replace(/\\/dist\\/(.*)\\.js$/, "/src/$1.ts"), context)
		throw error
	}
}`
register(`data:text/javascript,${encodeURIComponent(hook)}`)
const { BACKGROUND_CONTEXT: context } = await import("@earendil-works/chord/context")
const { Type } = await import("@earendil-works/pi-ai")
const { createModels } = await import("@earendil-works/pi-ai/models")
const { fauxAssistantMessage, fauxProvider, fauxToolCall } = await import("@earendil-works/pi-ai/providers/faux")
const { createRegistry, defineExtension, defineTool, Harness, section } = await import("@earendil-works/pi-durable")
const { openNodeSqliteStorage } = await import("@earendil-works/pi-durable/storage/sqlite/node")

const faux = fauxProvider({ models: [{ id: "scripted-1", contextWindow: 1e9 }], tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
const blocksOf = m => (typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content)
const textOf = m => blocksOf(m).filter(b => b.type === "text").map(b => b.text).join("")
const roleOf = m => (m.role === "toolResult" ? "tool" : m.role === "user" || m.role === "assistant" ? m.role : undefined)
const incremental = new Incremental()
let last = []
const respond = transcript => {
	faux.appendResponses([respond])
	last = transcript.messages
	const step = incremental.step(transcript.messages, roleOf, textOf)
	return "call" in step ? fauxAssistantMessage(fauxToolCall("lookup", { n: step.call }, { id: `call-${step.call}` }), { stopReason: "toolUse" }) : fauxAssistantMessage(step.answer)
}
faux.setResponses([respond])
const lookup = defineTool({ name: "lookup", description: "Look up record number n", parameters: Type.Object({ n: Type.Number() }), execute: async ({ n }) => ({ output: [{ type: "text", text: payload(n) }] }) })
const registry = createRegistry()
registry.install(defineExtension({ name: "bench", tools: [lookup], sections: [section("preamble", () => SYSTEM, { tag: false })] }))

const harness = await Harness.open(await openNodeSqliteStorage(db), { models, registry, settings: { compaction: { enabled: false } } }, context)
const model = faux.getModel()
const root = await harness.root(context, { agent: { model: { provider: model.provider, modelId: model.id } } })
const t0 = Date.now()
let done = from
for (const t of history(from, to)) {
	const submission = await root.submit({ type: "input", content: t.text, requestId: t.id }, context)
	const settled = await submission.wait(context)
	if (settled.status !== "done") throw new Error(`${t.id}: ${JSON.stringify(settled)}`)
	await root.waitForIdle(context)
	if (++done % 500 === 0) process.stderr.write(`${done}/${to} ${Math.round((Date.now() - t0) / 1000)} s\n`)
}
await harness.close(context)
console.log(JSON.stringify({ from, to, ms: Date.now() - t0, bench: fingerprint(seenOf(last)) }))
