// The crash handoff matrix with a real second implementation (the vendored TypeScript control): it passes where it is faithful,
// and each negative control, a deliberately broken copy of that control, fails at the level it should (CONTRACT 5.5).
import assert from "node:assert/strict"
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import test from "node:test"
import { IMPLS, registerImpl } from "../lib/impl.mjs"
import { crashPoints, matrix, sample } from "../lib/matrix.mjs"

const fixture = join(process.env.DURABLE_CONTRACT_CACHE ?? join(import.meta.dirname, "..", ".cache"), "fixtures", "pi-main-50.sqlite")
const work = mkdtempSync(join(tmpdir(), "contract-matrix-"))
test.after(() => rmSync(work, { recursive: true, force: true }))
const scenario = { turns: 1, tools: 8, variant: "full", scenario: "W1-50" }
const core = join(import.meta.dirname, "..", "impls", "ts-core")

/** A copy of the control with one source line changed; the change must apply, or the control tests nothing. */
function mutant(name, file, from, to) {
	const dir = join(work, `mutant-${name}`)
	cpSync(core, dir, { recursive: true })
	const text = readFileSync(join(dir, file), "utf8")
	assert.ok(text.includes(from), `mutation ${name}: ${JSON.stringify(from)} is not in ${file}`)
	writeFileSync(join(dir, file), text.replace(from, to))
	// The copy lives elsewhere: its workload import must point back at the contract's.
	const plan = readFileSync(join(dir, "plan.ts"), "utf8").replaceAll("../../lib/workload.mjs", pathToFileURL(join(import.meta.dirname, "..", "lib", "workload.mjs")).href)
	writeFileSync(join(dir, "plan.ts"), plan)
	registerImpl(`ts-${name}`, { ...IMPLS.ts, runBench: o => IMPLS.ts.runBench({ ...o, env: { CONTRACT_TS_CORE_DIR: dir } }) })
	return `ts-${name}`
}

test("the fixture exists (run `contract fixtures 50`)", () => assert.ok(existsSync(fixture)))

test("crash points are the measured turn's commits, in seq order, and the first is the fixture's next_seq", async () => {
	const points = await crashPoints(fixture, scenario, { work: join(work, "points") })
	assert.equal(points.length, 77)
	assert.deepEqual(points, [...points].sort((a, b) => a - b))
	assert.equal(points[0], 558)
})

test("the TypeScript control is handoff-equal to pi-durable in every order at sampled crash points and the clean restart", async () => {
	const points = sample(await crashPoints(fixture, scenario, { work: join(work, "points") }), 6)
	const r = await matrix({ fixture, scenario, candidate: "ts", work: join(work, "ts"), points, concurrency: 8 })
	assert.equal(r.rows.length, (points.length + 1) * 3)
	assert.deepEqual(r.rows.filter(x => !x.ok), [])
	assert.ok(r.rows.some(x => x.k === "end"))
})

const controls = [
	// name, file, from, to, what the matrix's report must name
	["key-order", "core.ts", '"kind":"pi.user","id":${t.user}', '"id":${t.user},"kind":"pi.user"', /"kind":"key-order"/],
	["no-endedAt", "core.ts", 'if (o.endedAt === undefined && status === "terminal") o.endedAt = this.now', "", /"path":"\$\.endedAt"/],
	["usage", "core.ts", '"totalTokens":${u.total}', '"totalTokens":${u.total + 1}', /"path":"\$\.model\[0\]\.usage\.totalTokens"/],
	// Recovery that does not requeue a running task writes one commit fewer: the store ends at a smaller next_seq.
	["no-reset", "core.ts", 'const reset = (r: Rec) => setStatus(r, "pending")', "const reset = (_r: Rec) => {}", /"table":"durable_metadata"/],
]
for (const [name, file, from, to, names] of controls) {
	test(`negative control ${name}: the matrix fails and its report names the cause`, async () => {
		const candidate = mutant(name, file, from, to)
		const points = sample(await crashPoints(fixture, scenario, { work: join(work, "points") }), 6)
		const r = await matrix({ fixture, scenario, candidate, work: join(work, candidate), points, orders: ["P>X", "X>X"], concurrency: 8 })
		const bad = r.rows.filter(x => !x.ok)
		assert.ok(bad.length > 0, `${name} passed the matrix`)
		// A failure of the run itself (a crash of the candidate) is not evidence the matrix compared anything.
		const uncompared = bad.find(x => !x.report)
		assert.equal(uncompared, undefined, `a row failed without a comparison: ${JSON.stringify(uncompared)?.slice(0, 500)}`)
		assert.ok(bad.some(x => names.test(JSON.stringify(x.report))), `no report names the cause; first: ${JSON.stringify(bad[0]).slice(0, 600)}`)
	})
}

// ---- the native host: the production Go core packages (store, rec, history) behind a SQLite host, killed and recovered.

const repo = join(import.meta.dirname, "..", "..", "..")
const hostSrc = join(repo, "durable", "core", "contracttest", "cmd", "benchhost")

