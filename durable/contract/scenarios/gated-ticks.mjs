// The microtask passes a task's `runtime.commit` takes, from the call to its caller's resumption (generated into durable/core/session/hopcost/
// testdata/gated_ticks.json by tools/gen-hopticks.mjs): the scheduler's `#gated` wrapper (scheduler.ts :2928-2960: the decision region that
// rereads the running task, then the user's change) around a Session line job. Counted by a self-requeuing microtask as storage-ticks.mjs
// counts. The Harness is the reference's own over the storage storage-ticks.mjs opens.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineExtension, defineTask, Harness, MemoryStorage } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const argv = process.argv.slice(2)
const kind = argv[argv.indexOf("--storage") + 1]
const storage = kind === "memory" ? new MemoryStorage() : await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "gated-ticks.sqlite"))
const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const out = []
const measure = async (name, call) => {
	passes = 0; counting = true; queueMicrotask(spin)
	try { return await call() } finally { counting = false; out.push(`${name} ${passes}`) }
}
const user = text => ({ kind: "pi.user", model: [{ role: "user", content: text, timestamp: 1 }] })
const settle = () => new Promise(resolve => setTimeout(resolve, 1000))

let rootId
const done = { status: "terminal", outcome: { status: "completed", result: null } }
const Probe = defineTask({
	name: "ticks.gated",
	version: 1,
	initial: () => ({ phase: "run" }),
	phases: {
		run: async (_task, runtime, taskContext) => {
			await settle()
			await measure("runtime.commit (async, no writes)", () => runtime.commit(async () => undefined, taskContext))
			await settle()
			await measure("runtime.commit (sync, no writes)", () => runtime.commit(() => undefined, taskContext))
			await settle()
			await measure("runtime.commit (entry)", () => runtime.commit(async tx => { await tx.appendEntry(rootId, user("a")); return undefined }, taskContext))
			await settle()
			// A child task: the commit's task membership walks the chain above it.
			await measure("runtime.commit (child task)", () => runtime.commit(async tx => { await tx.createTask(Quiet, {}, { ownership: { kind: "task", taskId: runtime.taskId } }); return undefined }, taskContext))
			await settle()
			// A next state: `await #commitState(...)`; the task stays in its phase.
			await measure("runtime.commit (next state)", () => runtime.commit(() => ({ status: "running", checkpoint: { phase: "run" } }), taskContext))
			await settle()
			await runtime.commit(() => done, taskContext)
		},
	},
	abort: async (_task, runtime, taskContext) => { await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), taskContext) },
})
// The child the probe creates: it ends at once.
const Quiet = defineTask({
	name: "ticks.quiet",
	version: 1,
	initial: () => ({ phase: "run" }),
	phases: { run: async (_task, runtime, taskContext) => { await runtime.commit(() => done, taskContext) } },
	abort: async (_task, runtime, taskContext) => { await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), taskContext) },
})
const registry = createRegistry()
registry.install(defineExtension({ name: "ticks", tasks: [Probe, Quiet] }))
const harness = await Harness.open(storage, { models, registry }, context)
const root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
rootId = root.id
const task = await harness.commit(tx => tx.createTask(Probe, {}, { ownership: { kind: "conversation" }, conversationId: root.id }), context)
harness.resume()
await harness.waitForTask(task, context)
for (const line of out) console.log(line)
process.exit(0)
