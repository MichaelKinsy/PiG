// The known-failure baseline (baseline.toml): listed differential rows stay red but do not fail the gate; any other failure does; a listed row
// that passes on every build fails the run until its entry is deleted; nothing but a differential row may be listed.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { describe, judge, loadBaseline, stale, validateBaseline } from "../lib/baseline.mjs"
import { loadCorpus } from "../lib/corpus.mjs"
import { piSource } from "../lib/run.mjs"

const work = mkdtempSync(join(tmpdir(), "contract-baseline-"))
test.after(() => rmSync(work, { recursive: true, force: true }))
const entry = id => ({ id, owner: "o", cause: "c", repro: "r" })
const row = (id, status) => ({ id, status })

test("the committed baseline lists only differential rows of the manifest, each with owner, cause and repro, and no row twice", () => {
	const known = loadBaseline()
	assert.deepEqual(validateBaseline(known, loadCorpus()), [])
})

test("validation rejects a row that is not a differential row, a duplicate and an entry without a repro", () => {
	const problems = validateBaseline([entry("E00"), entry("F-gate-1"), entry("F-gate-1"), { ...entry("F-gate-2"), repro: "" }, entry("F-gate-999")], loadCorpus())
	assert.equal(problems.filter(p => /only differential rows/.test(p)).length, 2)
	assert.ok(problems.some(p => /F-gate-1: listed twice/.test(p)))
	assert.ok(problems.some(p => /F-gate-2: no repro/.test(p)))
})

test("judge splits known, fresh and passing; the control has no baseline", () => {
	const known = [entry("F-gate-1"), entry("F-gate-2")]
	const results = [row("F-gate-1", "fail"), row("F-gate-2", "pass"), row("F-gate-3", "fail"), row("E00", "fail"), row("E01", "pass")]
	const j = judge("native", results, known)
	assert.deepEqual(j.known.map(r => r.id), ["F-gate-1"])
	assert.deepEqual(j.fresh.map(r => r.id), ["F-gate-3", "E00"])
	assert.deepEqual(j.passing.map(r => r.id), ["F-gate-2"])
	assert.deepEqual(judge("pi", results, known).fresh.map(r => r.id), ["F-gate-1", "F-gate-3", "E00"])
	assert.match(describe("native", j).join("\n"), /known failures \(baseline.toml\) on native: F-gate-1[\s\S]*NEW FAILURES on native \(not in baseline.toml\): F-gate-3 E00/)
})

test("an entry is stale only when it passes on every build that ran it", () => {
	const known = [entry("F-gate-1"), entry("F-gate-2")]
	const a = judge("native", [row("F-gate-1", "pass"), row("F-gate-2", "pass")], known)
	const b = judge("wasm-tinygo", [row("F-gate-1", "pass"), row("F-gate-2", "fail")], known)
	assert.deepEqual(stale({ pi: judge("pi", [], known), native: a, "wasm-tinygo": b }), ["F-gate-1"])
	assert.deepEqual(stale({ pi: judge("pi", [], known) }), [])
})

// The command line: a candidate directory that delays an observer fails seed 3; the identity candidate passes it.
// The reference checkout the gate itself runs (CONTRACT_PI_SOURCE, else the cache `make durable-contract-setup` fills).
const reference = piSource()
const index = `file://${reference}/packages/durable/src/index.ts?real`
const dirs = {
	same: `export * from "${index}"\n`,
	late: `import * as real from "${index}"\nexport * from "${index}"\nexport const watchEvents = async (...args: any[]) => {\n\tconst stream = await (real.watchEvents as any)(...args)\n\treturn { ...stream, start: (listener: any) => stream.start(async (events: any, context: any) => { await Promise.resolve(); await Promise.resolve(); return listener(events, context) }) }\n}\n`,
}
const toml = []
for (const [name, source] of Object.entries(dirs)) { mkdirSync(join(work, name)); writeFileSync(join(work, name, "index.ts"), source); toml.push(`[[impl]]\nname = "${name}"\ncandidate_dir = "${join(work, name)}"\n`) }
writeFileSync(join(work, "impls.toml"), toml.join("\n"))
const cli = join(import.meta.dirname, "..", "cli", "contract.mjs")
const run = (args, baselineIds) => {
	writeFileSync(join(work, "baseline.toml"), baselineIds.map(id => `[[known]]\nid = "${id}"\nowner = "o"\ncause = "c"\nrepro = "r"\n`).join("\n"))
	try { return { code: 0, out: execFileSync(process.execPath, [cli, ...args], { encoding: "utf8", env: { ...process.env, CONTRACT_IMPLS: join(work, "impls.toml"), CONTRACT_BASELINE: join(work, "baseline.toml"), CONTRACT_NO_BUILD: "1" } }) } } catch (e) { return { code: e.status, out: String(e.stdout) } }
}

test("a failing row the baseline lists leaves the exit status 0 and stays FAIL in the report", () => {
	const r = run(["run", "F-gate-3", "--impl", "late", "--out", join(work, "o1")], ["F-gate-3"])
	assert.equal(r.code, 0, r.out)
	assert.match(r.out, /^FAIL +F-gate-3/m)
	assert.match(r.out, /known failures \(baseline.toml\) on late: F-gate-3/)
})

test("a failing row the baseline does not list fails the run", () => {
	const r = run(["run", "F-gate-3", "--impl", "late", "--out", join(work, "o2")], [])
	assert.equal(r.code, 1)
	assert.match(r.out, /NEW FAILURES on late \(not in baseline.toml\): F-gate-3/)
})

test("a listed row that passes on every build fails `all` until the entry is deleted", () => {
	const r = run(["all", "--impls", "pi,same", "--no-diagnose", "--out", join(work, "o3"), "F-gate-3"], ["F-gate-3"])
	assert.equal(r.code, 1, r.out)
	assert.match(r.out, /FIXED: F-gate-3 pass on every build; delete them from baseline.toml/)
	assert.equal(run(["all", "--impls", "pi,same", "--no-diagnose", "--out", join(work, "o4"), "F-gate-3"], []).code, 0)
})
