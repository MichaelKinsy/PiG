// A vitest row may name tests it skips (corpus `skip_tests`, with the row's `reason`): the owner's written exception for tests the
// candidate cannot run yet (0.8.0: the two Pi #11892 tests of harness-tasks.test.ts, whose hook sits in a monkeypatched session.touch).
// The row passes only when every other test passes and the named tests are the only failures. A named test the reference no longer
// has, or one the candidate now passes, fails the row, so the exception cannot go stale unseen.
import assert from "node:assert/strict"
import test from "node:test"
import { validateCorpus } from "../lib/corpus.mjs"
import { runVitestRow } from "../lib/gate.mjs"

const REF = ["kept one", "kept two", "held one", "held two"]
// Each side's tests: name -> status. `ref` is the reference's run, `cand` the candidate's.
const run = (ref, cand) => async impl => {
	const tests = impl.name === "ref" ? ref : cand
	return Object.assign(new Map([["skip-tests.test.ts", { status: "passed", tests: new Map(Object.entries(tests)), failures: new Map() }]]), { timedOut: false, guard: [] })
}
const all = status => Object.fromEntries(REF.map(n => [n, status]))
let n = 0
// The gate memoizes a file's runs per implementation, so each case is a file of its own.
const call = (extra, ref, cand, impl = { name: "cand-skip", vitest: true, candidateDir: "x", env: {} }) => {
	const id = `skip-tests-${n++}`
	const stub = (i, c, include) => run(ref, cand)(i, c, include).then(m => new Map([[`${id}.test.ts`, m.get("skip-tests.test.ts")]]))
	return runVitestRow({ id: `U-${id}`, source: `packages/durable/test/${id}.test.ts`, reason: "owner decision A", skip_tests: ["held one", "held two"], ...extra }, impl, { out: "x", run: Object.assign(stub, {}) }, { scenario: [] })
}

test("skip_tests: the named tests failing are skipped, every other test passing passes the row and the output names the skipped tests", async () => {
	const result = await call({}, all("passed"), { ...all("passed"), "held one": "failed", "held two": "failed" })
	assert.equal(result.status, "pass", JSON.stringify(result))
	assert.match(result.detail, /2 tests/)
	assert.match(result.detail, /skipped by name \(owner decision A\): held one; held two/)
})

test("skip_tests: a failure outside the named tests still fails the row", async () => {
	const result = await call({}, all("passed"), { ...all("passed"), "held one": "failed", "held two": "failed", "kept two": "failed" })
	assert.equal(result.status, "fail", JSON.stringify(result))
	assert.match(JSON.stringify(result), /kept two/)
	assert.doesNotMatch(JSON.stringify(result.detail.first ?? []), /held/)
})

test("skip_tests: a named test the candidate now passes fails the row (the skip went stale)", async () => {
	const result = await call({}, all("passed"), { ...all("passed"), "held two": "failed" })
	assert.equal(result.status, "fail", JSON.stringify(result))
	assert.match(JSON.stringify(result), /held one.*now passes|stale/)
})

test("skip_tests: a named test the reference no longer has fails the row", async () => {
	const ref = { ...all("passed") }
	delete ref["held two"]
	const result = await call({}, ref, { ...ref, "held one": "failed" })
	assert.equal(result.status, "fail", JSON.stringify(result))
	assert.match(JSON.stringify(result), /held two.*(not|no longer)/)
})

test("skip_tests: the control (the reference as the candidate) passes with the named tests passing", async () => {
	const result = await call({}, all("passed"), all("passed"), { name: "pi", control: true })
	assert.equal(result.status, "pass", JSON.stringify(result))
})

test("corpus: skip_tests is a list of test names on a vitest row that carries a reason", () => {
	const base = { id: "U-x", family: "upstream-test", runner: "vitest", owner: "dcore-session", status: "ready" }
	const problems = r => validateCorpus({ scenario: [{ ...base, ...r }] })
	assert.deepEqual(problems({ skip_tests: ["a test"], reason: "why" }), [])
	assert.match(problems({ skip_tests: ["a test"] }).join("\n"), /skip_tests needs a reason/)
	assert.match(problems({ skip_tests: [], reason: "why" }).join("\n"), /skip_tests/)
	assert.match(problems({ skip_tests: ["a test"], reason: "why", runner: "diff", seed: 1, profile: "gate" }).join("\n"), /vitest/)
})
