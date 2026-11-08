// The microtask passes of a warm turn (rows F-ticks-warm-<profile>): five turns of eight sequential lookups on the bench workload (W1's
// shape: the scripted model, the `lookup` tool and a `preamble` prompt section), each turn's `submit` and `wait` counted as client-ticks.mjs
// counts a call. The wait of a turn is the sum of every await of Pi's turn, so an await the core's host spends too many (or too few) moves
// the count by exactly that many: a section render that resolved the agent once more per model call put 4 passes on each of the nine
// calls of a turn (+36) that F-ticks-node, which pins a cold submit and a short wait, does not see. The count is exact: a self-requeuing
// microtask counts the passes of the microtask queue while the call is pending. Run by lib/run.mjs under the hooks.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { num, obj } from "@earendil-works/pi-schema"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineExtension, defineTool, Harness, section } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"
import { MEASURED_TOOLS, payload, scriptedModel, SYSTEM, turn } from "../lib/workload.mjs"

// The bench workload (scenarios/bench.mjs W1): the scripted model, the `lookup` tool and the preamble section, no seeded history.
const TURNS = 5
const script = scriptedModel({ fauxProvider, fauxAssistantMessage, fauxToolCall, createModels })
const lookup = defineTool({ name: "lookup", description: "Look up record number n", parameters: obj({ n: num() }), execute: async ({ n }) => ({ output: [{ type: "text", text: payload(n) }] }) })
const registry = createRegistry()
registry.install(defineExtension({ name: "bench", tools: [lookup], sections: [section("preamble", () => SYSTEM, { tag: false })] }))
const harness = await Harness.open(await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "warm.sqlite")), { models: script.models, registry }, context)
const model = script.model()
const root = await harness.root(context, { agent: { model: { provider: model.provider, modelId: model.id } } })

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const measure = async (name, call) => {
	passes = 0; counting = true; queueMicrotask(spin)
	const result = await call()
	counting = false
	console.log(`${name}: ${passes}`)
	return result
}
// A virtual timer between turns lets the previous turn's background work finish, so each count starts from an idle Harness.
const settle = () => new Promise(resolve => setTimeout(resolve, 1000))

for (let i = 0; i < TURNS; i++) {
	const t = turn(`m${i}`, MEASURED_TOOLS)
	const submission = await measure(`submit (turn ${i})`, () => root.submit({ type: "input", content: t.text, requestId: t.id }, context))
	await measure(`wait (turn ${i})`, () => submission.wait(context))
	await harness.waitForIdle(context)
	await settle()
}
await harness.close(context)
