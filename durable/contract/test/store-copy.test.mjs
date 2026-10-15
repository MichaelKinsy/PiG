// Fixtures are kept read-only (chmod a-w) so that a run that writes a fixture in place fails loudly; every run writes its own copy.
import assert from "node:assert/strict"
import { chmodSync, mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { test } from "node:test"
import { DatabaseSync } from "node:sqlite"
import { copyStore } from "../lib/store.mjs"

test("a copy of a read-only fixture is a writable store", () => {
	const dir = mkdtempSync(join(tmpdir(), "contract-copy-"))
	try {
		const fixture = join(dir, "fixture.sqlite")
		const db = new DatabaseSync(fixture)
		db.exec("CREATE TABLE t (v INTEGER)")
		db.close()
		chmodSync(fixture, 0o444)
		const copy = copyStore(fixture, join(dir, "run", "store.sqlite"))
		const opened = new DatabaseSync(copy)
		opened.exec("INSERT INTO t VALUES (1)")
		assert.equal(opened.prepare("SELECT count(*) AS n FROM t").get().n, 1)
		opened.close()
		assert.throws(() => { const f = new DatabaseSync(fixture); try { f.exec("INSERT INTO t VALUES (2)") } finally { f.close() } }, /readonly/)
	} finally { rmSync(dir, { recursive: true, force: true }) }
})
