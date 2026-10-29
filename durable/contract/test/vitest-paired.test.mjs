// A U row judges the candidate's run of a file against the reference's run of the same file started with it, so both see the
// load of the same moment. A reference run made earlier, under another load, counted a load-sensitive test the reference passed
// then against a candidate that ran later under more load (native U-harness-inbox, -compaction, -lifecycle at host load ~600).
// The reference here is a stand-in tree whose test fails while a flag file exists: the flag is the load of the moment.
import assert from "node:assert/strict"
import { existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { piSource } from "../lib/run.mjs"

const real = (() => { try { return piSource() } catch { return undefined } })()
const skip = !real && "no reference checkout (CONTRACT_PI_SOURCE or the contract cache)"

const work = mkdtempSync(join(tmpdir(), "contract-paired-"))
test.after(() => rmSync(work, { recursive: true, force: true }))
const flag = join(work, "loaded")
const ref = join(work, "pi")
const durable = join(ref, "packages", "durable")
if (real) {
	mkdirSync(join(durable, "src"), { recursive: true })
	mkdirSync(join(durable, "test"), { recursive: true })
	symlinkSync(join(real, "node_modules"), join(ref, "node_modules"))
	writeFileSync(join(durable, "src", "index.ts"), "export const answer = 42\n")
	for (const name of ["first", "second"]) {
		writeFileSync(join(durable, "test", `${name}.test.ts`), [
			`import { existsSync } from "node:fs"`,
			`import { expect, it } from "vitest"`,
			`import { answer } from "../src/index.ts"`,
			`it("answers", () => { expect(answer).toBe(42) })`,
			`it("finishes in time", () => { expect(existsSync(${JSON.stringify(flag)})).toBe(false) })`,
			"",
		].join("\n"))
	}
}
const candidate = join(work, "candidate")
mkdirSync(candidate, { recursive: true })
const wrong = join(work, "wrong")
mkdirSync(wrong, { recursive: true })
writeFileSync(join(wrong, "index.ts"), "export const answer = 41\n")

const row = name => ({ source: `packages/durable/test/${name}.test.ts` })
let gate
test.before(async () => {
	process.env.CONTRACT_PI_SOURCE = ref
	gate = await import("../lib/gate.mjs")
})

test("a test the reference fails under the same load is not counted against the candidate, and the row names it", { skip }, async () => {
	const impl = { name: "same", vitest: true, candidateDir: candidate, env: {} }
	const ctx = { out: join(work, "out-same") }
	rmSync(flag, { force: true })
	const quiet = await gate.runVitestRow(row("first"), impl, ctx)
	assert.equal(quiet.status, "pass", JSON.stringify(quiet))
	assert.equal(quiet.detail, "2 tests")
	// The load changes between the first row and the second: the second row's reference run must see it too.
	writeFileSync(flag, "")
	try {
		const busy = await gate.runVitestRow(row("second"), impl, ctx)
		assert.equal(busy.status, "pass", JSON.stringify(busy))
		assert.equal(busy.detail, "1 tests; the reference failed 1 alongside: finishes in time")
	} finally {
		rmSync(flag, { force: true })
	}
})

test("a test only the candidate fails still fails the row", { skip }, async () => {
	assert.ok(!existsSync(flag))
	const r = await gate.runVitestRow(row("second"), { name: "wrong", vitest: true, candidateDir: wrong, env: {} }, { out: join(work, "out-wrong") })
	assert.equal(r.status, "fail", JSON.stringify(r))
	assert.deepEqual(r.detail.first, ["answers: failed"])
})

// A numbered divergence whose measured value the row states (corpus measured_test, measured_failure, measured_divergence): the
// candidate must fail that test with the stated failure. It is no excuse: a pass, or another failure, fails the row.
const measured = { name: "wrong", vitest: true, candidateDir: wrong, env: {} }
const measuredRow = (failure, test = "answers") => ({ ...row("second"), measured_test: test, measured_failure: failure, measured_divergence: "D206" })

test("a test the row states as a measured divergence passes the row when the candidate fails it with the stated failure", { skip }, async () => {
	const r = await gate.runVitestRow(measuredRow("expected 41 to be 42"), measured, { out: join(work, "out-measured-ok") })
	assert.equal(r.status, "pass", JSON.stringify(r))
	assert.match(r.detail, /D206: answers fails with "expected 41 to be 42"/)
})

test("a measured divergence fails the row when the candidate fails the test with another failure", { skip }, async () => {
	const r = await gate.runVitestRow(measuredRow("expected 40 to be 42"), measured, { out: join(work, "out-measured-other") })
	assert.equal(r.status, "fail", JSON.stringify(r))
	assert.deepEqual(r.detail.first, ["answers: failed"])
})

test("a measured divergence fails the row when the candidate passes the test, so the entry cannot outlive the divergence", { skip }, async () => {
	const r = await gate.runVitestRow(measuredRow("expected 41 to be 42"), { name: "right", vitest: true, candidateDir: candidate, env: {} }, { out: join(work, "out-measured-pass") })
	assert.equal(r.status, "fail", JSON.stringify(r))
	assert.match(r.detail.problem, /D206 .* passes answers/)
})

// E17 and E18 run `ls` in the reference's packages/durable. Vitest's cache (vite's cacheDir, by default packages/durable/node_modules/.vite)
// made the first U row create the entry `node_modules` there, so the reference's capture, taken before it, and a candidate's, taken after
// (`contract all` captures the reference once, in its first pass), listed different directories: both rows failed on every fresh clone.
test("a run of a U row leaves the reference checkout as it found it: vitest's cache lives in the run's output", { skip }, async () => {
	const clean = join(work, "clean")
	const cleanDurable = join(clean, "packages", "durable")
	mkdirSync(join(cleanDurable, "src"), { recursive: true })
	mkdirSync(join(cleanDurable, "test"), { recursive: true })
	symlinkSync(join(real, "node_modules"), join(clean, "node_modules"))
	writeFileSync(join(cleanDurable, "src", "index.ts"), "export const answer = 42\n")
	writeFileSync(join(cleanDurable, "test", "listing.test.ts"), ['import { expect, it } from "vitest"', 'import { answer } from "../src/index.ts"', 'it("answers", () => { expect(answer).toBe(42) })', ""].join("\n"))
	const before = readdirSync(cleanDurable, { recursive: true }).sort()
	const was = process.env.CONTRACT_PI_SOURCE
	process.env.CONTRACT_PI_SOURCE = clean
	try {
		const out = join(work, "out-clean")
		const r = await gate.runVitestRow(row("listing"), { name: "clean", vitest: true, candidateDir: candidate, env: {} }, { out })
		assert.equal(r.status, "pass", JSON.stringify(r))
		assert.deepEqual(readdirSync(cleanDurable, { recursive: true }).sort(), before)
		assert.ok(readdirSync(join(out, "clean")).some(f => f.startsWith("vitest-cache-")), "the cache went to the run's output directory")
	} finally {
		if (was === undefined) delete process.env.CONTRACT_PI_SOURCE
		else process.env.CONTRACT_PI_SOURCE = was
	}
})
