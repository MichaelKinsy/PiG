// Unknown-field injection (CONTRACT section 2.3 rule 3, scenario X1): add a field no code knows to a record that pi-durable
// rewrites or carries, before a run opens the store. Pi carries it through a spread or draft mutation in the same position
// and drops it where its code builds a record from known fields; the candidate must do exactly what Pi does with the same
// input. `where` names the record: entry (immutable, passed through as stored bytes), usage and live (document bases),
// task, state, checkpoint, input (the live tasks' record, state, checkpoint and input) and submission (queued or placed).
import { mkdirSync } from "node:fs"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { copyStore, settled } from "./store.mjs"

export const WHERE = ["entry", "usage", "live", "task", "state", "checkpoint", "input", "submission"]

/** Apply the injections to the store file in place (it must be a settled single file). */
export function injectInto(file, wheres) {
	const db = new DatabaseSync(file)
	const put = (table, id, o) => db.prepare(`UPDATE ${table} SET record = ? WHERE id = ?`).run(JSON.stringify(o), id)
	const live = db.prepare("SELECT id, record FROM tasks WHERE status IN ('pending', 'running', 'waiting', 'completing') ORDER BY id").all()
	for (const where of wheres) {
		if (!WHERE.includes(where)) throw new Error(`inject: unknown record ${where}; one of ${WHERE.join(", ")}`)
		if (where === "entry") {
			const row = db.prepare("SELECT id, record FROM entries ORDER BY id LIMIT 1 OFFSET 3").get() ?? db.prepare("SELECT id, record FROM entries ORDER BY id LIMIT 1").get()
			const o = JSON.parse(row.record); o.zz = { a: [1, 2], b: "x" }
			db.prepare("UPDATE entries SET record = ? WHERE id = ?").run(JSON.stringify(o), row.id)
		} else if (where === "usage" || where === "live") {
			const kind = JSON.stringify(where === "usage" ? "pi.usage" : "pi.live")
			const d = db.prepare("SELECT id FROM documents WHERE kind = ?").get(kind)
			const b = db.prepare("SELECT seq, content FROM document_revisions WHERE document_id = ? AND kind = 'base' ORDER BY seq DESC LIMIT 1").get(d.id)
			const o = JSON.parse(b.content); o.zz = 1
			db.prepare("UPDATE document_revisions SET content = ? WHERE document_id = ? AND seq = ?").run(JSON.stringify(o), d.id, b.seq)
		} else if (where === "submission") {
			for (const r of db.prepare("SELECT id, record FROM submissions WHERE status IN ('queued', 'placed')").all()) { const o = JSON.parse(r.record); o.zz = 1; put("submissions", r.id, o) }
		} else {
			for (const r of live) {
				const o = JSON.parse(r.record)
				if (where === "task") o.zz = { a: [1, 2] }
				else if (where === "state") o.state.zz = 1
				else if (where === "checkpoint" && o.state.checkpoint) o.state.checkpoint.zz = 1
				else if (where === "input") o.input.zz = 1
				else continue
				put("tasks", r.id, o)
			}
		}
	}
	db.close()
}

/** A settled copy of `store` with the injections applied, in `dir`; the copy is what a run opens. */
export function injectedCopy(store, dir, name, wheres) {
	const [file, cleanup] = settled(store)
	try {
		injectInto(file, wheres)
		mkdirSync(dir, { recursive: true })
		return copyStore(file, join(dir, `${name}.sqlite`))
	} finally { cleanup() }
}
