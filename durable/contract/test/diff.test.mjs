// The differential ordering rows (F-<profile>-<seed>, scenarios/diff-ordering.mjs): the control passes, and a candidate that
// changes an observable order, or only a stored duration, fails with the level that shows it. A negative control is a candidate
// directory (a module that re-exports the reference and changes one behaviour), so the row's own comparison is what fails.
import assert from "node:assert/strict"
import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { loadCorpus, validateCorpus } from "../lib/corpus.mjs"
import { piSource } from "../lib/run.mjs"

const work = mkdtempSync(join(tmpdir(), "contract-diff-"))
test.after(() => rmSync(work, { recursive: true, force: true }))
// The reference checkout the gate itself runs (CONTRACT_PI_SOURCE, else the cache `make durable-contract-setup` fills).
const reference = piSource()
const index = `file://${reference}/packages/durable/src/index.ts?real`
const candidates = {
	// Identical to the reference: the candidate-directory path itself must not change a verdict.
	same: `export * from "${index}"\n`,
	// An observer's listener is called two microtasks late: the lines it prints move relative to the lines other work prints (output level).
	"late-listener": `import * as real from "${index}"\nexport * from "${index}"\nexport const watchEvents = async (...args: any[]) => {\n\tconst stream = await (real.watchEvents as any)(...args)\n\treturn { ...stream, start: (listener: any) => stream.start(async (events: any, context: any) => { await Promise.resolve(); await Promise.resolve(); return listener(events, context) }) }\n}\n`,
	// One tool starts 3 virtual ms late: its stored durationMs and its place among concurrent tools change (commit level).
	"slow-tool": `import * as real from "${index}"\nexport * from "${index}"\nexport const defineTool = (tool: any) => real.defineTool({ ...tool, execute: async (...args: any[]) => { if (tool.name === "commitwork") await new Promise(r => setTimeout(r, 3)); return tool.execute(...args) } })\n`,
}
const toml = []
for (const [name, source] of Object.entries(candidates)) {
	const dir = join(work, name)
	mkdirSync(dir)
	writeFileSync(join(dir, "index.ts"), source)
	toml.push(`[[impl]]\nname = "${name}"\ncandidate_dir = "${dir}"\n`)
}
writeFileSync(join(work, "impls.toml"), toml.join("\n"))
process.env.CONTRACT_IMPLS = join(work, "impls.toml")
process.env.CONTRACT_NO_BUILD = "1"
const { diffRows, gate } = await import("../lib/gate.mjs")

const play = (impl, seed, profile = "gate") => gate({ impl, out: join(work, `${impl}-${seed}-${profile}`), concurrency: 1, rows: diffRows(seed, seed, [profile]) }).then(([row]) => row)

test("the manifest lists the differential rows for both storage profiles and rejects a row without a seed", () => {
	const corpus = loadCorpus()
	const rows = corpus.scenario.filter(r => r.runner === "diff")
	assert.deepEqual([...new Set(rows.map(r => r.profile))].sort(), ["gate", "node"])
	// Each seeded and tick row exists once per profile; F-crash (the crash program run to the end), the T1 time-travel rows (one
	// fixture each) and the extra seeds beyond 16 that found a bug (gen-corpus DIFF_EXTRA_SEEDS) are single rows on the gate profile.
	// F-fail rows pick the failing commit by argument: gate k=2..20 and node k=3, 9, 15, each a row of its own.
	const failing = r => r.source.endsWith("storage-failure.mjs")
	const single = r => r.id === "F-crash" || r.family === "timetravel" || r.source.endsWith("diff-ordering.mjs") && r.seed > 16 || failing(r)
	const paired = rows.filter(r => !single(r))
	assert.deepEqual(paired.filter(r => r.profile === "gate").map(r => r.id.replace("-gate", "")), paired.filter(r => r.profile === "node").map(r => r.id.replace("-node", "")))
	assert.deepEqual(rows.filter(r => single(r) && !failing(r)).map(r => r.profile), rows.filter(r => single(r) && !failing(r)).map(() => "gate"))
	assert.deepEqual(rows.filter(failing).map(r => `${r.profile}-${r.args[1]}`), [...Array.from({ length: 19 }, (_, i) => `gate-${i + 2}`), "node-3", "node-9", "node-15"])
	assert.deepEqual(rows.filter(r => r.family === "timetravel").map(r => r.fixture), ["pi-main-250", "pi-main-3500"])
	assert.deepEqual(validateCorpus(corpus), [])
	const broken = { scenario: [{ ...rows[0], seed: undefined }, { ...rows[1], profile: "disk" }] }
	assert.equal(validateCorpus(broken).filter(p => /names an integer seed and a storage profile/.test(p)).length, 2)
})

