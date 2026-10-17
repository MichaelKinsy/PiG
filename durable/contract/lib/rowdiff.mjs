// rowdiff (CONTRACT section 7.4): compare two runs of one scenario at the store, commit, context and output levels. A
// report names the first divergent commit seq and the field-level difference of each TEXT column, classified as value,
// key-order, number-format-or-escape or text, because a key-order-only and a number-format-only difference both break the
// `store` level although the decoded values match (CONTRACT 5.2).
import { readFileSync } from "node:fs"
import { DatabaseSync } from "node:sqlite"
import { EXCLUDED } from "./capsql.mjs"

export const LEVELS = ["store", "commit", "context", "output"]
export const readTrace = path => readFileSync(path, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))

const sortKeys = v => (Array.isArray(v) ? v.map(sortKeys) : v && typeof v === "object" ? Object.fromEntries(Object.keys(v).sort().map(k => [k, sortKeys(v[k])])) : v)
const same = (x, y) => JSON.stringify(x) === JSON.stringify(y)
const firstPath = (a, b, path = "$") => {
	if (same(a, b)) return undefined
	if (a && b && typeof a === "object" && typeof b === "object" && Array.isArray(a) === Array.isArray(b)) {
		for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) {
			const r = firstPath(a[k], b[k], `${path}${Array.isArray(a) ? `[${k}]` : `.${k}`}`)
			if (r) return r
		}
	}
	return path
}

/** What kind of difference separates two column values (undefined when equal). */
export function classify(a, b) {
	if (same(a, b)) return undefined
	if (typeof a !== "string" || typeof b !== "string") return { kind: "value" }
	let pa, pb
	try { pa = JSON.parse(a); pb = JSON.parse(b) } catch { return { kind: "text" } }
	if (same(sortKeys(pa), sortKeys(pb))) return { kind: same(pa, pb) ? "number-format-or-escape" : "key-order", path: firstPath(JSON.parse(JSON.stringify(pa)), pb) }
	return { kind: "value", path: firstPath(pa, pb) }
}

function diffRow(x, y) {
	if (same(x, y)) return undefined
	const columns = []
	for (let i = 0; i < Math.max(x?.length ?? 0, y?.length ?? 0); i++) {
		if (!same(x?.[i], y?.[i])) columns.push({ column: i, ...(classify(x?.[i], y?.[i]) ?? { kind: "value" }), a: x?.[i], b: y?.[i] })
	}
	return columns
}

const commitsOf = trace => {
	const by = new Map()
	for (const l of trace) if (l.t === "commit") (by.get(l.store) ?? by.set(l.store, []).get(l.store)).push(l)
	return by
}

/** The commit level: per store, every commit's row changes in order. */
export function diffCommits(a, b) {
	const ca = commitsOf(a), cb = commitsOf(b)
	for (const store of new Set([...ca.keys(), ...cb.keys()])) {
		const xs = ca.get(store) ?? [], ys = cb.get(store) ?? []
		for (let i = 0; i < Math.max(xs.length, ys.length); i++) {
			const x = xs[i], y = ys[i]
			if (!x || !y) return { level: "commit", store, commit: i, seq: (x ?? y).seq, problem: x ? "only in A" : "only in B", count: [xs.length, ys.length] }
			if (x.seq !== y.seq) return { level: "commit", store, commit: i, problem: "different seq", a: x.seq, b: y.seq }
			for (let j = 0; j < Math.max(x.changes.length, y.changes.length); j++) {
				const p = x.changes[j], q = y.changes[j]
				if (!p || !q || p.table !== q.table || p.op !== q.op || !same(p.pk, q.pk)) {
					return { level: "commit", store, commit: i, seq: x.seq, change: j, problem: "different change", a: p && { table: p.table, op: p.op, row: p.after ?? p.before }, b: q && { table: q.table, op: q.op, row: q.after ?? q.before } }
				}
				const d = diffRow(p.after ?? p.before, q.after ?? q.before)
				if (d) return { level: "commit", store, commit: i, seq: x.seq, change: j, table: p.table, op: p.op, columns: d }
			}
		}
	}
	return undefined
}

