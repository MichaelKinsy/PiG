// The microtask passes of the Session's own awaits over the storage profiles (generated into durable/core/session/hopcost/testdata/session_ticks.json
// by tools/gen-hopticks.mjs): what a Transaction method costs its callback (`tx.task(id)` from the Session's record cache or from Storage,
// `tx.appendEntry`, `tx.createTask`, `tx.doc`...), and what a whole `session.commit` takes from its call to its resumption. A count is the
// passes of a self-requeuing microtask, as storage-ticks.mjs counts them. The Session is the reference's own (createSession) over the
// storage storage-ticks.mjs opens: `--storage memory` is the gate's lazy proxy (profile gate), `--storage sqlite` the capsql facade
// (gate-capsql) or, with CONTRACT_STORAGE_PROFILE=node, the Node adapter.
//   warm: the Session that wrote the records (its CachedStorage, conversation tails and document tracker know them)
//   cold: a second Session over the same Storage that has read nothing
// Output: `<name> <passes>`; `commit ...` lines count from the call of `session.commit` to the resumption of its caller.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { createSession, defineDoc, defineTask } from "@earendil-works/pi-durable"
import { MemoryStorage } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const argv = process.argv.slice(2)
const kind = argv[argv.indexOf("--storage") + 1]
const storage = kind === "memory" ? new MemoryStorage() : await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "session-ticks.sqlite"))

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const idle = () => new Promise(resolve => setTimeout(resolve, 1000))
const out = []
async function measure(name, call) {
	passes = 0; counting = true; queueMicrotask(spin)
	try { return await call() } finally { counting = false; out.push(`${name} ${passes}`) }
}
// A line job is measured from inside its callback: the Session was idle when `commit` was called.
const inJob = (session, name, work) => session.commit(async tx => { await measure(name, () => work(tx)) }, context)
const user = text => ({ kind: "pi.user", model: [{ role: "user", content: text, timestamp: 1 }] })

const Task = defineTask({
	name: "ticks.task", version: 1, initial: () => ({ phase: "run" }),
	phases: { run: async () => {} },
	abort: async () => {},
})
const Doc = defineDoc({ checkpointWhen: () => false, kind: "ticks.doc", version: 1, scope: "conversation", history: "rewindable", fork: "current", initial: () => ({ n: 0 }) })
const TaskDoc = defineDoc({ checkpointWhen: () => false, kind: "ticks.taskdoc", version: 1, scope: "task", initial: () => ({ n: 0 }) })

const warm = createSession(storage, { now: () => 1000 })
const conversation = await warm.commit(tx => tx.createConversation({ ownership: { kind: "ownerless" } }), context)
const cid = conversation.id
const entry = await warm.commit(tx => tx.appendEntry(cid, user("a")), context)
const task = await warm.commit(tx => tx.createTask(Task, {}, { ownership: { kind: "conversation" }, conversationId: cid }), context)
const submission = await warm.commit(tx => tx.createSubmission({ conversationId: cid, requestId: "r1", type: "input", status: "placed", entry: entry.id }), context)
await warm.commit(async tx => { (await tx.doc(Doc, cid)).n = 1 }, context)
const owned = await warm.commit(tx => tx.createTask(Task, {}, { ownership: { kind: "task", taskId: task } }), context)
await warm.commit(async tx => { (await tx.doc(TaskDoc, owned)).n = 1 }, context)
const bare = await warm.commit(tx => tx.createTask(Task, {}, { ownership: { kind: "conversation" }, conversationId: cid }), context)
// A cold Session is created for every call, so nothing an earlier call read is known.
const coldSession = () => createSession(storage, { now: () => 1000 })

