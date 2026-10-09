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