/** The context level: the fingerprints named by `keys` (ctx and bench) of every model call, in call order. */
export function diffContexts(a, b, keys = ["ctx", "bench"]) {
	const ma = a.filter(l => l.t === "model"), mb = b.filter(l => l.t === "model")
	for (let i = 0; i < Math.max(ma.length, mb.length); i++) {
		const x = ma[i], y = mb[i]
		if (!x || !y) return { level: "context", call: i, problem: x ? "only in A" : "only in B", count: [ma.length, mb.length] }
		if (keys.some(k => x[k] !== y[k])) return { level: "context", call: i, a: { ctx: x.ctx, bench: x.bench, messages: x.messages }, b: { ctx: y.ctx, bench: y.bench, messages: y.messages } }
	}
	return undefined
}

/** The output level: the scenario's stdout lines (and the exit status). */
export function diffOutput(a, b) {
	const oa = a.filter(l => l.t === "out"), ob = b.filter(l => l.t === "out")
	for (let i = 0; i < Math.max(oa.length, ob.length); i++) {
		if (oa[i]?.text !== ob[i]?.text) return { level: "output", line: i, a: oa[i]?.text, b: ob[i]?.text }
	}
	const ea = a.findLast(l => l.t === "end"), eb = b.findLast(l => l.t === "end")
	if (ea && eb && (ea.code !== eb.code || ea.signal !== eb.signal)) return { level: "output", problem: "exit status", a: ea, b: eb }
	return undefined
}

/** The store level from the traces' `final` lines (per-table digests per store, last close wins). */
export function diffFinals(a, b) {
	const last = t => { const m = new Map(); for (const l of t) if (l.t === "final") m.set(l.store, l.tables); return m }
	const fa = last(a), fb = last(b)
	const out = []
	for (const store of new Set([...fa.keys(), ...fb.keys()])) {
		const x = fa.get(store), y = fb.get(store)
		if (!x || !y) { out.push({ store, problem: x ? "only in A" : "only in B" }); continue }
		for (const table of new Set([...Object.keys(x), ...Object.keys(y)])) if (x[table] !== y[table]) out.push({ store, table, a: x[table], b: y[table] })
	}
	return out.length ? { level: "store", digests: out } : undefined
}

/** The store level from the SQLite files: every Pi table, column by column, with classified text differences. */
export function diffStores(pathA, pathB) {
	const open = p => new DatabaseSync(p, { readOnly: true })
	const a = open(pathA), b = open(pathB)
	const tables = db => db.prepare("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name").all().map(r => r.name).filter(n => !EXCLUDED.test(n))
	const out = []
	for (const t of new Set([...tables(a), ...tables(b)])) {
		const rows = db => { try { const s = db.prepare(`SELECT * FROM "${t}" ORDER BY rowid`); s.setReturnArrays(true); return s.all() } catch { return undefined } }
		const x = rows(a), y = rows(b)
		if (!x || !y) { out.push({ table: t, problem: x ? "only in A" : "only in B" }); continue }
		let first, count = 0
		for (let i = 0; i < Math.max(x.length, y.length); i++) {
			const d = diffRow(x[i]?.map(blobHex), y[i]?.map(blobHex))
			if (d) { count++; first ??= { row: i, columns: d } }
		}
		if (count || x.length !== y.length) out.push({ table: t, rows: [x.length, y.length], differing: count, first })
	}
	a.close(); b.close()
	return out.length ? { level: "store", tables: out } : undefined
}
const blobHex = v => (v instanceof Uint8Array ? `blob:${Buffer.from(v).toString("hex")}` : typeof v === "bigint" ? Number(v) : v)

/** Compare two traces (and optionally their store files) at the requested levels; returns {equal, reports}. */
export function diffTraces(a, b, { levels = LEVELS, storesA = [], storesB = [], contexts } = {}) {
	const reports = []
	const add = r => r && reports.push(r)
	// A candidate that asked the tape for something the recording does not hold has already diverged, whatever else matches.
	for (const [side, trace] of [["A", a], ["B", b]]) { const m = trace.find(l => l.t === "mismatch"); if (m) add({ level: "context", problem: `tape mismatch in ${side}`, kind: m.kind, index: m.index, recorded: m.recorded, got: m.got }) }
	if (levels.includes("commit")) add(diffCommits(a, b))
	if (levels.includes("context")) add(diffContexts(a, b, contexts))
	if (levels.includes("output")) add(diffOutput(a, b))
	if (levels.includes("store")) {
		add(diffFinals(a, b))
		for (let i = 0; i < Math.max(storesA.length, storesB.length); i++) {
			if (!storesA[i] || !storesB[i]) { reports.push({ level: "store", problem: `store file ${i} only in ${storesA[i] ? "A" : "B"}` }); continue }
			add(diffStores(storesA[i], storesB[i]))
		}
	}
	return { equal: reports.length === 0, reports }
}
