// A U row judges the candidate's run of a file against the reference's run of the same file started with it, so both see the
// load of the same moment. A reference run made earlier, under another load, counted a load-sensitive test the reference passed
// then against a candidate that ran later under more load (native U-harness-inbox, -compaction, -lifecycle at host load ~600).
// The reference here is a stand-in tree whose test fails while a flag file exists: the flag is the load of the moment.
import assert from "node:assert/strict"
import { existsSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
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
