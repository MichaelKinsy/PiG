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
//   --objects K     K Harnesses in this process, as K Durable Objects in one isolate (robust-durable): object 0 opens --db, object
//                   i a copy of it (with its WAL) made before any open in the run's own TMPDIR, never beside --db, which may
//                   be a shared fixture; each has its own scripted model, and every measured turn runs once on each object in
//                   order (one memory sample per round)
import { appendFileSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { AssistantEntry, configure, createRegistry, defineExtension, defineTool, Harness, section } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"
import { copyStore } from "../lib/store.mjs"
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
// A tool result names its blocks `output` in Pi main (types.ts ToolExecutionResult) and `content` in the 1.0.4 release this scenario also
// seeds (fixtures.mjs kind v1.0.4). The tool returns only the field of the release it runs on: with both, a candidate that still reads
// `content` would store the same tool result as Pi main and pass the W, X1 and H rows.
const legacy = JSON.parse(readFileSync(join(process.env.PI_DURABLE_SOURCE_ROOT ?? join(process.cwd(), "..", ".."), "packages", "durable", "package.json"), "utf8")).version === "1.0.4"
const result = blocks => (legacy ? { content: blocks } : { output: blocks })
const lookup = defineTool({ name: "lookup", description: "Look up record number n", parameters: Type.Object({ n: Type.Number() }), execute: async ({ n }) => result([{ type: "text", text: payload(n, payloadKb) }]) })
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
			return { ...result([{ type: "text", text }]), details: { conversationId: child } }
		},
	})],
})
if (flag("subagent")) registry.install(Subagent)

const settings = { compaction: { enabled: flag("compaction") }, ...(partialMs ? { progress: { partialIntervalMs: Number(partialMs) } } : {}) }
// lib/run.mjs gives every run its own TMPDIR, so the copies neither race another run nor stay beside a shared fixture.
const copies = Array.from({ length: Number(arg("objects", "1")) - 1 }, (_, i) => copyStore(db, join(tmpdir(), `bench-object-${i + 1}.sqlite`)))
const harness = await Harness.open(await openNodeSqliteStorage(db), { models: script.models, registry, settings }, context)
const model = script.model()
const root = await harness.root(context, { agent: { model: { provider: model.provider, modelId: model.id } } })
const others = []
for (const copy of copies) {
	const own = scriptedModel({ fauxProvider, fauxAssistantMessage, fauxToolCall, createModels }, { variant, streaming: stream > 0, contextWindow: Number(arg("window", "0")) || undefined })
	const h = await Harness.open(await openNodeSqliteStorage(copy), { models: own.models, registry, settings }, context)
	const m = own.model()
	others.push({ harness: h, root: await h.root(context, { agent: { model: { provider: m.provider, modelId: m.id } } }) })
}

async function run(t, on = root) {
	const submission = await on.submit({ type: "input", content: t.text, requestId: t.id }, context)
	const settled = await submission.wait(context)
	if (settled.status !== "done") throw new Error(JSON.stringify(settled))
	await on.waitForIdle(context)
}

