// capsql (CONTRACT section 7.1): a node:sqlite database that records every statement with its parameters and, for each
// committed transaction, the row changes (a SQLite session per transaction, decoded per table). One Capture serves
// pi-durable through `piDurableDatabase` (the SqliteDatabase facade pi-durable's SqliteStorage takes) and a candidate
// through `capturingStore` (the host loop's store). Both write the trace format of CONTRACT section 7.6.
//
// A commit's `seq` is the `next_seq` it consumed. `stmts` lists the transaction's statements in order with `k` r (a read)
// or w (a write); the `commit` level compares `changes`, the `statement` level may compare `stmts`.
import { createHash } from "node:crypto"
import { DatabaseSync } from "node:sqlite"
import { decodeChangeset } from "./changeset.mjs"
import { emit } from "./trace.mjs"

/** Tables that are not part of Pi's store: workerd's, SQLite's and the core's sidecar (CONTRACT section 2.1). */
export const EXCLUDED = /^(pig_|_cf_|__cf_|sqlite_)/

const plain = v => v instanceof Uint8Array || v instanceof ArrayBuffer ? { blob: Buffer.from(v).toString("hex") } : typeof v === "bigint" ? Number(v) : v
const jsonable = c => ({ ...c, before: c.before?.map(plain), after: c.after?.map(plain) })

/** The state a process-wide crash switch needs: commits are counted across every Capture. */
const crash = { after: process.env.CONTRACT_CRASH_AFTER === undefined ? undefined : Number(process.env.CONTRACT_CRASH_AFTER) }

let storeCount = 0

/** A changeset lists tables in the order the session first saw them: sort by table and primary key so that two writers that
 * touch the same rows in a different statement order produce the same commit. */
function canonicalOrder(changes) {
	const keyOf = c => {
		const image = c.after ?? c.before
		return c.pk.map((rank, i) => [rank, image[i]]).filter(([rank]) => rank > 0).sort((x, y) => x[0] - y[0]).map(([, v]) => v)
	}
	const keyed = changes.map(c => [keyOf(c), c])
	keyed.sort(([a, x], [b, y]) => {
		if (x.table !== y.table) return x.table < y.table ? -1 : 1
		for (let i = 0; i < Math.max(a.length, b.length); i++) if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1
		return 0
	})
	return keyed.map(([, c]) => c)
}

export class Capture {
	/** @param {string} path @param {{label?: string, sink?: (line: object) => void}} [options] */
	constructor(path, options = {}) {
		this.path = path
		this.store = options.label ?? `s${storeCount++}`
		this.sink = options.sink ?? emit
		this.db = new DatabaseSync(path)
		this.db.exec("PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL")
		this.texts = new Map()
		this.stmts = []
		this.session = undefined
		this.cache = new Map()
		this.commits = 0
		this.closed = false
		this.sink({ t: "store", store: this.store, path })
	}