for (const label of ["warm", "cold"]) {
	const sessionOf = () => (label === "warm" ? warm : coldSession())
	await idle()
	await inJob(sessionOf(), `${label} tx.conversation`, tx => tx.conversation(cid))
	await idle()
	await inJob(sessionOf(), `${label} tx.task`, tx => tx.task(task))
	await idle()
	await sessionOf().commit(async tx => { await tx.task(task); await measure(`${label} tx.task (second)`, () => tx.task(task)) }, context)
	await idle()
	await inJob(sessionOf(), `${label} tx.submission`, tx => tx.submission(submission.id))
	await idle()
	await inJob(sessionOf(), `${label} tx.submissionByRequest`, tx => tx.submissionByRequest(cid, "r1"))
	await idle()
	await inJob(sessionOf(), `${label} tx.entry`, tx => tx.entry(entry.id))
	await idle()
	await inJob(sessionOf(), `${label} tx.scanEntries`, tx => tx.scanEntries({ conversationId: cid }, 10))
	await idle()
	await inJob(sessionOf(), `${label} tx.scanTasks`, tx => tx.scanTasks({}, 10))
	await idle()
	await inJob(sessionOf(), `${label} tx.latestEntryId`, tx => tx.latestEntryId(cid))
	await idle()
	await inJob(sessionOf(), `${label} tx.latestHeadMarker`, tx => tx.latestHeadMarker(cid))
	await idle()
	await inJob(sessionOf(), `${label} tx.appendEntry`, tx => tx.appendEntry(cid, user("b")))
	await idle()
	await inJob(sessionOf(), `${label} tx.createTask`, tx => tx.createTask(Task, {}, { ownership: { kind: "conversation" }, conversationId: cid }))
	await idle()
	await inJob(sessionOf(), `${label} tx.createTask (owned)`, tx => tx.createTask(Task, {}, { ownership: { kind: "task", taskId: task } }))
	await idle()
	await inJob(sessionOf(), `${label} tx.createSubmission`, tx => tx.createSubmission({ conversationId: cid, requestId: `${label}-r`, type: "input", status: "placed", entry: entry.id }))
	await idle()
	await inJob(sessionOf(), `${label} tx.createConversation`, tx => tx.createConversation({ ownership: { kind: "ownerless" } }))
	await idle()
	await inJob(sessionOf(), `${label} tx.createConversation (owned)`, tx => tx.createConversation({ ownership: { kind: "task", taskId: task } }))
	await idle()
	await inJob(sessionOf(), `${label} tx.doc (stored)`, tx => tx.doc(Doc, cid))
	await idle()
	await inJob(sessionOf(), `${label} tx.doc (task)`, tx => tx.doc(TaskDoc, owned))
	await idle()
	await sessionOf().commit(async tx => { await tx.doc(Doc, cid); await measure(`${label} tx.doc (second)`, () => tx.doc(Doc, cid)) }, context)
}
// A document the Session has never seen at its address: created in the commit.
await idle()
{
	const fresh = (await warm.commit(tx => tx.createConversation({ ownership: { kind: "ownerless" } }), context)).id
	await idle()
	await inJob(warm, "warm tx.doc (absent)", tx => tx.doc(Doc, fresh))
	await idle()
	await inJob(warm, "warm tx.doc (task, absent)", tx => tx.doc(TaskDoc, bare))
	await idle()
	await warm.commit(async tx => {
		const created = (await tx.createConversation({ ownership: { kind: "ownerless" } })).id
		await measure("warm tx.appendEntry (conversation created here)", () => tx.appendEntry(created, user("h")))
	}, context)
}

// Whole commits on an idle line, from the call to the caller's resumption.
const commits = async (label, sessionOf, spec) => {
	for (const [name, change] of Object.entries(spec)) {
		await idle()
		const session = sessionOf()
		await measure(`commit ${label} ${name}`, () => session.commit(change, context))
	}
}
const terminal = async (tx, id) => { const record = await tx.task(id); tx.setTask({ ...record, state: { status: "terminal", outcome: { status: "completed", result: null } } }) }
await commits("warm", () => warm, {
	"(no write)": async () => {},
	"(entry)": async tx => { await tx.appendEntry(cid, user("c")) },
	"(task)": async tx => { await tx.createTask(Task, {}, { ownership: { kind: "conversation" }, conversationId: cid }) },
	"(entry, task)": async tx => { await tx.appendEntry(cid, user("d")); await tx.createTask(Task, {}, { ownership: { kind: "conversation" }, conversationId: cid }) },
	"(task owned)": async tx => { await tx.createTask(Task, {}, { ownership: { kind: "task", taskId: task } }) },
	"(2 tasks owned)": async tx => { await tx.createTask(Task, {}, { ownership: { kind: "task", taskId: task } }); await tx.createTask(Task, {}, { ownership: { kind: "task", taskId: task } }) },
	"(doc)": async tx => { (await tx.doc(Doc, cid)).n = 2 },
	"(task terminal, documents known)": tx => terminal(tx, owned),
})
await commits("cold", coldSession, {
	"(entry)": async tx => { await tx.appendEntry(cid, user("e")) },
	"(doc)": async tx => { (await tx.doc(Doc, cid)).n = 3 },
	"(task terminal)": tx => terminal(tx, task),
})
// Two jobs queued together: the second job's callback runs while the first one's part is still unpublished.
await idle()
{
	passes = 0; counting = true; queueMicrotask(spin)
	const first = warm.commit(async tx => { await tx.appendEntry(cid, user("f")) }, context).then(() => out.push(`commit pair first ${passes}`))
	const second = warm.commit(async tx => { await tx.appendEntry(cid, user("g")) }, context).then(() => out.push(`commit pair second ${passes}`))
	await Promise.all([first, second])
	counting = false
}
for (const line of out) console.log(line)
// The Sessions share one Storage, which neither closes.
process.exit(0)
