// The split tick counts (rows F-split-<part>-<profile>): where client-ticks.mjs prints one total per client call, this prints the
// microtask passes between observable points inside one call, so two Go-side tick errors that cancel in a total fail a row apart.
// `--seed` picks the part:
//   1 generation: one turn's points measured from the submission's publication (the first agent event of the turn): the model
//     call (the faux respond function) and the turn's last events. Between them run the prepare phase's context read (a scan) and
//     the request phase's read of the same range (`at: cutoff`, the kept range: context.ts readContextFrom :81), so a read that
//     spends other passes than Pi moves the model call.
//   2 client: the same turn measured from the client's side: the submission's publication relative to the call of `submit` (its line
//     job starts a tick after the call, whatever resolves the call) and to `submit` resolving, and `wait` resolving relative to the
//     turn's last events (submissions.ts wait :75-86).
//   3 context: after one turn, a custom task's runtime.context, from the call to the phase's resumption, for a first read (scan), the same read
//     again and a cut (the kept range) and a read after a commit (an extending scan) (scheduler.ts :1229-1256).
// Every wait is a tick or a detenv virtual timer, so the counts are exact. Run by lib/run.mjs under the hooks.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, defineExtension, defineTask, Harness, watchEvents } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const argv = process.argv.slice(2)
const part = Number(argv[argv.indexOf("--seed") + 1])
if (![1, 2, 3].includes(part)) throw new Error(`--seed must be 1 (generation), 2 (client) or 3 (context), got ${argv[argv.indexOf("--seed") + 1]}`)

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const start = () => { passes = 0; counting = true; queueMicrotask(spin) }
const stop = () => { counting = false; return passes }

/** [what, pass] in the order they happened. */
const marks = []
const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
const respond = () => { marks.push(["respond", passes]); faux.appendResponses([respond]); return fauxAssistantMessage("answer") }
faux.setResponses([respond])

let root
const user = text => ({ role: "user", content: text, timestamp: 0 })
const Reads = defineTask({
	name: "split.context-reads",
	version: 1,
	initial: () => ({ phase: "run" }),
	phases: {
		run: async (_task, runtime, taskContext) => {
			const read = async (name, at) => {
				const before = passes
				const view = await runtime.context(root.id, taskContext, at === undefined ? undefined : { at })
				console.log(`context ${name}: ${passes - before}`)
				return view
			}
			start()
			const first = await read("first (scan)")
			await read("again (kept)")
			await read("cut (kept, at)", first.entries.at(0)?.id)
			await runtime.commit(async tx => { await tx.appendEntry(root.id, { kind: "message", model: [user("more")] }); return undefined }, taskContext)
			await read("after a commit (extending scan)")
			await read("again (kept)")
			stop()
			await runtime.commit(() => ({ status: "terminal", outcome: { status: "completed", result: null } }), taskContext)
		},
	},
	abort: async (_task, runtime, taskContext) => {
		await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), taskContext)
	},
})
const registry = createRegistry()
registry.install(defineExtension({ name: "split", tasks: [Reads] }))
const harness = await Harness.open(await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "split.sqlite")), { models, registry }, context)
root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
// A virtual timer between turns lets the previous turn's background work finish, so each turn starts from an idle Harness.
const settle = () => new Promise(resolve => setTimeout(resolve, 1000))

if (part === 1 || part === 2) {
	const stream = await watchEvents(harness, root.id, context)
	stream.start(events => { for (const e of events) marks.push([`event ${e.type}`, passes]) })
	for (const label of ["cold", "idle"]) {
		marks.length = 0
		start()
		const submission = await root.submit({ type: "input", content: label }, context)
		const submitted = passes
		const record = await submission.wait(context)
		const waited = stop()
		const published = marks.find(m => m[0].startsWith("event "))[1]
		const last = marks.at(-1)[1]
		if (part === 1) {
			for (const [what, n] of marks) console.log(`${label} ${what}: ${n - published} after the publication`)
		} else {
			console.log(`${label} publication: ${published} after the call to submit (the line job's start tick: #enqueue starts it through this.#tail.then(job), session.ts :530-531)`)
			console.log(`${label} publication: ${submitted - published} before submit resolves`)
			console.log(`${label} wait (${record.status}): ${waited - last} after the turn's last event`)
		}
		await settle()
	}
	await stream.stop()
} else {
	// One turn first, so the reads have a range to scan, keep and cut.
	await (await root.submit({ type: "input", content: "first" }, context)).wait(context)
	await settle()
	const id = await harness.commit(tx => tx.createTask(Reads, {}, { ownership: { kind: "conversation" }, conversationId: root.id }), context)
	harness.resume()
	const settled = await harness.waitForTask(id, context)
	console.log(`task ${settled.state.outcome.status}`)
}
await harness.close(context)
