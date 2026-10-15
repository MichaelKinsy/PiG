// A candidate that spins must fail its own U row within the timeout and leave the other rows alone (vitest cannot time out a
// synchronous loop, so the gate kills the run).
import assert from "node:assert/strict"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"

process.env.CONTRACT_VITEST_TIMEOUT_MS = "20000"
const { runVitestRow } = await import("../lib/gate.mjs")

const work = mkdtempSync(join(tmpdir(), "contract-hang-"))
test.after(() => rmSync(work, { recursive: true, force: true }))
const candidateDir = join(work, "cand")
mkdirSync(candidateDir)
writeFileSync(join(candidateDir, "ids.ts"), "for (;;) {}\nexport {}\n")
const impl = { name: "spinner", vitest: true, candidateDir, env: {} }
const ctx = { out: join(work, "out") }

test("a spinning candidate fails the row that loads it, naming the timeout", async () => {
	const r = await runVitestRow({ source: "packages/durable/test/memory-storage.test.ts" }, impl, ctx)
	assert.equal(r.status, "fail")
	assert.match(r.detail.problem, /did not finish memory-storage\.test\.ts within 20 s/)
})

test("a row whose file never loads the spinning module is unaffected", async () => {
	const r = await runVitestRow({ source: "packages/durable/test/env-truncate.test.ts" }, impl, ctx)
	assert.equal(r.status, "pass")
})

// An implementation with an SC provider (sc_exe) still runs its U rows on the candidate: the provider swaps the SC suite's
// Storage only, so the candidate's modules must still replace the reference's in the other files.
test("a candidate with an sc_exe is still the candidate in a U row", async () => {
	const r = await runVitestRow({ source: "packages/durable/test/memory-storage.test.ts" }, { ...impl, name: "spinner-sc", scExe: "/bin/true" }, ctx)
	assert.equal(r.status, "fail")
	assert.match(r.detail.problem, /did not finish/)
})
