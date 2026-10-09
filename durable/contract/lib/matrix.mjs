// The crash handoff matrix (CONTRACT section 5.5, 7.5). For one scenario and each crash point k (after the commit that
// consumed seq k) it runs the orders
//   control  pi -> pi          P->X  pi -> candidate         X->P  candidate -> pi          X->X  candidate -> candidate
// in fresh directories. Run 1 abandons its process after commit k (SIGKILL: no close, no flush); run 2 opens the store the
// crash left, recovers and finishes the turn. Every non-control order must end store-equal to the control, its run-1 store
// must equal the control's at the crash, and the commits of its run 2 must equal the control's run 2. `k = end` is the
// clean restart row: run 1 completes, run 2 reopens and runs a follow-up turn.
import { mkdirSync, rmSync } from "node:fs"
import { join } from "node:path"
import { IMPLS, runBench } from "./impl.mjs"
import { diffStores, diffTraces, readTrace } from "./rowdiff.mjs"
import { copyStore, settled } from "./store.mjs"

/** Seqs of the commits the measured turn(s) make, from an uncrashed reference run on the fixture. */
export async function crashPoints(fixture, scenario, opts) {
	const dir = join(opts.work, "points")
	rmSync(dir, { recursive: true, force: true })
	const r = await runBench("pi", { ...scenario, store: fixture, out: dir, name: "points" })
	if (r.code !== 0) throw new Error(`reference run failed: ${r.stderr}`)
	return readTrace(r.trace).filter(l => l.t === "commit" && l.seq !== undefined).map(l => l.seq)
}

const storeEqual = (a, b) => {
	const [pa, ca] = settled(a), [pb, cb] = settled(b)
	try { return diffStores(pa, pb) } finally { ca(); cb() }
}

/** Pick `n` crash points spread over the list, always including the first and the last (CONTRACT 5.5: 3,500 samples 10). */
export function sample(points, n) {
	if (!n || n >= points.length) return points
	const picks = new Set([0, points.length - 1])
	for (let i = 1; picks.size < n; i++) picks.add(Math.round((i * (points.length - 1)) / (n - 1)))
	return [...picks].sort((a, b) => a - b).map(i => points[i])
}

/**
 * Run the matrix for `candidate` (an implementation name) on `scenario` over `fixture`.
 * @returns {Promise<{rows: object[], failed: number}>} one row per crash point per order
 */
export async function matrix({ fixture, scenario, candidate, work, points, orders = ["P>X", "X>P", "X>X"], concurrency = 8, onRow, injectAtCrash }) {
	mkdirSync(work, { recursive: true })
	const ks = [...(points ?? (await crashPoints(fixture, scenario, { work }))), "end"]
	const rows = []
	const queue = [...ks]
	async function one(k) {
		const dir = join(work, `k${k}`)
		rmSync(dir, { recursive: true, force: true })
		// injectAtCrash adds an unknown field to the live records of the crashed store, before run 2 opens it (X1 on live state).
		const run = (impl, name, store, extra) => runBench(impl, { ...scenario, ...extra, inject: extra?.recover || extra?.label === "f" ? injectAtCrash : scenario.inject, store, out: join(dir, name), name })
		const fail = (order, problem) => rows.push({ k, order, ok: false, ...problem })
		const ok = order => rows.push({ k, order, ok: true })

		// control: pi -> pi
		// Run 2 only finishes the interrupted turn (turns: 0); the end row's run 2 is a follow-up turn instead.
		const second = k === "end" ? { turns: 1, label: "f" } : { recover: true, turns: 0 }
		const first = k === "end" ? {} : { crashAfter: k }
		const c1 = await run("pi", "c1", fixture, first)
		const c2 = await run("pi", "c2", c1.stores[0], second)
		if (c2.code !== 0) return fail("control", { problem: `control recovery failed: ${c2.stderr.slice(0, 300)}` })
		const control2 = readTrace(c2.trace)

		const check = (order, r1, r2) => {
			if (r2.code !== 0) return fail(order, { problem: `run 2 exited ${r2.code} ${r2.signal ?? ""}: ${r2.stderr.slice(0, 400)}` })
			const atCrash = storeEqual(c1.stores[0], r1.stores[0])
			if (atCrash) return fail(order, { level: "store", phase: "at crash", report: atCrash })
			const end = storeEqual(c2.stores[0], r2.stores[0])
			if (end) return fail(order, { level: "store", phase: "final", report: end })
			const d = diffTraces(control2, readTrace(r2.trace), { levels: ["commit", "context"], contexts: IMPLS[candidate].contexts })
			if (!d.equal) return fail(order, { level: "commit", phase: "run 2", report: d.reports })
			ok(order)
		}
		let x1
		if (orders.includes("P>X")) { const x2 = await run(candidate, "px2", c1.stores[0], second); check("P>X", c1, x2) }
		if (orders.includes("X>P") || orders.includes("X>X")) {
			x1 = await run(candidate, "x1", fixture, first)
			if (orders.includes("X>P")) { const p2 = await run("pi", "xp2", x1.stores[0], second); check("X>P", x1, p2) }
			if (orders.includes("X>X")) { const x2 = await run(candidate, "xx2", x1.stores[0], second); check("X>X", x1, x2) }
		}
		rmSync(dir, { recursive: true, force: true })
	}
	await Promise.all(Array.from({ length: Math.min(concurrency, queue.length) }, async () => {
		for (let k = queue.shift(); k !== undefined; k = queue.shift()) { const before = rows.length; await one(k); rows.slice(before).forEach(r => onRow?.(r)) }
	}))
	rows.sort((a, b) => (a.k === "end" ? Infinity : a.k) - (b.k === "end" ? Infinity : b.k) || a.order.localeCompare(b.order))
	void copyStore
	return { rows, failed: rows.filter(r => !r.ok).length }
}
