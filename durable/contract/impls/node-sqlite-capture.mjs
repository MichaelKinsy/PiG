// `node:sqlite` as the reference's Node adapter (storage/sqlite/node.ts) sees it under CONTRACT_STORAGE_PROFILE=node: a
// DatabaseSync whose statements run on a capsql Capture. The adapter's own SerialOperationQueue and transaction code run
// unchanged, so the reference spends exactly the microtask hops the real adapter spends (the default profile routes the same
// SqliteStorage through lib/capsql.mjs piDurableDatabase, whose serial queue adds hops no product adapter has: the Pi tick
// traces' storage profile, questions/dcore-yield.md S7).
import { openCapture } from "./capture.mjs"

export * from "node:sqlite"

class Statement {
	constructor(cap, sql) { this.cap = cap; this.sql = sql }
	run(...params) { return { changes: this.cap.run(this.sql, params) } }
	get(...params) { return this.cap.get(this.sql, params) }
	all(...params) { return this.cap.all(this.sql, params) }
}

let memoryStores = 0

export class DatabaseSync {
	// The adapter skips its mkdir (a real file-system hop) only for the exact path ":memory:"; the gate's memory stores are told apart here.
	constructor(path) { this.cap = openCapture(path === ":memory:" ? `:memory:${memoryStores++}` : path) }
	exec(sql) {
		const word = sql.trim().toUpperCase()
		if (word.startsWith("BEGIN")) this.cap.begin()
		else if (word === "COMMIT") this.cap.commit()
		else if (word === "ROLLBACK") this.cap.rollback()
		else if (word.startsWith("PRAGMA WAL_CHECKPOINT")) this.cap.exec(sql)
		else this.cap.exec(sql)
	}
	prepare(sql) { return new Statement(this.cap, sql) }
	close() { this.cap.finish() }
}
