// The microtask passes each Storage method of a Pi adapter spends on an idle queue (generated into durable/core/session/hopcost/testdata/ticks.json
// by tools/gen-hopticks.mjs): from the call to the resumption of a caller that awaits it, counted by a self-requeuing microtask exactly as
// client-ticks.mjs counts client calls. The adapters are the reference's own, instrumented from outside: the gate's hooks hand back the
// capsql-backed SqliteStorage (--storage sqlite: gate-capsql, or node with CONTRACT_STORAGE_PROFILE=node) and the lazy MemoryStorage
// proxy (--storage memory: gate). Every record is the smallest Pi accepts; every call starts from an idle queue (a virtual timer between calls).
// Output: one line per call, `<method>[<shape>] <passes>`, and after each commit `commitTail[<shape>] <passes>`, the passes from its COMMIT
// statement to the caller's resumption.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { MemoryStorage } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const argv = process.argv.slice(2)
const kind = argv[argv.indexOf("--storage") + 1]
const storage = kind === "memory" ? new MemoryStorage() : await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "storage-ticks.sqlite"))

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const idle = () => new Promise(resolve => setTimeout(resolve, 1000))
const out = []
// capsql reports each COMMIT statement (lib/capsql.mjs): a commit's `commitTail` is the passes from it to the caller's resumption.
let committedAt
globalThis.__contractCommitted = () => { committedAt = passes }
async function measure(name, call) {
	await idle()
	passes = 0; counting = true; committedAt = undefined; queueMicrotask(spin)
	const result = await call()
	counting = false
	out.push(`${name} ${passes}`)
	if (name.startsWith("commit[")) out.push(`commitTail[${name.slice("commit[".length)} ${passes - committedAt}`)
	return result
}

const conversation = id => ({ type: "conversation", value: { id } })
const entry = (id, conversationId) => ({ type: "entry", value: { model: [{ role: "user", content: "x", timestamp: 1 }], kind: "pi.user", id, conversationId } })
const task = (id, conversationId) => ({ type: "task", value: { id, conversationId, kind: "pi.generation", version: 1, input: {}, background: false, abortRequested: false, state: { status: "running", checkpoint: { phase: "prepare", attempt: 1 } }, startedAt: 1 } })
const submission = (id, conversationId) => ({ type: "submission", value: { id, conversationId, requestId: `r${id}`, type: "input", status: "placed", entry: 1 } })

await measure("mintId", () => storage.mintId())
// The commit shapes the core charges (hopcost.Writes with Tables only): the number of table writes decides the cost.
await measure("commit[conversation]", () => storage.commit([conversation(1)], context))
await measure("commit[entry]", () => storage.commit([entry(2, 1)], context))
await measure("commit[task]", () => storage.commit([task(3, 1)], context))
await measure("commit[submission]", () => storage.commit([submission(4, 1)], context))
await measure("commit[task,task]", () => storage.commit([task(5, 1), task(6, 1)], context))
await measure("commit[entry,task]", () => storage.commit([entry(7, 1), task(8, 1)], context))
await measure("commit[entry,submission,task]", () => storage.commit([entry(9, 1), submission(10, 1), task(11, 1)], context))
await measure("commit[entry,entry,submission,task]", () => storage.commit([entry(12, 1), entry(13, 1), submission(14, 1), task(15, 1)], context))
// The reads (hopcost.Read).
await measure("conversation", () => storage.conversation(1, context))
await measure("task", () => storage.task(3, context))
await measure("submission", () => storage.submission(4, context))
await measure("scanTasks", () => storage.scanTasks({}, 10, undefined, context))
await measure("entry(id)", () => storage.entry(2, context))
await measure("entry(conversation,id)", () => storage.entry(1, 2, context))
await measure("scanEntries", () => storage.scanEntries({ conversationId: 1 }, 10, undefined, context))
await measure("findLatestHeadMarker", () => storage.findLatestHeadMarker(1, undefined, context))
for (const line of out) console.log(line)
await (storage.close?.(context) ?? Promise.resolve())
