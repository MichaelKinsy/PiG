// The tick costs of the calls rows F-ticks-<profile> leave out (rows F-ticks-runtime-<profile>): runtime.context in a custom task
// over a range it scans, keeps, extends and cuts; client reads queued behind a running turn instead of on an idle line; and a wait on a
// running submission under a context that carries an abort signal. Each count is the microtask passes the call keeps pending, as in
// client-ticks.mjs: a client read's answer, a submission waiter and a context read each resume their caller in Pi's own passes only when
// the candidate answers at Pi's tick, not one early or late. Run by lib/run.mjs under the hooks.
// `--seed` picks the part:
//   0 the calls above, which spend Pi's passes (rows F-ticks-runtime-<profile>);
//   1 calls that do not yet (rows F-ticks-runtime-drift-<profile>, listed in baseline.toml): waitForTask on a settled task and on a
//     live custom task, runtime.getTask in a custom task, and a single-row client read (status) behind a running turn and part 0's
//     two reads.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context, withAbortSignal } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineExtension, defineTask, Harness } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const argv = process.argv.slice(2)
const part = Number(argv[argv.indexOf("--seed") + 1])
if (![0, 1].includes(part)) throw new Error(`--seed must be 0 (runtime reads) or 1 (drift), got ${argv[argv.indexOf("--seed") + 1]}`)

const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
const respond = () => { faux.appendResponses([respond]); return fauxAssistantMessage("answer") }
faux.setResponses([respond])

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const measure = async (name, call) => {
	passes = 0; counting = true; queueMicrotask(spin)
	const result = await call()
	counting = false
	console.log(`${name}: ${passes}`)
	return result
}
const settle = () => new Promise(resolve => setTimeout(resolve, 1000))
const user = text => ({ role: "user", content: text, timestamp: 0 })

let rootId
// runtime.context of a custom task: the first read scans the range, the second keeps it, a new entry extends it, an earlier cutoff cuts
// it, and the kept range serves again (context.ts readContextFrom, scheduler.ts :1229-1256).
const Reads = defineTask({
	name: "ticks.context",
	version: 1,
	initial: () => ({ phase: "run" }),
	phases: {
		run: async (_task, runtime, taskContext) => {
			const first = await measure("runtime.context (scan)", () => runtime.context(rootId, taskContext))
			await measure("runtime.context (kept)", () => runtime.context(rootId, taskContext))
			await runtime.commit(async tx => { await tx.appendEntry(rootId, { kind: "message", model: [user("more")] }); return undefined }, taskContext)
			await measure("runtime.context (extend)", () => runtime.context(rootId, taskContext))
			await measure("runtime.context (cut)", () => runtime.context(rootId, taskContext, { at: first.entries.at(-1).id }))
			await measure("runtime.context (kept again)", () => runtime.context(rootId, taskContext))
			await runtime.commit(() => ({ status: "terminal", outcome: { status: "completed", result: null } }), taskContext)
		},
	},
	abort: async (_task, runtime, taskContext) => { await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), taskContext) },
})
// A task whose one phase ends it.
const Done = defineTask({
	name: "ticks.done",
	version: 1,
	initial: () => ({ phase: "run" }),
	phases: { run: async (_task, runtime, taskContext) => { await runtime.commit(() => ({ status: "terminal", outcome: { status: "completed", result: null } }), taskContext) } },
	abort: async (_task, runtime, taskContext) => { await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), taskContext) },
})
// A task that reads another task's record (runtime.getTask: a readOnLine job, scheduler.ts :1183-1186), then ends.
let readId
const Get = defineTask({
	name: "ticks.get",
	version: 1,
	initial: () => ({ phase: "run" }),
	phases: {
		run: async (_task, runtime, taskContext) => {
			await measure("runtime.getTask", () => runtime.getTask(readId, taskContext))
			await runtime.commit(() => ({ status: "terminal", outcome: { status: "completed", result: null } }), taskContext)
		},
	},
	abort: async (_task, runtime, taskContext) => { await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), taskContext) },
})
// A task that reads an entry of its conversation (runtime.entry: an async arrow over a readOnLine job, scheduler.ts :1216-1228), then ends.
let entryId
const GetEntry = defineTask({
	name: "ticks.entry",
	version: 1,
	initial: () => ({ phase: "run" }),
	phases: {
		run: async (_task, runtime, taskContext) => {
			await measure("runtime.entry", () => runtime.entry(entryId, taskContext))
			await runtime.commit(() => ({ status: "terminal", outcome: { status: "completed", result: null } }), taskContext)
		},
	},
	abort: async (_task, runtime, taskContext) => { await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), taskContext) },
})
const registry = createRegistry()
registry.install(defineExtension({ name: "ticks", tasks: [Reads, Done, Get, GetEntry] }))
const harness = await Harness.open(await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "runtime-ticks.sqlite")), { models, registry }, context)
const root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
rootId = root.id
for (let i = 0; i < 3; i++) entryId = (await root.commit(tx => tx.appendEntry(root.id, { kind: "message", model: [user(`m${i}`)] }), context)).id

