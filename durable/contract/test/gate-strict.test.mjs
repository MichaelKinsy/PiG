// The gate never reports a verdict it did not earn: the order level catches a candidate whose commits match but whose calls
// interleave with them differently (CONTRACT 5.8), a reference that does not run is a failed row, a failed build stops the run,
// a pending row is pending, and the manifest cannot carry a relaxed comparison.
import assert from "node:assert/strict"
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, relative } from "node:path"
import { pathToFileURL } from "node:url"
import test from "node:test"
import { REPO, validateCorpus } from "../lib/corpus.mjs"
import { piSource } from "../lib/run.mjs"

const work = mkdtempSync(join(tmpdir(), "contract-strict-"))
test.after(() => rmSync(work, { recursive: true, force: true }))
const impls = join(work, "impls.toml")
writeFileSync(impls, `[[impl]]\nname = "unbuildable"\nnative_exe = "${join(import.meta.dirname, "gate-strict.test.mjs")}"\nbuild = "false"\n`)
process.env.CONTRACT_IMPLS = impls
const { ensureBuilt, runProgramRow } = await import("../lib/gate.mjs")

/** The reference's sources as a candidate directory, its MemoryStorage the gate's capsql one, with `edit` applied to one file. */
function mirror(name, edit) {
	const dir = join(work, name)
	cpSync(join(piSource(), "packages/durable/src"), dir, { recursive: true })
	writeFileSync(join(dir, "storage/memory.ts"), `import { MemoryStorage as storage } from ${JSON.stringify(pathToFileURL(join(import.meta.dirname, "..", "impls", "storage.mjs")).href)};\nexport const MemoryStorage = storage();\n`)
	if (edit) {
		const file = join(dir, edit[0])
		const text = readFileSync(file, "utf8")
		assert.ok(text.includes(edit[1]), `${edit[1]} is not in ${edit[0]}`)
		writeFileSync(file, text.replace(edit[1], edit[2]))
	}
	return { name, examples: true, candidateDir: dir, env: {} }
}
const e14 = { id: "E14", runner: "example", source: "packages/durable/test/examples/14-chat.ts", levels: ["store", "commit", "context", "output", "order"] }

test("order level: the reference's own sources pass every level of E14", async () => {
	const r = await runProgramRow(e14, mirror("mirror"), { out: join(work, "out-mirror") })
	assert.equal(r.status, "pass", JSON.stringify(r.detail).slice(0, 600))
})

test("negative control, order level: a submit that returns later than Pi's passes every other level and fails the order", async () => {
	const slow = mirror("slow", ["harness/submissions.ts", "\t\treturn new SubmissionHandle(id, this);", "\t\tfor (let i = 0; i < 200; i++) await Promise.resolve();\n\t\treturn new SubmissionHandle(id, this);"])
	const r = await runProgramRow(e14, slow, { out: join(work, "out-slow") })
	assert.equal(r.status, "fail")
	assert.equal(r.detail[0].level, "order", JSON.stringify(r.detail).slice(0, 600))
	assert.match(r.detail[0].expected[0], /^> harness\.root\(\)\.submit\(\)\.wait/)
	// Without the order level the same candidate passes: that was the gate before the level existed.
	const relaxed = await runProgramRow({ ...e14, id: "E14", levels: e14.levels.filter(l => l !== "order") }, slow, { out: join(work, "out-slow-relaxed") })
	assert.equal(relaxed.status, "pass", JSON.stringify(relaxed.detail).slice(0, 600))
})

test("a reference that does not run is a failed row, not a skipped one", async () => {
	const script = join(work, "broken.mjs")
	writeFileSync(script, "throw new Error('the reference broke')\n")
	const r = await runProgramRow({ id: "Xbroken", runner: "interop", source: relative(REPO, script), levels: ["store"] }, { name: "pi", control: true }, { out: join(work, "out-broken") })
	assert.equal(r.status, "fail")
	assert.match(r.detail.problem, /the reference scenario did not run/)
})

test("a failed build stops the run instead of running the rows on an earlier build", async () => {
	delete process.env.CONTRACT_NO_BUILD
	await assert.rejects(ensureBuilt("unbuildable"), /build `false` failed/)
})

test("the manifest cannot excuse a row: divergence fields and an order level outside a program row are problems", () => {
	const row = { id: "E20", owner: "dcore-session", runner: "example" }
	for (const key of ["divergence", "divergence_levels", "divergence_tests", "not_core_tests"]) {
		assert.match(validateCorpus({ scenario: [{ ...row, [key]: key === "divergence" ? "D106" : ["store"] }] }).join(), new RegExp(`${key} relaxes`))
	}
	assert.match(validateCorpus({ scenario: [{ id: "W1-50", owner: "dcore-loop", runner: "bench", levels: ["order"] }] }).join(), /order level needs a scenario program/)
	assert.deepEqual(validateCorpus({ scenario: [{ ...row, levels: ["store", "order"] }] }), [])
})

test("a row nobody can run is reported skipped with its reason (a strict run fails on it), never run against the reference's own modules", async () => {
	const { gate } = await import("../lib/gate.mjs")
	const [r] = await gate({ impl: "pi", ids: ["U-memory-storage"], out: join(work, "out-pending") })
	assert.equal(r.status, "skipped")
	assert.match(r.detail, /runs against the core as row SC/)
	assert.match(r.detail, /own internals/)
})

// A row that waits for the owner's API keys (E16, W4f) is reported pending with "pending-owner" and does not fail a strict run: no other
// lane can run it. Any other pending row still does.
test("pending-owner rows do not fail --strict; other pending rows do", async () => {
	const { gate } = await import("../lib/gate.mjs")
	const results = await gate({ impl: "pi", ids: ["E16", "W4f"], out: join(work, "out-owner") })
	assert.deepEqual(results.map(r => [r.id, r.status]).sort(), [["E16", "pending"], ["W4f", "pending"]])
	for (const r of results) assert.match(r.detail, /^pending-owner: /)
	const { blocking } = await import("../lib/gate.mjs")
	assert.equal(results.some(blocking), false)
	assert.equal(blocking({ status: "pending", detail: "no runner for matrix rows yet" }), true)
	assert.equal(blocking({ status: "pass", detail: "" }), false)
})
