// The microtask passes each Storage method of a Pi adapter spends on an idle queue (generated into durable/core/session/hopcost/testdata/ticks.json
// by tools/gen-hopticks.mjs): from the call to the resumption of a caller that awaits it, counted by a self-requeuing microtask exactly as
// client-ticks.mjs counts client calls. The adapters are the reference's own, instrumented from outside: the gate's hooks hand back the
// capsql-backed SqliteStorage (--storage sqlite: gate-capsql, or node with CONTRACT_STORAGE_PROFILE=node) and the lazy MemoryStorage
// proxy (--storage memory: gate). Every record is the smallest Pi accepts; every call starts from an idle queue (a virtual timer between calls).
// Output: one line per call, `<method>[<shape>] <passes>`, and after each commit `commitTail[<shape>] <passes>`, the passes from its COMMIT
// statement to the caller's resumption, and `receiveTail[<shape>] <passes>`, the passes from the last `receive` callback to it. A shape lists
// the batches of the call separated by `;`, each batch's writes separated by `,`: `conversation`, `entry`, `task`, `submission`, and the
// document writes `doc.create:base[:latest]`, `doc.change:delta`, `doc.change:base[:latest]`, `doc.retire[:latest]`, `doc.copy`; `@a` marks
// writes of one document address.
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
let committedAt, receivedAt
globalThis.__contractCommitted = () => { committedAt = passes }
async function measure(name, call) {
	await idle()
	passes = 0; counting = true; committedAt = undefined; queueMicrotask(spin)
	const result = await call()
	counting = false
	out.push(`${name} ${passes}`)
	if (name.startsWith("commit[")) {
		out.push(`commitTail[${name.slice("commit[".length)} ${passes - committedAt}`)
		out.push(`receiveTail[${name.slice("commit[".length)} ${passes - receivedAt}`)
	}
	return result
}

// Storage.commit(batches, receive, context) calls `receive(index, seq)` once per durable batch, after all of them are durable.
const receive = () => { receivedAt = passes }
const conversation = id => ({ type: "conversation", value: { id } })
const entry = (id, conversationId) => ({ type: "entry", value: { model: [{ role: "user", content: "x", timestamp: 1 }], kind: "pi.user", id, conversationId } })
const task = (id, conversationId) => ({ type: "task", value: { id, conversationId, kind: "pi.generation", version: 1, input: {}, background: false, abortRequested: false, state: { status: "running", checkpoint: { phase: "prepare", attempt: 1 } }, startedAt: 1 } })
const submission = (id, conversationId) => ({ type: "submission", value: { id, conversationId, requestId: `r${id}`, type: "input", status: "placed", entry: 1 } })

