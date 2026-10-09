// Replay (CONTRACT section 7.1, "the same adapter serves Miniflare captures by replaying the recorded statements into
// node:sqlite"): a native candidate (Go with modernc SQLite, or a Durable Object) cannot produce SQLite session changesets, so
// it records only each commit's statements. `replay` applies those statements, in order and one transaction per commit, to a
// copy of the store the run started from, through capsql, and so produces the commit lines with row changes the comparison
// needs. The candidate's own store file is still compared at the store level, so a replay cannot hide a difference there.
import { mkdirSync } from "node:fs"
import { join } from "node:path"
import { Capture } from "./capsql.mjs"
import { copyStore } from "./store.mjs"

const param = p => (p && typeof p === "object" && "blob" in p ? Buffer.from(p.blob, "hex") : p)

/**
 * @param {object[]} trace the candidate's trace: `sql` lines (statement text by index) and `commit` lines with `stmts`
 * @param {string} start the store the candidate's run started from
 * @param {string} dir where the replayed store goes
 * @returns {{trace: object[], store: string}} the trace with `changes` filled in on every commit, and the replayed store
 */
export function replay(trace, start, dir) {
	mkdirSync(dir, { recursive: true })
	const file = copyStore(start, join(dir, "replay.sqlite"))
	const lines = []
	const cap = new Capture(file, { label: "s0", sink: l => lines.push(l) })
	lines.length = 0
	const text = new Map()
	for (const l of trace) if (l.t === "sql") text.set(`${l.store ?? "s0"}:${l.q}`, l.text)
	const out = []
	let commits = 0
	for (const l of trace) {
		if (l.t !== "commit") { if (l.t !== "sql" && l.t !== "final" && l.t !== "store") out.push(l); continue }
		cap.begin()
		for (const s of l.stmts) {
			const q = text.get(`${l.store ?? "s0"}:${s.q}`)
			if (q === undefined) throw new Error(`replay: commit ${l.seq} uses statement ${s.q}, which no sql line defines`)
			cap.run(q, (s.p ?? []).map(param))
		}
		cap.commit()
		const made = lines.filter(x => x.t === "commit")
		lines.length = 0
		commits++
		if (made.length !== 1) throw new Error(`replay: commit ${l.seq} changed no Pi table or more than one transaction`)
		if (l.seq !== undefined && made[0].seq !== l.seq) throw new Error(`replay: commit ${l.seq} replayed as seq ${made[0].seq}`)
		out.push({ ...l, store: "s0", reads: made[0].reads, changes: made[0].changes })
	}
	cap.finish()
	out.push(...lines.filter(x => x.t === "final"))
	return { trace: out, store: file, commits }
}
