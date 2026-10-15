// The interleaving trace tests (CONTRACT section 5.8): the comparator rejects any reordering of a call and a commit, the golden
// files are what Pi records now (determinism and pin drift), and every program row has one.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { loadCorpus } from "../lib/corpus.mjs"
import { compareInterleavings, tokens } from "../lib/interleave.mjs"

const call = (name, tick, phase = "enter") => ({ t: "call", phase, name, tick })
const commit = (seq, tables, tick) => ({ t: "commit", seq, tick, changes: tables.map(t => ({ table: t, op: "insert" })) })
const golden = [call("harness.root", 1), commit(1, ["conversations"], 50), call("harness.root().submit", 52), commit(2, ["tasks"], 100), call("harness.root().submit", 160)]

test("the same calls and commits in the same order are equal, whatever their ticks, and a call's return is not a token", () => {
	const shifted = [call("harness.root", 5), call("harness.root", 6, "return"), commit(1, ["conversations"], 9), call("harness.root().submit", 10), commit(2, ["tasks"], 11), call("harness.root().submit", 12)]
	assert.equal(compareInterleavings(tokens(golden), tokens(shifted)), undefined)
})

test("negative control: a call that enters before the commit it follows in Pi (or after the one it precedes) is a difference, with its ticks", () => {
	const early = [call("harness.root", 1), commit(1, ["conversations"], 50), commit(2, ["tasks"], 51), call("harness.root().submit", 52), call("harness.root().submit", 53)]
	const d = compareInterleavings(tokens(golden), tokens(early))
	assert.equal(d.index, 2)
	assert.match(d.expected[0], /^> harness\.root\(\)\.submit @52/)
	assert.match(d.got[0], /^# 2 tasks:i @51/)
})

test("negative control: a different commit (seq or tables) and a missing token are differences", () => {
	assert.equal(compareInterleavings(tokens(golden), tokens([...golden.slice(0, 3), commit(2, ["entries"], 100), golden[4]])).index, 3)
	assert.equal(compareInterleavings(tokens(golden), tokens(golden.slice(0, 4))).index, 4)
	assert.equal(compareInterleavings(tokens(golden.slice(0, 4)), tokens(golden)).index, 4)
})

const traces = join(import.meta.dirname, "..", "traces")
const rows = loadCorpus().scenario.filter(r => (r.runner === "example" || r.runner === "interop") && r.status !== "skipped").map(r => r.id)

test("every program row has a golden Pi interleaving, as parseable tokens", () => {
	for (const id of rows) {
		const file = join(traces, `${id}.pi.jsonl`)
		if (!existsSync(file)) { assert.ok(["E16"].includes(id), `${id} has no golden trace: run node cli/trace.mjs record ${id}`); continue }
		const events = readFileSync(file, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
		assert.ok(events.length > 0 && events.every(e => ["#", ">", "!"].includes(e.k)), `${id}: tokens`)
		// E00-E05 drive the Session kernel, not a Harness: their trace is commits only.
		if (!/^E0[0-5]$/.test(id)) assert.ok(events.some(e => e.k === ">"), `${id}: the trace has no API call (the tracer did not run)`)
	}
})

test("Pi reproduces its golden interleavings for E20, E22 and the interop row (determinism; a changed pin or tracer shows here)", () => {
	const out = mkdtempSync(join(tmpdir(), "trace-verify-"))
	try {
		const result = execFileSync(process.execPath, [join(import.meta.dirname, "..", "cli", "trace.mjs"), "verify", "E20", "E22", "I", "--out", out], { encoding: "utf8", env: { ...process.env, CONTRACT_NO_BUILD: "1" } })
		assert.match(result, /3 of 3 rows match Pi's interleaving/)
	} finally { rmSync(out, { recursive: true, force: true }) }
})

test("the reference's own Node SQLite adapter profile (CONTRACT_STORAGE_PROFILE=node) keeps the interleaving of E20 and E22 and spends other hops", () => {
	const out = mkdtempSync(join(tmpdir(), "trace-profile-"))
	try {
		const result = execFileSync(process.execPath, [join(import.meta.dirname, "..", "cli", "trace.mjs"), "verify", "E20", "E22", "--out", out], { encoding: "utf8", env: { ...process.env, CONTRACT_NO_BUILD: "1", CONTRACT_STORAGE_PROFILE: "node" } })
		assert.match(result, /2 of 2 rows match Pi's interleaving/)
		const ticks = file => readFileSync(file, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l)).filter(l => l.t === "commit" && l.tick !== undefined).map(l => l.tick)
		const run = (profile) => { const dir = mkdtempSync(join(tmpdir(), "trace-ticks-")); try { execFileSync(process.execPath, [join(import.meta.dirname, "..", "cli", "contract.mjs"), "run", "E20", "--impl", "pi", "--out", dir], { env: { ...process.env, CONTRACT_NO_BUILD: "1", CONTRACT_TICKS: "1", ...(profile ? { CONTRACT_STORAGE_PROFILE: profile } : {}) } }); return ticks(join(dir, "ref", "E20.trace")) } finally { rmSync(dir, { recursive: true, force: true }) } }
		assert.notDeepEqual(run(undefined).map((t, i, a) => t - (a[i - 1] ?? 0)), run("node").map((t, i, a) => t - (a[i - 1] ?? 0)), "the node profile must change the hop counts (otherwise the shim is not in use)")
	} finally { rmSync(out, { recursive: true, force: true }) }
})