await measure("mintId", () => storage.mintId())
// The commit shapes the core charges (hopcost.Writes with Tables only): the number of table writes decides the cost.
await measure("commit[conversation]", () => storage.commit([[conversation(1)]], receive, context))
await measure("commit[entry]", () => storage.commit([[entry(2, 1)]], receive, context))
await measure("commit[task]", () => storage.commit([[task(3, 1)]], receive, context))
await measure("commit[submission]", () => storage.commit([[submission(4, 1)]], receive, context))
await measure("commit[task,task]", () => storage.commit([[task(5, 1), task(6, 1)]], receive, context))
await measure("commit[entry,task]", () => storage.commit([[entry(7, 1), task(8, 1)]], receive, context))
await measure("commit[entry,submission,task]", () => storage.commit([[entry(9, 1), submission(10, 1), task(11, 1)]], receive, context))
await measure("commit[entry,entry,submission,task]", () => storage.commit([[entry(12, 1), entry(13, 1), submission(14, 1), task(15, 1)]], receive, context))
// A call of several batches (the writer's group commit): one COMMIT per batch, `commitTail` counted from the last.
await measure("commit[task;task]", () => storage.commit([[task(16, 1)], [task(17, 1)]], receive, context))
await measure("commit[entry;entry;task]", () => storage.commit([[entry(18, 1)], [entry(19, 1)], [task(20, 1)]], receive, context))
await measure("commit[entry,task;submission]", () => storage.commit([[entry(21, 1), task(22, 1)], [submission(23, 1)]], receive, context))
// The reads (hopcost.Read).
await measure("conversation", () => storage.conversation(1, context))
await measure("task", () => storage.task(3, context))
await measure("submission", () => storage.submission(4, context))
await measure("scanTasks", () => storage.scanTasks({}, 10, undefined, context))
await measure("entry(id)", () => storage.entry(2, context))
await measure("entry(conversation,id)", () => storage.entry(1, 2, context))
await measure("scanEntries", () => storage.scanEntries({ conversationId: 1 }, 10, undefined, context))
await measure("findLatestHeadMarker", () => storage.findLatestHeadMarker(1, undefined, context))
// Document writes: every document is created by an unmeasured call first when a shape changes or retires it.
let nextDocument = 100
const address = key => ({ kind: "d.k", key, scope: { kind: "conversation", conversationId: 1 } })
const documentRecord = (id, key, history, conversationId = 1) => ({ id, kind: "d.k", key, scope: { kind: "conversation", conversationId }, history, fork: "current" })
const documentCreate = (id, key, history = "rewindable") => ({ type: "document.create", record: documentRecord(id, key, history), content: { kind: "base", version: 1, value: {} } })
const fresh = async (key, history = "rewindable") => { const id = nextDocument++; await storage.commit([[documentCreate(id, key, history)]], () => {}, context); return id }
const documentChange = (id, kind, version = 1) => ({ type: "document.change", id, content: kind === "delta" ? { kind, version, ops: [["r", {}]] } : { kind, version, value: {} } })
const documentRetire = id => ({ type: "document.retire", id })
await measure("commit[doc.create:base]", () => storage.commit([[documentCreate(nextDocument++, "c1")]], receive, context))
await measure("commit[doc.create:base:latest]", () => storage.commit([[documentCreate(nextDocument++, "c2", "latest")]], receive, context))
{ const id = nextDocument++; await measure("commit[doc.create:base,doc.retire]", () => storage.commit([[documentCreate(id, "c3"), documentRetire(id)]], receive, context)) }
const changed = await fresh("e1"), latest = await fresh("e2", "latest"), base = await fresh("e3"), retired = await fresh("e4"), retiredLatest = await fresh("e5", "latest")
await measure("commit[doc.change:delta]", () => storage.commit([[documentChange(changed, "delta")]], receive, context))
await measure("commit[doc.change:base]", () => storage.commit([[documentChange(base, "base", 2)]], receive, context))
await measure("commit[doc.change:base:latest]", () => storage.commit([[documentChange(latest, "base", 2)]], receive, context))
await measure("commit[doc.retire]", () => storage.commit([[documentRetire(retired)]], receive, context))
await measure("commit[doc.retire:latest]", () => storage.commit([[documentRetire(retiredLatest)]], receive, context))
{ const old = await fresh("e6"); await measure("commit[doc.retire@a,doc.create:base@a]", () => storage.commit([[documentRetire(old), documentCreate(nextDocument++, "e6")]], receive, context)) }
{
	await storage.commit([[conversation(24)]], () => {}, context)
	const source = await fresh("e7")
	await measure("commit[doc.copy]", () => storage.commit([[{ type: "document.copy", record: documentRecord(nextDocument++, "e7", "rewindable", 24), source: { id: source, at: "current" } }]], receive, context))
}
await measure("commit[doc.change:delta;doc.change:delta]", () => storage.commit([[documentChange(changed, "delta")], [documentChange(changed, "delta")]], receive, context))
await measure("commit[entry,doc.create:base;task]", () => storage.commit([[entry(25, 1), documentCreate(nextDocument++, "c4")], [task(26, 1)]], receive, context))
// A batch Durable's own checks refuse (an ID that is taken) ends the call; the batches before it stand (`refused`, not a rejection).
await measure("commit[entry;entry!taken]", () => storage.commit([[entry(27, 1)], [entry(2, 1)]], receive, context))
await measure("commit[entry;entry,entry!taken]", () => storage.commit([[entry(28, 1)], [entry(29, 1), entry(2, 1)]], receive, context))
for (const line of out) console.log(line)
await (storage.close?.(context) ?? Promise.resolve())