/** Build the native bench host, optionally with one source line of turn.go changed (go build -overlay). */
async function buildHost(name, change) {
	const { execFileSync } = await import("node:child_process")
	const { runNative } = await import("../impls/native.mjs")
	const exe = join(work, `benchhost-${name}`)
	const args = ["build", "-o", exe]
	if (change) {
		const text = readFileSync(join(hostSrc, "turn.go"), "utf8")
		assert.ok(text.includes(change[0]), `mutation ${name}: ${JSON.stringify(change[0])} is not in turn.go`)
		const mutated = join(work, `turn-${name}.go`)
		writeFileSync(mutated, text.replace(change[0], change[1]))
		const overlay = join(work, `overlay-${name}.json`)
		writeFileSync(overlay, JSON.stringify({ Replace: { [join(hostSrc, "turn.go")]: mutated } }))
		args.push(`-overlay=${overlay}`)
	}
	args.push("./durable/core/contracttest/cmd/benchhost")
	execFileSync("go", args, { cwd: repo, env: { ...process.env, GOWORK: "" } })
	const impl = `native-${name}`
	registerImpl(impl, { label: impl, contexts: ["bench"], slice: "bench", runBench: o => runNative({ exe, o: { ...o, implLabel: impl } }) })
	return impl
}

// 564 and 573 are the commits after which the tool may have run: recovery writes the interrupted result.
test("the native host is handoff-equal to pi-durable in every order, at sampled crash points, the interrupted-tool points and the clean restart", async () => {
	const impl = await buildHost("ok")
	const all = await crashPoints(fixture, scenario, { work: join(work, "points") })
	const points = [...new Set([...sample(all, 6), 564, 573])].sort((a, b) => a - b)
	const r = await matrix({ fixture, scenario, candidate: impl, work: join(work, "native"), points, concurrency: 8 })
	assert.equal(r.rows.length, (points.length + 1) * 3)
	assert.deepEqual(r.rows.filter(x => !x.ok), [])
})

test("negative control, native host: a duration on the recovery's interrupted result fails the matrix at the entry it differs in", async () => {
	const impl = await buildHost("duration", ['if isError {\n\t\tduration = ""', 'if false {\n\t\tduration = ""'])
	const r = await matrix({ fixture, scenario, candidate: impl, work: join(work, "native-duration"), points: [564], concurrency: 8 })
	const bad = r.rows.filter(x => !x.ok)
	assert.ok(bad.length > 0, "the broken host passed")
	assert.match(JSON.stringify(bad[0].report), /durationMs/)
})

test("a candidate whose run fails is a failed row carrying its stderr, never a gate error", async () => {
	registerImpl("broken", { label: "broken", contexts: ["bench"], runBench: async o => ({ code: 1, signal: null, stderr: "boom: Harness.open needs a core", stores: [], trace: join(o.out, "none.trace"), stdout: "" }) })
	const r = await matrix({ fixture, scenario, candidate: "broken", work: join(work, "broken"), points: [560], concurrency: 4 })
	assert.equal(r.rows.length, 4 * 0 + 3 * 2)
	assert.ok(r.rows.every(x => !x.ok))
	assert.ok(r.rows.some(x => /boom: Harness\.open needs a core/.test(x.problem ?? "")))
	// injection on a candidate's missing store (X1-live) is the same failure, not a TypeError
	const x = await matrix({ fixture, scenario, candidate: "broken", work: join(work, "broken-inject"), points: [560], concurrency: 4, injectAtCrash: ["task"] })
	assert.ok(x.rows.every(row => !row.ok && typeof row.problem === "string"))
})

// The store at the crash is compared on its own: a candidate whose crashed store differs only in rows the recovery replaces would
// otherwise reach the control's final store. This candidate is pi-durable that leaves a stray old pi.live revision behind its
// crash; pi-durable's recovery writes a pi.live base, which deletes every older revision of the document, so only the at-crash
// comparison can see it.
test("negative control: a crashed store that differs only in rows the recovery deletes fails the matrix at the crash", async () => {
	const { DatabaseSync } = await import("node:sqlite")
	registerImpl("pi-stray-live", { label: "pi-stray-live", runBench: async o => {
		const r = await IMPLS.pi.runBench({ ...o, implLabel: "pi-stray-live" })
		if (o.crashAfter !== undefined && r.stores[0]) {
			const db = new DatabaseSync(r.stores[0])
			const live = db.prepare(`SELECT id FROM documents WHERE kind = '"pi.live"'`).get().id
			db.prepare("INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, 2, 'delta', 1, '[]')").run(live)
			db.close()
		}
		return r
	} })
	const r = await matrix({ fixture, scenario, candidate: "pi-stray-live", work: join(work, "stray-live"), points: [558], orders: ["X>P"], concurrency: 2 })
	const bad = r.rows.filter(x => !x.ok)
	assert.equal(bad.length, 1, JSON.stringify(r.rows).slice(0, 600))
	assert.equal(bad[0].phase, "at crash")
	assert.match(JSON.stringify(bad[0].report), /document_revisions/)
})
