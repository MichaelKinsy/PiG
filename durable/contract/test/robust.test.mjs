// The robustness proofs' arithmetic and ledger (lib/robust.mjs): the induction step reads bytes per turn from the samples it
// is given, a run that stopped early is not a pass, and the crash ledger names a lost, a duplicated and a changed submission.
// The crash scenario's own control (pi against pi at two crash points) runs the scenario program for real.
import assert from "node:assert/strict"
import { mkdtempSync, readdirSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import test from "node:test"
import { seed } from "../cli/fixtures.mjs"
import { ensureBuilt } from "../lib/gate.mjs"
import { runBench } from "../lib/impl.mjs"
import { readSamples } from "../lib/memory.mjs"
import { readTrace } from "../lib/rowdiff.mjs"
import { crash, induction, inductionStep, ledger, ledgerProblems, slope, soak, timeTravel } from "../lib/robust.mjs"
import { copyStore } from "../lib/store.mjs"

const KiB = 1024
const run = (turns, heapAt) => Array.from({ length: turns }, (_, turn) => ({ turn, heap: heapAt(turn + 1), reserved: 6 * 1024 * KiB }))

test("a flat heap is bounded and a growing one is measured in bytes per turn", () => {
	const flat = inductionStep(run(450, () => 2_000_000), { n: 100, warmup: 50, bound: 0 })
	assert.deepEqual(flat.points, [150, 250, 450])
	assert.equal(flat.metrics.heap.bounded, true)
	assert.equal(flat.metrics.heap.step2, 0)
	// 40 KiB per turn: the steps and the slope say so, and the bound fails it
	const leak = inductionStep(run(450, turn => 1_000_000 + 40 * KiB * turn), { n: 100, warmup: 50, bound: 0 })
	assert.equal(leak.metrics.heap.step1, 40 * KiB)
	assert.equal(leak.metrics.heap.step2, 40 * KiB)
	assert.equal(Math.round(leak.metrics.heap.slope), 40 * KiB)
	assert.equal(leak.metrics.heap.bounded, false)
	assert.equal(leak.metrics.reserved.bounded, true)
	// the bound judges 2N->4N: a leak that starts after W+2N fails it although N->2N is flat, and growth that stops at
	// W+2N (a warm-up longer than W) passes it although N->2N grew
	const late = inductionStep(run(450, turn => 2_000_000 + 40 * KiB * Math.max(0, turn - 250)), { n: 100, warmup: 50, bound: 0 })
	assert.equal(late.metrics.heap.step1, 0)
	assert.equal(late.metrics.heap.step2, 40 * KiB)
	assert.equal(late.metrics.heap.bounded, false)
	const settles = inductionStep(run(450, turn => 2_000_000 + 40 * KiB * Math.min(turn, 250)), { n: 100, warmup: 50, bound: 0 })
	assert.equal(settles.metrics.heap.step1, 40 * KiB)
	assert.equal(settles.metrics.heap.bounded, true)
	// a bound of 64 bytes per turn tolerates collector noise, not a leak
	assert.equal(inductionStep(run(450, turn => 2_000_000 + (turn % 2) * 64), { n: 100, warmup: 50, bound: 64 }).metrics.heap.bounded, true)
})

test("a sawtooth that releases in batches is bounded at any phase; a sawtooth on a leak is not", () => {
	// 12 KiB per turn held, released every 90 turns (a batch release): single samples at the points land at different
	// phases, the envelopes do not.
	let pointGrowth = 0
	for (const phase of [0, 17, 45, 89]) {
		const saw = inductionStep(run(450, turn => 2_000_000 + 12 * KiB * ((turn + phase) % 90)), { n: 100, warmup: 50, bound: 0 })
		assert.equal(saw.metrics.heap.bounded, true, `phase ${phase}`)
		assert.equal(saw.metrics.heap.step1, 0)
		pointGrowth = Math.max(pointGrowth, (saw.metrics.heap.atPoint[2] - saw.metrics.heap.atPoint[1]) / 200)
	}
	assert.ok(pointGrowth > 1024, "single samples at the points would call some phase a leak")
	const leaky = inductionStep(run(450, turn => 2_000_000 + 12 * KiB * (turn % 90) + 2 * KiB * turn), { n: 100, warmup: 50, bound: 1024 })
	assert.equal(leaky.metrics.heap.bounded, false)
	assert.ok(leaky.metrics.heap.step2 >= 2 * KiB - 12 * KiB * 90 / 200, `step2 ${leaky.metrics.heap.step2}`)
})

test("a run that stopped before 4N turns is incomplete, never bounded", () => {
	const r = inductionStep(run(300, turn => 1_000_000 + 10 * KiB * turn), { n: 100, warmup: 50, bound: 1e9 })
	assert.match(r.incomplete, /stopped after 300 turns; the induction needs 450/)
	assert.equal(r.metrics.heap.bounded, false)
	assert.equal(Math.round(r.metrics.heap.slope), 10 * KiB)
	assert.equal(slope([[0, 1], [1, 3], [2, 5]]), 2)
})

/** A store with the three tables the ledger reads. */
function store(dir, name, { conversations, submissions, entries }) {
	const path = join(dir, `${name}.sqlite`)
	const db = new DatabaseSync(path)
	db.exec("CREATE TABLE conversations (id INTEGER PRIMARY KEY, record TEXT); CREATE TABLE submissions (id INTEGER PRIMARY KEY, record TEXT); CREATE TABLE entries (id INTEGER PRIMARY KEY, record TEXT)")
	const put = (table, rows) => { const s = db.prepare(`INSERT INTO ${table} (id, record) VALUES (?, ?)`); for (const r of rows) s.run(r.id, JSON.stringify(r)) }
	put("conversations", conversations); put("submissions", submissions); put("entries", entries)
	db.close()
	return path
}
const user = (id, conversationId, text) => ({ id, conversationId, kind: "pi.user", model: [{ role: "user", content: text }] })

test("the ledger names a lost, a duplicated and a changed submission, and a repeated input", () => {
	const dir = mkdtempSync(join(tmpdir(), "robust-ledger-"))
	try {
		const conversations = [{ id: 1 }, { id: 5, owner: { taskId: 4 } }, { id: 9, parent: { conversationId: 1 } }]
		const sub = (id, conversationId, requestId, status) => ({ id, conversationId, requestId, status })
		const want = ledger(store(dir, "want", {
			conversations,
			submissions: [sub(2, 1, "t1", "done"), sub(6, 5, "subagent:4", "done"), sub(7, 1, "compaction:3", "done"), sub(10, 9, "f1", "done")],
			entries: [user(3, 1, "turn t1"), user(8, 5, "task for t1"), user(11, 9, "turn f1")],
		}))
		assert.deepEqual(want.requests, { "child/subagent": ["done"], "fork/f1": ["done"], "root/t1": ["done"] })
		// the same history with other ids (a restart minted them) and its own compaction placements is the same ledger
		const same = ledger(store(dir, "same", {
			conversations: [{ id: 1 }, { id: 6, owner: { taskId: 5 } }, { id: 12, parent: { conversationId: 1 } }],
			submissions: [sub(2, 1, "t1", "done"), sub(7, 6, "subagent:5", "done"), sub(8, 1, "compaction:3", "done"), sub(9, 1, "compaction:4", "done"), sub(13, 12, "f1", "done")],
			entries: [user(3, 1, "turn t1"), user(10, 6, "task for t1"), user(14, 12, "turn f1")],
		}))
		assert.deepEqual(ledgerProblems(same, want), [])
		const bad = ledger(store(dir, "bad", {
			conversations,
			submissions: [sub(2, 1, "t1", "done"), sub(6, 5, "subagent:4", "done"), sub(12, 5, "subagent:4b", "done"), sub(10, 9, "f1", "pending")],
			entries: [user(3, 1, "turn t1"), user(4, 1, "turn t1"), user(8, 5, "task for t1"), user(11, 9, "turn f1")],
		}))
		assert.deepEqual(ledgerProblems(bad, want), [
			"submission child/subagent: 2 rows (done, done), uninterrupted 1",
			"submission fork/f1 pending, uninterrupted done",
			'input "root/turn t1" 2 times, uninterrupted 1',
		])
		const lost = ledger(store(dir, "lost", { conversations, submissions: [sub(2, 1, "t1", "done")], entries: [user(3, 1, "turn t1")] }))
		assert.deepEqual(ledgerProblems(lost, want), ["submission child/subagent lost", "submission fork/f1 lost", 'input "child/task for t1" 0 times, uninterrupted 1', 'input "fork/turn f1" 0 times, uninterrupted 1'])
	} finally { rmSync(dir, { recursive: true, force: true }) }
})

test("the crash scenario restarts after a crash and the reference matches itself", { timeout: 300_000 }, async () => {
	const out = mkdtempSync(join(tmpdir(), "robust-crash-"))
	try {
		// pi as the candidate: the control of the proof. The first commit and one in the inbox phase.
		const r = await crash({ impls: ["pi"], out, points: [1, 60] })
		assert.equal(r.uninterrupted.pi.ok, true)
		assert.ok(r.commits > 100, `the scenario makes ${r.commits} commits`)
		assert.deepEqual(Object.keys(r.ledger.requests), ["child/subagent", "fork/f1", "root/t1", "root/t2", "root/t3", "root/t4", "root/t5", "root/t6", "root/t7", "root/w1"])
		assert.equal(r.points.length, 4)
		for (const p of r.points) assert.equal(p.ok, true, JSON.stringify(p))
	} finally { rmSync(out, { recursive: true, force: true }) }
})

test("an induction run samples the core's heap after a collection, what it reserved, and the host's heap", { timeout: 300_000 }, async () => {
	await ensureBuilt("native")
	const out = mkdtempSync(join(tmpdir(), "robust-induction-"))
	try {
		// The native build: cmd/corenative answers mem_stats with its live heap after runtime.GC (slot 6) and its runtime's Sys (slot 7).
		const r = await induction({ impl: "native", n: 2, warmup: 1, tools: 1, out, bound: 1e9 })
		assert.equal(r.stopped, undefined)
		assert.deepEqual(r.points, [3, 5, 9])
		const samples = readSamples(r.mem)
		assert.equal(samples.length, 9)
		for (const s of samples) {
			assert.equal(s.cores, 1)
			assert.ok(s.heap > 100_000 && s.heap < s.reserved, `heap ${s.heap} of ${s.reserved} reserved`)
			assert.ok(s.jsHeap > 1_000_000)
		}
		assert.ok(samples.every((s, i) => i === 0 || s.wallMs >= samples[i - 1].wallMs), "wallMs is the real clock")
		assert.deepEqual(Object.keys(r.metrics), ["heap", "reserved", "jsHeap", "rss"])
	} finally { rmSync(out, { recursive: true, force: true }) }
})

test("a soak samples every K rounds across every object's core and judges per object-turn", { timeout: 300_000 }, async () => {
	await ensureBuilt("native")
	const out = mkdtempSync(join(tmpdir(), "robust-soak-"))
	try {
		await assert.rejects(soak({ impl: "native", objects: 2, rounds: 10, warmup: 3, every: 2, out }), /must be 4N/)
		// An 8,000-token window: compaction starts once the history passes Pi's kept-recent budget (after ~25 turns of 8 lookups).
		const r = await soak({ impl: "native", objects: 2, rounds: 32, warmup: 8, every: 2, tools: 8, window: 8000, out, bound: 1e9 })
		assert.equal(r.stopped, undefined)
		assert.equal(r.turns, 64)
		assert.deepEqual(r.points, [14, 20, 32])
		const samples = readSamples(r.mem)
		// one sample after every second round (turn indexes 1, 3, ..., 31), each summed over every core instance the objects use
		// (one shared instance or one per object, as the host's pool decides)
		assert.deepEqual(samples.map(s => s.turn), Array.from({ length: 16 }, (_, i) => 2 * i + 1))
		for (const s of samples) assert.ok(s.cores >= 1 && s.cores <= 2 && s.heap > 0, JSON.stringify(s))
		const h = r.metrics.heap
		assert.equal(h.perTurn.step2, h.step2 / 2)
		assert.equal(h.bounded, true)
		// compaction ran: the first object's store has compaction tasks
		const { DatabaseSync } = await import("node:sqlite")
		const db = new DatabaseSync(r.stores[0], { readOnly: true })
		try { assert.ok(db.prepare("SELECT count(*) n FROM tasks WHERE json_extract(record, '$.kind') = 'pi.compaction'").get().n > 0) } finally { db.close() }
	} finally { rmSync(out, { recursive: true, force: true }) }
})

test("time travel on a W1 history equals pi on native and times every operation", { timeout: 300_000 }, async () => {
	await ensureBuilt("native")
	const out = mkdtempSync(join(tmpdir(), "robust-timetravel-"))
	try {
		const r = await timeTravel({ impls: ["native"], size: 50, atTurn: 20, scrub: 3, out })
		assert.equal(r.runs.pi.ok, true)
		assert.equal(r.runs.native.ok, true, JSON.stringify(r.runs.native))
		const ops = r.runs.native.ops.map(o => o.op)
		assert.deepEqual([...new Set(ops)], ["open", "root", "locate", "asof", "snapshotAsOf", "snapshotAsOf-first", "scrub", "fork", "fork-context", "fork-turn", "root-context"])
		assert.equal(ops.filter(o => o === "scrub").length, 3)
		assert.ok(r.runs.native.ops.every(o => o.ms >= 0))
		// what pi printed: the located turn, then one line per operation
		const printed = readTrace(join(out, "pi", "time-travel.trace")).filter(l => l.t === "out" && l.stream === "stdout").map(l => l.text)
		assert.match(printed[0], /^locate h20 user \d+ answer \d+ scanned \d+$/)
		assert.equal(printed.filter(l => l.startsWith("scrub ")).length, 3)
		assert.match(printed.at(-2), /^fork turn done messages \d+ fp [0-9a-f]{16}$/)
	} finally { rmSync(out, { recursive: true, force: true }) }
})

test("bench --objects runs every measured turn on each object's own copy and writes nothing beside the store", { timeout: 300_000 }, async () => {
	const out = mkdtempSync(join(tmpdir(), "robust-objects-"))
	try {
		// The store is a private copy of the 50-turn fixture, so a file the run leaves beside it is visible.
		const store = copyStore(await seed(50), join(out, "fixture", "w1-50.sqlite"))
		const before = readdirSync(join(out, "fixture")).sort()
		const r = await runBench("pi", { store, out, name: "objects", turns: 2, tools: 1, variant: "incr", objects: 3 })
		assert.equal(r.code, 0, r.stderr.slice(-600))
		assert.deepEqual(readdirSync(join(out, "fixture")).sort(), before)
		assert.equal(r.stores.length, 3)
		for (const path of r.stores) {
			const db = new DatabaseSync(path, { readOnly: true })
			const measured = db.prepare("SELECT count(*) AS n FROM submissions WHERE json_extract(record, '$.requestId') LIKE 'm%'").get().n
			const seeded = db.prepare("SELECT count(*) AS n FROM submissions WHERE json_extract(record, '$.requestId') LIKE 'h%'").get().n
			db.close()
			assert.deepEqual({ measured, seeded }, { measured: 2, seeded: 50 }, path)
		}
	} finally { rmSync(out, { recursive: true, force: true }) }
})