	#q(text) {
		let q = this.texts.get(text)
		if (q === undefined) { q = this.texts.size; this.texts.set(text, q); this.sink({ t: "sql", store: this.store, q, text }) }
		return q
	}
	#prepare(text) {
		let s = this.cache.get(text)
		if (!s) { s = this.db.prepare(text); this.cache.set(text, s) }
		return s
	}

	begin() {
		this.db.exec("BEGIN IMMEDIATE")
		this.session = this.db.createSession()
		this.stmts = []
	}
	/** A write. Outside a transaction (pi-durable's schema setup on open) nothing is part of a commit: run it, record nothing. */
	run(text, params = []) {
		if (!this.session) { if (params.length) return Number(this.#prepare(text).run(...params).changes); this.db.exec(text); return 0 }
		this.stmts.push({ q: this.#q(text), k: "w", p: params.map(plain) })
		if (params.length) return Number(this.#prepare(text).run(...params).changes)
		this.db.exec(text)
		return 0
	}
	/** A read: recorded inside a transaction, returns rows as objects. */
	get(text, params = []) { return this.#read(text, params, false)[0] }
	all(text, params = []) { return this.#read(text, params, false) }
	/** A read that returns arrays (the candidate's host loop). */
	rows(text, params = []) { return this.#read(text, params, true) }
	#read(text, params, arrays) {
		if (this.session) this.stmts.push({ q: this.#q(text), k: "r", p: params.map(plain) })
		const s = this.#prepare(text)
		s.setReturnArrays(arrays)
		return s.all(...params)
	}
	exec(text) { this.db.exec(text) }

	commit() {
		const changes = canonicalOrder(decodeChangeset(new Uint8Array(this.session.changeset())).filter(c => !EXCLUDED.test(c.table)))
		this.session.close()
		this.session = undefined
		this.db.exec("COMMIT")
		const stmts = this.stmts
		this.stmts = []
		// A commit that touched only excluded tables (a sidecar write) is not a Pi commit: it has no seq and counts for nothing.
		if (!changes.length) return
		const meta = changes.find(c => c.table === "durable_metadata")
		// durable_metadata(singleton, next_id, next_seq, ...): the before image of its update carries the seq this commit consumed.
		const seq = meta?.before ? Number(this.#metadataSeq(meta.before)) : undefined
		this.commits++
		this.sink({ t: "commit", store: this.store, seq, stmts: stmts.filter(s => s.k === "w"), reads: stmts.length - stmts.filter(s => s.k === "w").length, changes: changes.map(jsonable) })
		if (crash.after !== undefined && seq !== undefined && seq >= crash.after) process.kill(process.pid, "SIGKILL")
	}
	#metadataSeq(row) {
		this.cols ??= this.db.prepare("PRAGMA table_info(durable_metadata)").all().map(c => c.name)
		return row[this.cols.indexOf("next_seq")]
	}
	rollback() { this.session?.close(); this.session = undefined; this.db.exec("ROLLBACK"); this.stmts = [] }

	/** Per-table digests of the Pi tables, in rowid order (the `final` line of the trace). */
	digests() {
		const out = {}
		for (const { name } of this.db.prepare("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name").all()) {
			if (EXCLUDED.test(name)) continue
			const h = createHash("sha256")
			const s = this.db.prepare(`SELECT * FROM "${name}" ORDER BY rowid`)
			s.setReturnArrays(true)
			for (const row of s.all()) h.update(JSON.stringify(row.map(v => (v instanceof Uint8Array ? Buffer.from(v).toString("hex") : typeof v === "bigint" ? Number(v) : v))) + "\n")
			out[name] = h.digest("hex")
		}
		return out
	}
	/** Emit the `final` line and close. */
	finish() {
		if (this.closed) return
		this.closed = true
		this.cache.clear()
		this.sink({ t: "final", store: this.store, tables: this.digests() })
		try { this.db.exec("PRAGMA wal_checkpoint(TRUNCATE)") } catch { /* a store still in a transaction at exit keeps its WAL */ }
		this.db.close()
	}
}

/** The SqliteDatabase facade of @earendil-works/pi-durable/storage/sqlite over a Capture. */
export function piDurableDatabase(cap) {
	const direct = {
		exec: async q => { cap.exec(q) },
		run: async (q, ...p) => { cap.run(q, p) },
		get: async (q, ...p) => cap.get(q, p),
		all: async (q, ...p) => cap.all(q, p),
	}
	let queue = Promise.resolve()
	const serial = work => { const r = queue.then(work); queue = r.catch(() => {}); return r }
	return {
		exec: q => serial(() => direct.exec(q)),
		run: (q, ...p) => serial(() => direct.run(q, ...p)),
		get: (q, ...p) => serial(() => direct.get(q, ...p)),
		all: (q, ...p) => serial(() => direct.all(q, ...p)),
		transaction: cb => serial(async () => {
			cap.begin()
			try { const r = await cb(direct); cap.commit(); return r } catch (e) { cap.rollback(); throw e }
		}),
		close: async () => { cap.finish() },
	}
}

/** A host-loop store (one transaction per commit) over a Capture. */
export function capturingStore(cap) {
	return {
		cap,
		run: (q, p = []) => cap.run(q, p),
		all: (q, p = []) => cap.rows(q, p),
		tx(work) { cap.begin(); try { work() } catch (e) { cap.rollback(); throw e } cap.commit() },
		close: () => cap.finish(),
	}
}
