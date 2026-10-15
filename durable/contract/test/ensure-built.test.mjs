// A candidate's build runs on every row command, even when its outputs exist: a stale build reported an old core's verdicts as
// the current one's (wasm-go failed E06 after the source was fixed because dist/core-go.wasm was already there).
import assert from "node:assert/strict"
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"

const work = mkdtempSync(join(tmpdir(), "contract-built-"))
test.after(() => rmSync(work, { recursive: true, force: true }))
const marker = join(work, "built")
const impls = join(work, "impls.toml")
// native_exe exists (this very file), so the old rule "build only when an output is missing" would skip the build.
writeFileSync(impls, `[[impl]]\nname = "stale"\nnative_exe = "${join(import.meta.dirname, "ensure-built.test.mjs")}"\nbuild = "touch ${marker}"\n`)
process.env.CONTRACT_IMPLS = impls
const { ensureBuilt } = await import("../lib/gate.mjs")

test("the build runs although the candidate's output exists", async () => {
	delete process.env.CONTRACT_NO_BUILD
	const note = await ensureBuilt("stale")
	assert.match(note, /^built with `touch /)
	assert.ok(existsSync(marker))
})

test("CONTRACT_NO_BUILD=1 skips it", async () => {
	rmSync(marker)
	process.env.CONTRACT_NO_BUILD = "1"
	try { assert.equal(await ensureBuilt("stale"), undefined) } finally { delete process.env.CONTRACT_NO_BUILD }
	assert.ok(!existsSync(marker))
})
