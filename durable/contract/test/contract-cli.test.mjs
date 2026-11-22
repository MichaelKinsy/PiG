// CLI argument parsing and zero-row guard tests for durable/contract/cli/contract.mjs.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"

const work = mkdtempSync(join(tmpdir(), "contract-cli-"))
test.after(() => rmSync(work, { recursive: true, force: true }))

const cli = join(import.meta.dirname, "..", "cli", "contract.mjs")
const run = (args, extraEnv = {}) => {
	try {
		const out = execFileSync(process.execPath, [cli, ...args], {
			encoding: "utf8",
			stdio: ["ignore", "pipe", "pipe"],
			env: { ...process.env, CONTRACT_NO_BUILD: "1", ...extraEnv },
		})
		return { code: 0, stdout: out, stderr: "" }
	} catch (e) {
		return { code: e.status ?? 1, stdout: String(e.stdout ?? ""), stderr: String(e.stderr ?? "") }
	}
}

test("the CI command line (--impls pi -j 4) does not exit 0 with zero rows", () => {
	// Scope to the two-row dcore-history lane? Even cheaper: run with --lane do-core-design (a valid lane with 0 rows)
	// would fail for 0 rows; instead run with --impls pi -j 4 and one fast real row E00 (or check --impls pi -j 4 with E15/U-memory-storage).
	// Wait: with --impls pi -j 4 and NO ids, on buggy code `-j` and `4` become ids and match 0 rows (rc 0, pass 0 fail 0 pending 0 skipped 0).
	// Pass E00 with --impls pi -j 4: on buggy code `-j` and `4` are left in rest alongside E00, which are unknown option/id tokens.
	// Also pass --impls pi -j 4 with --lane do-core-design: on buggy code it exits 0 with `pass 0  fail 0  pending 0  skipped 0`!
	const zero = run(["all", "--impls", "pi", "-j", "4", "--lane", "do-core-design", "--out", join(work, "ci-zero")])
	assert.notEqual(zero.code, 0, `CI args with 0 matching rows must not exit 0:\n${zero.stdout}`)

	// And the CI spelling (`--impls pi -j 4`) with a real corpus row (U-memory-storage, skipped; E00, real) must accept -j 4 cleanly and count the row.
	const one = run(["all", "--impls", "pi", "-j", "4", "--out", join(work, "ci-one"), "E00"])
	assert.equal(one.code, 0, `expected rc 0 with --impls pi -j 4 E00; got rc=${one.code}\nstdout:\n${one.stdout}\nstderr:\n${one.stderr}`)
	assert.match(one.stdout, /totals pi: pass 1  fail 0  pending 0  skipped 0/, `expected E00 counted:\n${one.stdout}`)
})

test("contract all with unknown scenario id exits non-zero and names the id", () => {
	const r = run(["all", "--impls", "pi", "-j", "4", "--out", join(work, "bad-id-all"), "NO-SUCH-SCENARIO"])
	assert.notEqual(r.code, 0, `expected non-zero exit for unknown id; stdout:\n${r.stdout}`)
	assert.match(r.stdout + r.stderr, /NO-SUCH-SCENARIO/)
})

test("contract run with unknown scenario id exits non-zero and names the id", () => {
	const r = run(["run", "--impl", "pi", "-j", "4", "--out", join(work, "bad-id-run"), "NO-SUCH-SCENARIO"])
	assert.notEqual(r.code, 0, `expected non-zero exit for unknown id; stdout:\n${r.stdout}`)
	assert.match(r.stdout + r.stderr, /NO-SUCH-SCENARIO/)
})

test("contract all with an unknown option exits non-zero and names it", () => {
	const r = run(["all", "--impls", "pi", "--bogus-flag", "4", "--out", join(work, "bad-opt-all")])
	assert.notEqual(r.code, 0, `expected non-zero exit for unknown option; stdout:\n${r.stdout}`)
	assert.match(r.stdout + r.stderr, /--bogus-flag/)
})

test("contract run with an unknown option exits non-zero and names it", () => {
	const r = run(["run", "--impl", "pi", "--bogus-flag", "--out", join(work, "bad-opt-run")])
	assert.notEqual(r.code, 0, `expected non-zero exit for unknown option; stdout:\n${r.stdout}`)
	assert.match(r.stdout + r.stderr, /--bogus-flag/)
})

test("contract all with zero matching rows (valid id excluded by --lane) exits non-zero and says so", () => {
	// U-memory-storage is owned by dcore-host; filtering by --lane dcore-store matches zero rows.
	const r = run(["all", "--impls", "pi", "--lane", "dcore-store", "--out", join(work, "zero-all"), "U-memory-storage"])
	assert.notEqual(r.code, 0, `expected non-zero exit when zero rows match; stdout:\n${r.stdout}`)
	assert.match(r.stdout + r.stderr, /zero|no .*row|matched no/i)
})

test("contract run with zero matching rows (valid id excluded by --lane) exits non-zero and says so", () => {
	const r = run(["run", "--impl", "pi", "--lane", "dcore-store", "--out", join(work, "zero-run"), "U-memory-storage"])
	assert.notEqual(r.code, 0, `expected non-zero exit when zero rows match; stdout:\n${r.stdout}`)
	assert.match(r.stdout + r.stderr, /zero|no .*row|matched no/i)
})
