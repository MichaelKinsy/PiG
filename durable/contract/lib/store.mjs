// Store files as the contract handles them: a SQLite database is three files while a process holds it (db, -wal, -shm), and
// a crashed process leaves committed transactions only in the WAL. Copying all three is the "same store" the next run opens.
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { basename, join } from "node:path"
import { DatabaseSync } from "node:sqlite"

const SUFFIXES = ["", "-wal", "-shm"]

/** Copy a store (with its WAL) to `dst`. */
export function copyStore(src, dst) {
	mkdirSync(join(dst, ".."), { recursive: true })
	for (const s of SUFFIXES) if (existsSync(src + s)) copyFileSync(src + s, dst + s)
	return dst
}

/** A checkpointed single-file copy of a store in a scratch directory, safe to open read-only; returns [path, cleanup]. */
export function settled(src) {
	const dir = mkdtempSync(join(tmpdir(), "contract-settle-"))
	const path = copyStore(src, join(dir, basename(src)))
	const db = new DatabaseSync(path)
	db.exec("PRAGMA wal_checkpoint(TRUNCATE)")
	db.close()
	for (const s of SUFFIXES.slice(1)) rmSync(path + s, { force: true })
	return [path, () => rmSync(dir, { recursive: true, force: true })]
}
