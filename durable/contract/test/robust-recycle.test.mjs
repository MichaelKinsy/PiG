// Recycling across a crash point (Durable plan 2 item 3). The scenario of `robust crash` runs with a core trap after commit k instead of a
// kill: the host drops the trapped Wasm instance, reopens from SQLite and the same process finishes the program. Each point must lose or
// duplicate no submission and print what Pi prints for that interruption (the uninterrupted run's output, or that of Pi killed after the
// same commit and restarted). The points are ones where the work in flight at the trap is a generation, a tool, an inbox admission, a
// background compaction and a fork; `robust recycle --every` runs all of them.
import assert from "node:assert/strict"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { ensureBuilt } from "../lib/gate.mjs"
import { recycle } from "../lib/robust.mjs"

test("wasm-go: a core trap after a commit is recovered in process and the conversation finishes as Pi's does", { timeout: 900_000 }, async () => {
	await ensureBuilt("wasm-go")
	const out = mkdtempSync(join(tmpdir(), "robust-recycle-"))
	try {
		const r = await recycle({ impls: ["wasm-go"], out, points: [3, 17, 40, 46, 96, 105], concurrency: 3 })
		assert.equal(r.uninterrupted["wasm-go"].ok, true, JSON.stringify(r.uninterrupted["wasm-go"]))
		const failed = r.points.filter(p => !p.ok).map(p => `k=${p.k} ${p.impl}: ${p.problem} ${JSON.stringify(p.report).slice(0, 300)}`)
		assert.deepEqual(failed, [])
		assert.equal(r.points.length, 6)
	} finally { rmSync(out, { recursive: true, force: true }) }
})