if (part === 0) {
	const task = await harness.commit(tx => tx.createTask(Reads, {}, { ownership: { kind: "conversation" }, conversationId: root.id }), context)
	harness.resume()
	console.log(`task: ${(await harness.waitForTask(task, context)).state.outcome.status}`)
	await settle()

	// Client reads queued behind the turn a submission starts: each waits for the turn's queued line jobs, then answers. A single-row
	// read (status) is part 1.
	const turn = await root.submit({ type: "input", content: "first" }, context)
	await measure("entries (busy line)", () => root.entries({}, 5, undefined, context))
	await measure("inspect (busy line)", async () => (await harness.inspect(context)).tasks.length)
	await turn.wait(context)
	await settle()

	// A wait under an abortable context: Pi's waiter takes the signal itself (util.ts Waiters.add), so it spends no pass of its own.
	const controller = new AbortController()
	const second = await root.submit({ type: "input", content: "second" }, context)
	const settled = await measure("wait (running, abortable context)", () => second.wait(withAbortSignal(controller.signal, context)))
	console.log(`second: ${settled.status}`)
	await settle()
} else {
	// waitForTask on a stored task: a readOnLine job that reads the record, then `return found.promise` (scheduler.ts waitForTask
	// :306-315). On a live task: the line job registers the waiter, the task's reservation, step, phase and terminal commit run, and the
	// terminal publication resolves the waiter.
	const first = await harness.commit(tx => tx.createTask(Done, {}, { ownership: { kind: "conversation" }, conversationId: root.id }), context)
	harness.resume()
	console.log(`first: ${(await harness.waitForTask(first, context)).state.outcome.status}`)
	await settle()
	await measure("waitForTask (settled task)", () => harness.waitForTask(first, context))
	const live = await harness.commit(tx => tx.createTask(Done, {}, { ownership: { kind: "conversation" }, conversationId: root.id }), context)
	harness.resume()
	console.log(`live: ${(await measure("waitForTask (live task)", () => harness.waitForTask(live, context))).state.outcome.status}`)
	await settle()
	readId = first
	const get = await harness.commit(tx => tx.createTask(Get, {}, { ownership: { kind: "conversation" }, conversationId: root.id }), context)
	harness.resume()
	console.log(`get: ${(await harness.waitForTask(get, context)).state.outcome.status}`)
	await settle()
	const getEntry = await harness.commit(tx => tx.createTask(GetEntry, {}, { ownership: { kind: "conversation" }, conversationId: root.id }), context)
	harness.resume()
	console.log(`entry: ${(await harness.waitForTask(getEntry, context)).state.outcome.status}`)
	await settle()

	// Part 0's client reads behind a running turn, then a single-row read (status): on the gate profile it also waits in the capsql
	// statement queue behind the turn's off-line range scan, a contention the candidate's Storage read costs do not model (hopcost
	// ReadHops: idle-queue costs).
	const turn = await root.submit({ type: "input", content: "first" }, context)
	await measure("entries (busy line)", () => root.entries({}, 5, undefined, context))
	await measure("inspect (busy line)", async () => (await harness.inspect(context)).tasks.length)
	await measure("status (busy line)", () => turn.status(context))
	await turn.wait(context)
	await settle()
}
await harness.close(context)
