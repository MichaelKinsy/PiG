// replay (lib/replay.mjs) on a synthetic trace: a commit that consumed no seq and changed no Pi table is not a Pi commit (as
// capsql's commit() rules for a live capture) and is dropped; a seq-consuming commit that changes nothing still fails.
import assert from "node:assert/strict"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import test from "node:test"
import { replay } from "../lib/replay.mjs"

const dir = mkdtempSync(join(tmpdir(), "contract-replay-"))
test.after(() => rmSync(dir, { recursive: true, force: true }))

function start(name) {
	const file = join(dir, `${name}.sqlite`)
	const db = new DatabaseSync(file)
	db.exec(`CREATE TABLE durable_schema (singleton INTEGER PRIMARY KEY, version INTEGER NOT NULL);
		INSERT INTO durable_schema VALUES (1, 1);
		CREATE TABLE durable_metadata (singleton INTEGER PRIMARY KEY, next_id TEXT NOT NULL, next_seq INTEGER NOT NULL);
		INSERT INTO durable_metadata VALUES (1, '2', 1);
		CREATE TABLE entries (id INTEGER PRIMARY KEY, x TEXT NOT NULL)`)
	db.close()
	return file
}

const sql = [
	"CREATE TABLE IF NOT EXISTS durable_schema (singleton INTEGER PRIMARY KEY, version INTEGER NOT NULL)",
	"INSERT INTO entries (id, x) VALUES (?, ?)",
	"UPDATE durable_metadata SET next_seq = ? WHERE singleton = 1 AND next_seq = ?",
].map((text, q) => ({ t: "sql", store: "s0", q, text }))

test("replay drops cold open's DDL-only commit and keeps the Pi commits", () => {
	const trace = [...sql,
		{ t: "commit", store: "s0", seq: -1, stmts: [{ q: 0, k: "w", p: [] }] },
		{ t: "commit", store: "s0", seq: 1, stmts: [{ q: 1, k: "w", p: [2, "a"] }, { q: 2, k: "w", p: [2, 1] }] },
	]
	const r = replay(trace, start("ddl"), join(dir, "ddl"))
	const commits = r.trace.filter(l => l.t === "commit")
	assert.equal(r.commits, 1)
	assert.equal(commits.length, 1)
	assert.equal(commits[0].seq, 1)
	assert.deepEqual(commits[0].changes.map(c => c.table).sort(), ["durable_metadata", "entries"])
})

test("replay still fails a seq-consuming commit that changes nothing", () => {
	const trace = [...sql, { t: "commit", store: "s0", seq: 1, stmts: [{ q: 0, k: "w", p: [] }] }]
	assert.throws(() => replay(trace, start("empty"), join(dir, "empty")), /commit 1 changed no Pi table/)
})
