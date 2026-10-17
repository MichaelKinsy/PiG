// The row-identity oracle of the store (CONTRACT section 5.1): apply a script of StorageWrite batches with pi-durable
// main's own SqliteStorage on node:sqlite, then compare every Pi table of the result with a database the core's store
// wrote from the same script.
//
//   ORACLE_DIR=<scratch directory> node --import durable/contract/hooks/register.mjs oracle.mjs <mode> ...
//
// script.json: {"commits": [{"mint": n, "writes": [StorageWrite, ...]}, ...]}. "mint" calls mintId() n times before the
// commit, as a Session callback does. Prints one JSON line per commit ({"commit", "seq"} or {"commit", "error"}) and
// then OK, or the first difference between the two databases (exit status 1).
import { copyFileSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { pathToFileURL } from "node:url"

// Modes:
//   compare  <script.json> <core.sqlite>             apply the script to a fresh pi-durable store; compare with core.sqlite
//   write    <script.json> <out.sqlite>              apply the script to a fresh pi-durable store and keep it at out.sqlite
//   continue <script.json> <base.sqlite> <core.sqlite>
//                                                    apply the script to a copy of base.sqlite (a store pi-durable wrote
//                                                    earlier, reopened cold); compare with core.sqlite
const [mode, scriptPath, arg1, arg2] = process.argv.slice(2)
const script = JSON.parse(readFileSync(scriptPath, "utf8"))
const piUrl = rel => pathToFileURL(`${process.env.CONTRACT_PI_SOURCE}/packages/durable/src/${rel}`).href + "?real"
const { openNodeSqliteStorage } = await import(piUrl("storage/sqlite/node.ts"))

const piPath = mode === "write" ? arg1 : join(process.env.ORACLE_DIR, "pi.sqlite")
if (mode === "continue") copyFileSync(arg1, piPath)
const corePath = mode === "continue" ? arg2 : arg1
const storage = await openNodeSqliteStorage(piPath)
const results = []
for (const [i, c] of script.commits.entries()) {
	for (let k = 0; k < (c.mint ?? 0); k++) await storage.mintId()
	try {
		const seq = await storage.commit(c.writes, undefined)
		results.push({ commit: i, seq })
	} catch (e) {
		results.push({ commit: i, error: String(e?.message ?? e) })
	}
}
await storage.close(undefined)
for (const r of results) console.log(JSON.stringify(r))
if (mode === "write") process.exit(0)

const TABLES = [
	["durable_schema", "singleton"],
	["durable_metadata", "singleton"],
	["record_ids", "id"],
	["conversations", "id"],
	["entries", "id"],
	["tasks", "id"],
	["submissions", "id"],
	["documents", "id"],
	["document_revisions", "document_id, seq"],
]
const dump = path => {
	const db = new DatabaseSync(path, { readOnly: true })
	const out = {}
	for (const [t, order] of TABLES) out[t] = db.prepare(`SELECT * FROM ${t} ORDER BY ${order}`).all().map(r => JSON.stringify(r))
	db.close()
	return out
}
const piDump = dump(piPath)
const coreDump = dump(corePath)
for (const [t] of TABLES) {
	const a = piDump[t], b = coreDump[t]
	for (let i = 0; i < Math.max(a.length, b.length); i++) {
		if (a[i] !== b[i]) {
			console.log(`DIFF ${t} row ${i}\n pi:   ${a[i]}\n core: ${b[i]}`)
			process.exit(1)
		}
	}
}
console.log(`OK ${TABLES.map(([t]) => `${t}=${piDump[t].length}`).join(" ")}`)