if (flag("recover")) await root.waitForIdle(context)
for (const t of history(0, seed)) await run(t)
// One memory sample per measured turn for the gate's bound (lib/memory.mjs): the process's resident set, and the linear memory
// of every Wasm core the candidate facade made.
const memFile = process.env.CONTRACT_MEM
if (memFile) writeFileSync(memFile, "")
// CONTRACT_MEM_HEAP=1 (the memory induction of cli/robust.mjs): each sample also runs a full collection in every core and in
// the host, and records what they retain: `heap` the cores' heaps (mem_stats slot 6), `reserved` what the cores obtained (Wasm
// linear memory, a native core's runtime Sys), `jsHeap` the host's V8 heap. The collections cost time, so timed rows leave it off.
const heapSamples = process.env.CONTRACT_MEM_HEAP === "1"
const memEvery = Number(process.env.CONTRACT_MEM_EVERY ?? "1")
if (heapSamples) (await import("node:v8")).setFlagsFromString("--expose-gc")
const collect = heapSamples ? (await import("node:vm")).runInNewContext("gc") : undefined
const sampleMemory = turn => {
	if (!memFile) return
	const cores = globalThis[Symbol.for("pig.contract")]?.cores
	const wasm = cores?.size ? [...cores].filter(core => typeof core.memoryBytes === "number") : []
	const linear = wasm.length ? wasm.reduce((sum, core) => sum + core.memoryBytes, 0) : undefined
	let heap
	if (heapSamples) {
		collect()
		const stats = [...(cores ?? [])].filter(core => core.heapStats).map(core => core.heapStats())
		// wallMs: the real clock (detenv makes Date and performance.now virtual), so a slowing turn shows in the samples.
		heap = { wallMs: Number(process.hrtime.bigint() / 1000000n), jsHeap: process.memoryUsage().heapUsed, ...(stats.length ? { heap: stats.reduce((sum, s) => sum + s.heap, 0), reserved: stats.reduce((sum, s) => sum + s.reserved, 0), cores: stats.length } : {}) }
	}
	const usage = process.memoryUsage()
	// heapUsed, arrayBuffers and external are for the host-memory investigation (CONTRACT_HEAPSNAP); the gate reads rss and linear.
	appendFileSync(memFile, JSON.stringify({ turn, rss: usage.rss, heapUsed: usage.heapUsed, arrayBuffers: usage.arrayBuffers, external: usage.external, ...(linear === undefined ? {} : { linear }), ...heap }) + "\n")
}
// CONTRACT_HEAPPROF=<file>: V8's sampling heap profiler over the measured turns (what allocates, not what is retained).
let heapProfile
if (process.env.CONTRACT_HEAPPROF) {
	const { Session } = await import("node:inspector")
	heapProfile = new Session(); heapProfile.connect()
	const post = (m, p) => new Promise((ok, no) => heapProfile.post(m, p, (e, r) => e ? no(e) : ok(r)))
	heapProfile.call = post
	await post("HeapProfiler.startSampling", { samplingInterval: 4096 })
}
// CONTRACT_CPUPROF=<file>: V8's CPU profile over the measured turns (Wasm functions appear by their name-section names).
let cpuProfile
if (process.env.CONTRACT_CPUPROF) {
	const { Session } = await import("node:inspector")
	cpuProfile = new Session(); cpuProfile.connect()
	const post = (m, p) => new Promise((ok, no) => cpuProfile.post(m, p, (e, r) => e ? no(e) : ok(r)))
	cpuProfile.call = post
	await post("Profiler.enable"); await post("Profiler.setSamplingInterval", { interval: 200 }); await post("Profiler.start")
}
// CONTRACT_COUNTS=<file> (cli/counts.mjs): the cumulative counters after each measured turn, then the cores' final heap and linear memory.
const counts = process.env.CONTRACT_COUNTS ? (await import("../lib/counts.mjs")).sampler(process.env.CONTRACT_COUNTS) : undefined
for (let i = 0; i < turns; i++) {
	const t = { ...turn(`${label}${i}`, tools), text: `turn ${label}${i} tools=${tools}${stream ? ` stream=${stream}` : ""}${flag("parallel") ? " parallel=1" : ""}${flag("subagent") ? " subagent=1" : ""}` }
	await run(t)
	for (const o of others) await run(t, o.root)
	// CONTRACT_MEM_EVERY=K (the soak): one sample every K turns and after the last, so a long run's collections stay cheap.
	if ((i + 1) % memEvery === 0 || i === turns - 1) sampleMemory(i)
	counts?.turn(i)
}
// A background compaction may still be running; a fixture is written at rest.
if (flag("compaction")) for (const task of (await harness.inspect(context)).tasks) if (task.record.state.status !== "terminal") await harness.waitForTask(task.record.id, context)
counts?.final()
if (cpuProfile) writeFileSync(process.env.CONTRACT_CPUPROF, JSON.stringify((await cpuProfile.call("Profiler.stop")).profile))
if (heapProfile) writeFileSync(process.env.CONTRACT_HEAPPROF, JSON.stringify((await heapProfile.call("HeapProfiler.stopSampling")).profile))
if (process.env.CONTRACT_HEAPSNAP) { globalThis.gc?.(); (await import("node:v8")).writeHeapSnapshot(process.env.CONTRACT_HEAPSNAP) }
for (const o of others) await o.harness.close(context)
await harness.close(context)
console.log(JSON.stringify({ seeded: seed, turns, calls: script.state.calls, bench: script.benchFingerprint() }))
