// Crash points of scenarios/crash-conversation.mjs where every build differs from Pi under the same crash (robust-durable,
// routed to dcore-cfhost with dcore-int: admission order on restart). Found by `robust crash` over all 162 commits at
// 51a5e42725; the three builds fail identically, so the cause is the core's scheduling, not a binding.
//   k=54..56  the final store differs: the restarted program's resubmission of t4 mints submission 46 before the recovered
//             generation's task 47, where Pi mints the task (46) first and the submission (47) after it
//   k=57      the final store equals Pi's but the restart commits in another order: the write w1 is admitted (seq 61) before the
//             background compaction's start (Pi seq 61) and the generation attempt's live-document delta (Pi seq 62)
//   k=75..77  the same order difference later in the inbox phase
// Each is a real process killed with SIGKILL after commit k and restarted with the same program (lib/robust.mjs crash).
import assert from "node:assert/strict"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { ensureBuilt } from "../lib/gate.mjs"
import { crash } from "../lib/robust.mjs"

test("native: a restart after a crash in the inbox phase admits work in Pi's order", { timeout: 600_000 }, async () => {
	await ensureBuilt("native")
	const out = mkdtempSync(join(tmpdir(), "robust-crash-red-"))
	try {
		const r = await crash({ impls: ["native"], out, points: [54, 57, 75] })
		assert.equal(r.uninterrupted.native.ok, true, JSON.stringify(r.uninterrupted.native))
		const failed = r.points.filter(p => !p.ok).map(p => `k=${p.k} ${p.impl}: ${p.problem} ${JSON.stringify(p.report).slice(0, 300)}`)
		assert.deepEqual(failed, [])
	} finally { rmSync(out, { recursive: true, force: true }) }
})

// The uninterrupted run itself: at the background compaction's start (Pi commits 79-81) the compaction's summarize read and the
// generation's tool-round commit race for the Session line two ticks apart, so the tool task's start (Pi commit 80) precedes the
// compaction's summary (Pi commit 81) only when a kept context range does not return later than Pi's three hops (context.ts
// readContextFrom :75-102): with four (a5c57503df) the core commits the summary first (commit 80: a document_revisions delete where
// Pi updates durable_metadata). The other tick fixes of 9c013f53c0 (job tails, the idle read job's Begin, the publication split,
// runtime.env) do not move commit 80; F-ticks, F-split and the session/core unit tests guard them.
test("native: the uninterrupted run and a crash at the compaction's start commit in Pi's order", { timeout: 600_000 }, async () => {
	await ensureBuilt("native")
	const out = mkdtempSync(join(tmpdir(), "robust-crash-compaction-"))
	try {
		const r = await crash({ impls: ["native"], out, points: [80] })
		assert.equal(r.uninterrupted.native.ok, true, JSON.stringify(r.uninterrupted.native).slice(0, 600))
		const failed = r.points.filter(p => !p.ok).map(p => `k=${p.k} ${p.impl}: ${p.problem} ${JSON.stringify(p.report).slice(0, 300)}`)
		assert.deepEqual(failed, [])
	} finally { rmSync(out, { recursive: true, force: true }) }
})
