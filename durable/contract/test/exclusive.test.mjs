// A row whose upstream test asserts inside a real-time window the run itself has to fill (corpus `exclusive`) runs alone, and its
// reference run and candidate run take turns. U-codemode's `times a script out across a restart` gives a script 300 ms to start the
// QuickJS sandbox and reach a tool, which costs 150-230 ms of CPU; the gate ran it beside seven other rows (two vitest runs of four
// forks each), and the reference or the candidate, whichever the host starved, missed the window: of six full runs the candidate
// missed it in four and the reference in one. The tests below fix the order the gate starts and ends work in, so no clock is involved.
import assert from "node:assert/strict"
import test from "node:test"
import { loadCorpus, validateCorpus } from "../lib/corpus.mjs"
import { runRows, runVitestRow } from "../lib/gate.mjs"

const tick = () => new Promise(resolve => setTimeout(resolve, 5))

test("runRows: an exclusive row runs alone and before every other row; the others share the concurrency", async () => {
	const rows = ["a", "b", "x", "c", "y", "d"].map(id => ({ id, ...(id === "x" || id === "y" ? { exclusive: true } : {}) }))
	let active = 0, peak = 0
	const log = []
	const results = await runRows(rows, 2, async row => {
		log.push(`start ${row.id} alone=${active === 0}`)
		active++
		peak = Math.max(peak, active)
		await tick()
		if (row.exclusive) assert.equal(active, 1, `${row.id} ran beside another row`)
		active--
		log.push(`end ${row.id}`)
		return { id: row.id }
	})
	assert.deepEqual(log.slice(0, 4), ["start x alone=true", "end x", "start y alone=true", "end y"])
	assert.equal(peak, 2, "the other rows keep their concurrency")
	assert.deepEqual(results.map(r => r.id).sort(), ["a", "b", "c", "d", "x", "y"])
})

// The two runs of a vitest row: `run` stands in for vitestRun and logs when each starts and ends.
const file = name => new Map([[`${name}.test.ts`, { status: "passed", tests: new Map([["it", "passed"]]), failures: new Map() }]])
const stub = log => async (impl, _ctx, include) => {
	const name = include.replace(/^test\//, "").replace(/\.test\.ts$/, "")
	log.push(`start ${impl.name}`)
	await tick()
	log.push(`end ${impl.name}`)
	return Object.assign(file(name), { timedOut: false, guard: [] })
}
const row = (name, extra = {}) => ({ id: `U-${name}`, source: `packages/durable/test/${name}.test.ts`, ...extra })
// The gate calls a runner as RUNNERS[row.runner](row, impl, ctx, corpus): the arguments after ctx are the dispatcher's, not the test's.
const corpus = { scenario: [] }
const call = (r, impl, log) => runVitestRow(r, impl, { out: "x", run: stub(log) }, corpus)

test("runVitestRow: the reference's run and the candidate's start together", async () => {
	const log = []
	const result = await call(row("shared"), { name: "cand-shared", vitest: true, candidateDir: "x", env: {} }, log)
	assert.equal(result.status, "pass", JSON.stringify(result))
	assert.deepEqual(log.slice(0, 2), ["start ref", "start cand-shared"])
})

test("runVitestRow: an exclusive row's two runs take turns, the reference first", async () => {
	const log = []
	const result = await call(row("window", { exclusive: true }), { name: "cand-window", vitest: true, candidateDir: "x", env: {} }, log)
	assert.equal(result.status, "pass", JSON.stringify(result))
	assert.deepEqual(log, ["start ref", "end ref", "start cand-window", "end cand-window"])
})

test("runVitestRow: the control of an exclusive row runs the reference once", async () => {
	const log = []
	const result = await call(row("control", { exclusive: true }), { name: "pi", control: true }, log)
	assert.equal(result.status, "pass", JSON.stringify(result))
	assert.deepEqual(log, ["start ref", "end ref"])
})

test("corpus: exclusive is true and a field of vitest rows; the rows that name real-time tests or windows carry it", () => {
	const base = { id: "U-x", family: "upstream-test", runner: "vitest", owner: "dcore-session", status: "ready" }
	const problems = r => validateCorpus({ scenario: [{ ...base, ...r }] })
	assert.deepEqual(problems({ exclusive: true }), [])
	assert.match(problems({ exclusive: false }).join("\n"), /exclusive is true/)
	assert.match(problems({ exclusive: true, runner: "bench" }).join("\n"), /exclusive is true/)
	const exclusive = loadCorpus().scenario.filter(s => s.exclusive).map(s => s.id)
	assert.deepEqual(exclusive, ["U-codemode", "U-harness-tools"])
	for (const s of loadCorpus().scenario) if (s.real_time_tests) assert.equal(s.exclusive, true, `${s.id} names real-time tests; the gate must not run it beside other rows`)
})