test("the control passes on both storage profiles, and two seeds draw different programs", async () => {
	const rows = await Promise.all([play("pi", 1), play("pi", 1, "node"), play("pi", 2)])
	for (const row of rows) assert.equal(row.status, "pass", JSON.stringify(row.detail))
	assert.match(rows[0].detail, /\d+ commits/)
	assert.notEqual(rows[0].detail, rows[2].detail, "seeds 1 and 2 stored different numbers of commits")
})

test("a row that names a fixture gets its own copy of the store as --db in every run", async () => {
	// The time-travel script forks and runs a turn on the store it opens: a shared copy would let the candidate see the reference's writes.
	const row = { ...loadCorpus().scenario.find(r => r.id === "T1-250"), fixture: "pi-main-50", args: ["--at-turn", "10", "--scrub", "3"] }
	for (const impl of ["pi", "same"]) {
		const [r] = await gate({ impl, out: join(work, `tt-${impl}`), concurrency: 1, rows: [row] })
		assert.equal(r.status, "pass", JSON.stringify(r.detail))
		assert.match(r.detail, /\d+ commits/)
	}
	const copies = readdirSync(work, { recursive: true }).filter(f => f.startsWith("tt-") && f.endsWith("store.sqlite"))
	assert.ok(copies.length >= 2, `the reference and the candidate runs each opened their own copy (${copies.join(", ")})`)
})

test("a candidate directory that re-exports the reference passes", async () => {
	for (const row of [await play("same", 3), await play("same", 6, "node")]) assert.equal(row.status, "pass", JSON.stringify(row.detail))
})

test("negative control: a listener that runs two microtasks late fails at the output level", async () => {
	for (const seed of [3, 6]) {
		const row = await play("late-listener", seed)
		assert.equal(row.status, "fail", `seed ${seed}`)
		assert.equal(row.detail[0].level, "output")
	}
})

test("negative control: a tool that starts 3 virtual ms late fails at the commit level", async () => {
	const row = await play("slow-tool", 1)
	assert.equal(row.status, "fail")
	assert.equal(row.detail[0].level, "commit")
})

test("a reduction cuts the plan and changes no other draw (what `soak --shrink` relies on)", async () => {
	const { runScenario } = await import("../lib/run.mjs")
	const { argsOf, planOf } = await import("../lib/shrink.mjs")
	const run = async (name, args) => planOf((await runScenario({ script: join(import.meta.dirname, "..", "scenarios", "diff-ordering.mjs"), args: ["--seed", "9", ...args], out: join(work, name), name, scenario: name })).trace)
	const full = await run("full", [])
	const cut = await run("cut", argsOf({ hooks: [], observers: [], arrivals: [1], single: false, rounds: 0, calls: undefined, streaming: false }))
	assert.equal(full.arrivals.length, 3)
	assert.deepEqual(cut.arrivals.map(a => a.id), [full.arrivals[1].id])
	assert.deepEqual(cut.arrivals[0].rounds, [])
	assert.equal(cut.streaming, false)
	assert.deepEqual(cut.settings, full.settings)
	assert.deepEqual(cut.inputs[0].start, full.inputs[0].start)
	assert.deepEqual(cut.arrivals[0].at, full.arrivals[1].at)
	assert.ok(Object.values(cut.hooks).every(h => h === undefined))
})
