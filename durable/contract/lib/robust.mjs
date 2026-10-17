// Robustness proofs of a Durable core build (cli/robust.mjs). Each proof runs a bounded number of real processes and states
// its claim as an inductive step or an exhaustive enumeration, instead of a long soak:
//   induction   retained memory after warm-up is measured at N, 2N and 4N turns of one long-lived Harness; a bounded core
//               shows the same retained bytes at 2N and 4N, so the growth per turn between them is ~0
//   crash       one scripted conversation (scenarios/crash-conversation.mjs: tools, a sub-agent, the inbox, compaction, a fork) is
//               killed after every commit it makes, one crash point per run, and restarted with the same program; each
//               crash point's stores must equal Pi's under the same crash, and no submission may be lost or duplicated
// A candidate runs through its JS facade (the same host for every build: the Wasm cores in-process, the native core in
// cmd/corenative), so the numbers compare the cores, not their hosts.
import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { facadeBench, runBench } from "./impl.mjs"
import { readSamples } from "./memory.mjs"
import { resolveImpl } from "./gate.mjs"
import { diffStores, diffTraces, readTrace } from "./rowdiff.mjs"
import { CONTRACT_DIR, runScenario } from "./run.mjs"
import { copyStore, settled } from "./store.mjs"

/** A run of the bench scenario program for `impl` (pi or a candidate through its facade). */
export async function benchRunner(implName) {
	if (implName === "pi") return o => runBench("pi", o)
	const impl = await resolveImpl(implName)
	if (!impl.candidateDir) throw new Error(`${implName} cannot run through its facade: ${impl.why ?? "no candidate_dir"}`)
	return facadeBench({ dir: impl.candidateDir, env: impl.env })
}

/** Least-squares slope of y over x. */
export function slope(points) {
	const n = points.length
	if (n < 2) return 0
	const mx = points.reduce((s, p) => s + p[0], 0) / n, my = points.reduce((s, p) => s + p[1], 0) / n
	const sxx = points.reduce((s, p) => s + (p[0] - mx) ** 2, 0)
	return sxx ? points.reduce((s, p) => s + (p[0] - mx) * (p[1] - my), 0) / sxx : 0
}

/** The metrics an induction step reads from a memory sample, by the build's kind. */
export const METRICS = ["heap", "reserved", "jsHeap", "rss"]

/**
 * The induction step over memory samples (one per turn; `turn` counts from 0). Points are the ends of turns W+N, W+2N and
 * W+4N (W warm-up turns). A core may release what it holds in batches (a sawtooth), so each point's value is the envelope:
 * the largest sample in the interval that ends at the point, (W, W+N], (W+N, W+2N] and (W+2N, W+4N]. For each metric:
 *   step1 = (E(W+2N) - E(W+N)) / N        the growth per turn from N to 2N
 *   step2 = (E(W+4N) - E(W+2N)) / 2N       the growth per turn from 2N to 4N
 *   slope = least squares over every sample from W+N to W+4N (reported, not judged: a sawtooth biases it)
 * Growth that is linear gives the same steps as single samples; a sawtooth whose period is shorter than N gives steps of
 * zero, at any phase. A metric is bounded when step2 is at most `bound` bytes per turn (the claim: what the core keeps does
 * not grow with the turns it has run). `atPoint` keeps the single samples at the points.
 */
export function inductionStep(samples, { n, warmup, bound, metrics = METRICS }) {
	const at = turn => samples.find(s => s.turn === turn - 1)
	const points = [warmup + n, warmup + 2 * n, warmup + 4 * n]
	const out = { n, warmup, points, metrics: {} }
	const picked = points.map(at)
	// A run that stopped early (a trap at the memory cap) has no induction step; its growth is measured over what it ran.
	if (picked.some(p => !p)) {
		out.incomplete = `the run stopped after ${samples.length} turns; the induction needs ${points.at(-1)}`
		const from = samples.find(s => s.turn === warmup - 1) ?? samples[0], last = samples.at(-1)
		for (const metric of metrics) if (from && typeof last?.[metric] === "number" && typeof from[metric] === "number") out.metrics[metric] = { at: [from[metric], last[metric]], turns: [from.turn + 1, last.turn + 1], slope: slope(samples.filter(s => s.turn >= from.turn && typeof s[metric] === "number").map(s => [s.turn, s[metric]])), max: Math.max(...samples.map(s => s[metric] ?? 0)), bounded: false }
		return out
	}
	// Interval ends as sample turns (turn index = turn number - 1): (W-1, W+N-1], (W+N-1, W+2N-1], (W+2N-1, W+4N-1].
	const ends = [warmup - 1, ...points.map(p => p - 1)]
	for (const metric of metrics) {
		if (picked.some(p => typeof p[metric] !== "number")) continue
		const [a, b, c] = [0, 1, 2].map(i => Math.max(...samples.filter(s => s.turn > ends[i] && s.turn <= ends[i + 1] && typeof s[metric] === "number").map(s => s[metric])))
		const window = samples.filter(s => s.turn >= points[0] - 1 && s.turn <= points[2] - 1 && typeof s[metric] === "number").map(s => [s.turn, s[metric]])
		const r = { at: [a, b, c], atPoint: picked.map(p => p[metric]), step1: (b - a) / n, step2: (c - b) / (2 * n), slope: slope(window), max: Math.max(...window.map(p => p[1])) }
		r.bounded = r.step2 <= bound
		out.metrics[metric] = r
	}
	return out
}

/**
 * Run the memory induction for one build: one Harness on an empty store runs warmup + 4N measured turns of `tools` lookups
 * (the bench scenario program with CONTRACT_MEM_HEAP=1: after every turn a full collection in each core and in the host).
 * The statement trace is not written (a 2,000-turn trace is gigabytes); the run's store is kept for inspection.
 */
export async function induction({ impl, n = 500, warmup = 50, tools = 8, variant = "incr", out, bound = 0, timeoutMs = 4 * 3600_000 }) {
	mkdirSync(out, { recursive: true })
	const run = await benchRunner(impl)
	const turns = warmup + 4 * n
	const t0 = Date.now()
	const r = await run({ out, name: `induction-${impl}`, turns, tools, variant, env: { CONTRACT_MEM_HEAP: "1", CONTRACT_TRACE: "/dev/null" }, timeoutMs })
	const ms = Date.now() - t0
	const samples = readSamples(r.mem)
	const stopped = r.code === 0 ? undefined : `exited ${r.code} ${r.signal ?? ""} after ${samples.length} turns: ${r.stderr.slice(-800)}`
	return { impl, turns, tools, variant, ms, mem: r.mem, ...(stopped ? { stopped } : {}), ...inductionStep(samples, { n, warmup, bound }) }
}

/**
 * The soak: `objects` Harnesses in one process (bench.mjs --objects: the Durable Objects of one isolate) on empty stores run
 * `rounds` rounds, each one turn of `tools` lookups on every object, with compaction on and a `window`-token model (at the
 * default 100,000 each conversation compacts about every 50 turns, so the context the model is sent stays bounded while the
 * stored history grows). Every `every` rounds the cores and the host run a full collection and record one memory sample. The
 * judge is the induction step over those samples with W = `warmup` rounds and N = (rounds - W) / 4; `perTurn` divides each
 * metric's steps by `objects` (bytes per object-turn), and a metric is bounded when its 2N->4N step per object-turn is at most
 * `bound`.
 */
export async function soak({ impl, objects = 4, rounds = 5000, warmup = 1000, every = 25, tools = 8, window = 100000, out, bound = 0, timeoutMs = 8 * 3600_000 }) {
	const n = (rounds - warmup) / 4
	if (!Number.isInteger(n) || n <= 0 || warmup % every || n % every) throw new Error(`soak: rounds - warmup (${rounds} - ${warmup}) must be 4N with N and warmup multiples of every (${every})`)
	mkdirSync(out, { recursive: true })
	const run = await benchRunner(impl)
	const t0 = Date.now()
	const r = await run({ out, name: `soak-${impl}`, turns: rounds, tools, variant: "incr", objects, compaction: true, window, env: { CONTRACT_MEM_HEAP: "1", CONTRACT_MEM_EVERY: String(every), CONTRACT_TRACE: "/dev/null" }, timeoutMs })
	const ms = Date.now() - t0
	const samples = readSamples(r.mem)
	const stopped = r.code === 0 ? undefined : `exited ${r.code} ${r.signal ?? ""} after ${samples.at(-1)?.turn + 1 || 0} rounds (the last sample): ${r.stderr.match(/FatalError: [^\n]*/)?.[0] ?? r.stderr.slice(-800)}`
	const step = inductionStep(samples, { n, warmup, bound: bound * objects })
	if (step.incomplete) step.incomplete = `the run stopped after ${samples.at(-1)?.turn + 1 || 0} rounds (the last sample); the soak needs ${rounds}`
	for (const m of Object.values(step.metrics)) m.perTurn = { step1: m.step1 === undefined ? undefined : m.step1 / objects, step2: m.step2 === undefined ? undefined : m.step2 / objects, slope: m.slope / objects }
	return { impl, objects, rounds, turns: rounds * objects, tools, window, every, ms, mem: r.mem, stores: r.stores, ...(stopped ? { stopped } : {}), ...step }
}

const TIME_TRAVEL_SCRIPT = join(CONTRACT_DIR, "scenarios", "time-travel.mjs")

/**
 * Time travel on a `size`-turn W1 history (scenarios/time-travel.mjs): locate turn h<atTurn>, an as-of context there,
 * snapshotAsOf of the agent document, `scrub` as-of contexts spread over the conversation, a fork at h<atTurn> with one turn on
 * it. Pi runs it first; each candidate's run must equal Pi's at every level (commits, model contexts, printed lines, store).
 * Returns per build its verdict and the wall time of each operation (ms; the real clock, under the contract hooks).
 */
export async function timeTravel({ impls, size = 3500, atTurn = 100, scrub = 8, out }) {
	mkdirSync(out, { recursive: true })
	const fixture = await (await import("../cli/fixtures.mjs")).seed(size)
	const result = { size, atTurn, scrub, runs: {} }
	let ref
	for (const name of ["pi", ...impls]) {
		const runner = await programRunner(name)
		const dir = join(out, runner.label)
		rmSync(dir, { recursive: true, force: true })
		mkdirSync(dir, { recursive: true })
		const store = join(dir, "store.sqlite")
		copyStore(fixture, store)
		const timings = join(dir, "timings.jsonl")
		const r = await runner.run({ script: TIME_TRAVEL_SCRIPT, out: dir, name: "time-travel", scenario: "time-travel", tmpKey: `robust-tt-${process.pid}-${runner.label}`, args: ["--db", store, "--at-turn", String(atTurn), "--scrub", String(scrub), "--timings", timings] })
		const ops = existsSync(timings) ? readFileSync(timings, "utf8").trim().split("\n").filter(Boolean).map(l => JSON.parse(l)) : []
		const row = result.runs[runner.label] = { ok: r.code === 0, ops }
		if (r.code !== 0) { row.problem = `exited ${r.code} ${r.signal ?? ""}: ${r.stderr.match(/FatalError: [^\n]*/)?.[0] ?? r.stderr.slice(-600)}`; if (name === "pi") throw new Error(`time-travel: the reference failed: ${row.problem}`); continue }
		if (name === "pi") { ref = r; continue }
		const same = diffTraces(readTrace(ref.trace), readTrace(r.trace), { storesA: ref.stores, storesB: r.stores })
		if (!same.equal) Object.assign(row, { ok: false, problem: "differs from pi", reports: same.reports })
	}
	return result
}

const CRASH_SCRIPT = join(CONTRACT_DIR, "scenarios", "crash-conversation.mjs")

/** The scenario program runner for `impl` (pi or a candidate facade): one process, optionally killed after commit `crashAfter`. */
async function programRunner(implName) {
	if (implName === "pi") return { label: "pi", run: o => runScenario({ ...o, impl: "pi-durable" }) }
	const impl = await resolveImpl(implName)
	if (!impl.candidateDir) throw new Error(`${implName} cannot run through its facade: ${impl.why ?? "no candidate_dir"}`)
	return { label: implName, run: o => runScenario({ ...o, impl: implName, candidate: impl.candidateDir, env: { ...impl.env, ...o.env } }) }
}

/** "table: the first differing column's JSON path" per table of a store difference. */
const differing = d => d?.tables.map(t => `${t.table}${t.first ? `:${t.first.columns.map(c => c.path ?? c.column).join("|")}` : t.rows ? ` rows ${t.rows.join("/")}` : ""}`) ?? []

/** Compare two store files; a checkpointed copy of each, so a crashed run's WAL is part of what is compared. */
export function storesDiffer(a, b) {
	const [pa, ca] = settled(a), [pb, cb] = settled(b)
	try { return diffStores(pa, pb) } finally { ca(); cb() }
}

/**
 * What a client can count on after any crash: per submission request id, one submission row with the status the
 * uninterrupted run left, and per conversation, each user input in the transcript exactly once. Conversations are named by
 * their role (root, fork, child) and a sub-agent's request id by its prefix, because a restart may mint different ids.
 */
export function ledger(path) {
	const [p, cleanup] = settled(path)
	try {
		const db = new DatabaseSync(p, { readOnly: true })
		const subs = db.prepare("SELECT record FROM submissions ORDER BY id").all().map(r => JSON.parse(r.record))
		const entries = db.prepare("SELECT record FROM entries ORDER BY id").all().map(r => JSON.parse(r.record))
		const convs = db.prepare("SELECT record FROM conversations ORDER BY id").all().map(r => JSON.parse(r.record))
		db.close()
		const role = new Map(convs.map(c => [c.id, c.parent ? "fork" : c.owner ? "child" : "root"]))
		// A compaction places its summary with request id compaction:<task id>: the harness's own submission, which the
		// final-store comparison with the control covers; the ledger is the client's (the program's and the sub-agent's).
		const requests = new Map()
		for (const s of subs) {
			if (s.requestId === undefined || s.requestId.startsWith("compaction:")) continue
			const key = `${role.get(s.conversationId)}/${s.requestId.replace(/^subagent:.*/, "subagent")}`
			requests.set(key, [...(requests.get(key) ?? []), s.status])
		}
		const inputs = new Map()
		for (const e of entries) {
			if (e.kind !== "pi.user") continue
			const text = e.model?.map(m => (typeof m.content === "string" ? m.content : m.content.map(c => c.text ?? "").join(""))).join("")
			const key = `${role.get(e.conversationId)}/${text}`
			inputs.set(key, (inputs.get(key) ?? 0) + 1)
		}
		return { requests: Object.fromEntries([...requests].sort()), inputs: Object.fromEntries([...inputs].sort()) }
	} finally { cleanup() }
}

/** The problems of a crashed-and-restarted store's ledger against the uninterrupted run's. */
export function ledgerProblems(got, want) {
	const problems = []
	for (const [id, statuses] of Object.entries(want.requests)) {
		const g = got.requests[id]
		if (!g) problems.push(`submission ${id} lost`)
		else if (g.length !== statuses.length) problems.push(`submission ${id}: ${g.length} rows (${g.join(", ")}), uninterrupted ${statuses.length}`)
		else if (g.join() !== statuses.join()) problems.push(`submission ${id} ${g.join(", ")}, uninterrupted ${statuses.join(", ")}`)
	}
	for (const id of Object.keys(got.requests)) if (!want.requests[id]) problems.push(`submission ${id} not in the uninterrupted run`)
	for (const [key, n] of Object.entries(want.inputs)) if ((got.inputs[key] ?? 0) !== n) problems.push(`input ${JSON.stringify(key)} ${got.inputs[key] ?? 0} times, uninterrupted ${n}`)
	for (const key of Object.keys(got.inputs)) if (!(key in want.inputs)) problems.push(`input ${JSON.stringify(key)} not in the uninterrupted run`)
	return problems
}

/**
 * The exhaustive crash proof for candidate builds. The uninterrupted reference run lists the commits (crash points); each
 * candidate's uninterrupted run must equal it at every level. Then for every crash point k:
 *   control   pi killed after commit k, then pi restarted with the same program until it finishes
 *   X>X       the candidate killed after commit k, then the candidate restarted
 * The candidate's store at the crash must equal the control's, its final store must equal the control's, and its restart's
 * commits, contexts and output must equal the control's restart. The final store's ledger must equal the uninterrupted
 * run's (no lost or duplicated submission or input). Each final store is also compared with the uninterrupted store byte
 * for byte and the answer recorded: the control's own answer is the reference behaviour (a restarted process's clock starts
 * again, so timestamps differ), and a candidate that equals the control matches it.
 * `onPoint` gets each crash point's rows as they finish.
 */
export async function crash({ impls, out, points, concurrency = 1, keep = false, onPoint }) {
	mkdirSync(out, { recursive: true })
	const pi = await programRunner("pi")
	const cands = await Promise.all(impls.map(programRunner))
	let unique = 0
	const run = (runner, dir, name, store, crashAfter) => runner.run({ script: CRASH_SCRIPT, out: join(out, dir), name, scenario: "crash-conversation", tmpKey: `robust-crash-${process.pid}-${unique++}`, args: ["--db", store ?? join(out, dir, "new.sqlite")], crashAfter })
	const refU = await run(pi, "uninterrupted", "pi")
	if (refU.code !== 0) throw new Error(`the reference's uninterrupted run failed: ${refU.stderr.slice(-800)}`)
	const all = readTrace(refU.trace).filter(l => l.t === "commit" && l.seq !== undefined).map(l => l.seq)
	const want = ledger(refU.stores[0])
	const result = { impls, commits: all.length, ledger: want, uninterrupted: {}, points: [], failed: {} }
	const candU = {}
	for (const cand of cands) {
		const u = candU[cand.label] = await run(cand, "uninterrupted", cand.label)
		result.failed[cand.label] = 0
		if (u.code !== 0) { result.uninterrupted[cand.label] = { ok: false, problem: `exited ${u.code} ${u.signal ?? ""}: ${u.stderr.slice(-600)}` }; result.failed[cand.label]++; continue }
		const same = diffTraces(readTrace(refU.trace), readTrace(u.trace), { storesA: refU.stores, storesB: u.stores })
		result.uninterrupted[cand.label] = same.equal ? { ok: true } : { ok: false, reports: same.reports }
		if (!same.equal) result.failed[cand.label]++
	}
	const queue = [...(points ?? all)]
	async function one(k) {
		const dir = `k${k}`
		rmSync(join(out, dir), { recursive: true, force: true })
		const rows = []
		const c1 = await run(pi, dir, "c1", undefined, k)
		if (c1.signal !== "SIGKILL") return [{ k, impl: "pi", ok: false, problem: `the reference was not killed after commit ${k} (exit ${c1.code}): not a commit of the scenario` }]
		const c2 = c1.stores[0] && await run(pi, dir, "c2", c1.stores[0])
		if (!c2 || c2.code !== 0) return [{ k, impl: "pi", ok: false, problem: `control: ${c1.signal ?? c1.code} then ${c2?.code}: ${(c2?.stderr ?? c1.stderr).slice(-400)}` }]
		const control = { k, impl: "pi", ok: true }
		const controlLost = ledgerProblems(ledger(c2.stores[0]), want)
		if (controlLost.length) Object.assign(control, { ok: false, problem: "the reference lost or duplicated submissions", report: controlLost })
		const pDiff = storesDiffer(refU.stores[0], c2.stores[0])
		control.identical = !pDiff
		control.tablesDiffering = differing(pDiff)
		rows.push(control)
		for (const cand of cands) {
			const row = { k, impl: cand.label, ok: false }
			rows.push(row)
			const x1 = await run(cand, dir, `${cand.label}-1`, undefined, k)
			if (x1.signal !== "SIGKILL" || !x1.stores[0]) { row.problem = `run 1 was not killed after commit ${k} (exit ${x1.code} ${x1.signal ?? ""}): ${x1.stderr.slice(-400)}`; continue }
			const x2 = await run(cand, dir, `${cand.label}-2`, x1.stores[0])
			if (x2.code !== 0) { row.problem = `the restart exited ${x2.code} ${x2.signal ?? ""}: ${x2.stderr.slice(-600)}`; continue }
			const lost = ledgerProblems(ledger(x2.stores[0]), want)
			if (lost.length) { row.problem = "submissions lost or duplicated"; row.report = lost; continue }
			const atCrash = storesDiffer(c1.stores[0], x1.stores[0])
			if (atCrash) { row.problem = "the store at the crash differs from pi's"; row.report = atCrash; continue }
			const final = storesDiffer(c2.stores[0], x2.stores[0])
			if (final) { row.problem = "the final store differs from pi's after the same crash"; row.report = final; continue }
			const d = diffTraces(readTrace(c2.trace), readTrace(x2.trace), { levels: ["commit", "context", "output"] })
			if (!d.equal) { row.problem = "the restart differs from pi's restart"; row.report = d.reports; continue }
			const xDiff = storesDiffer(candU[cand.label].stores[0], x2.stores[0])
			row.identical = !xDiff
			row.tablesDiffering = differing(xDiff)
			row.ok = true
		}
		if (!keep && rows.every(r => r.ok)) rmSync(join(out, dir), { recursive: true, force: true })
		return rows
	}
	await Promise.all(Array.from({ length: Math.min(concurrency, queue.length) }, async () => {
		for (let k = queue.shift(); k !== undefined; k = queue.shift()) {
			const rows = await one(k)
			for (const row of rows) { if (!row.ok) result.failed[row.impl] = (result.failed[row.impl] ?? 0) + 1; result.points.push(row) }
			onPoint?.(rows)
		}
	}))
	result.points.sort((a, b) => a.k - b.k || a.impl.localeCompare(b.impl))
	return result
}

/**
 * The crash proof's in-process twin: for every commit of the scenario, the candidate's core traps right after that commit and the
 * host recycles its instance, reopens from SQLite and carries on, in the same process and with the same program (no kill, no
 * restart, so no resubmission). What a client can count on is what `crash` checks of a restart: the run finishes, no submission is
 * lost or duplicated (`ledger`), and the output is one the reference itself produces for that interruption: the uninterrupted
 * run's, or that of the reference killed after the same commit and restarted (a restart replays the program, so where the work
 * that was in flight lands can differ from the uninterrupted run, in Pi as in a recycle). The final store is compared with both
 * and the tables that differ are reported: the clock does not restart in process, so timestamps differ from a restarted run's.
 */
export async function recycle({ impls, out, points, concurrency = 1, keep = false, onPoint }) {
	mkdirSync(out, { recursive: true })
	const pi = await programRunner("pi")
	const cands = await Promise.all(impls.map(programRunner))
	let unique = 0
	const run = (runner, dir, name, { store, crashAfter, env } = {}) => runner.run({ script: CRASH_SCRIPT, out: join(out, dir), name, scenario: "crash-conversation", tmpKey: `robust-recycle-${process.pid}-${unique++}`, args: ["--db", store ?? join(out, dir, "new.sqlite"), "--no-cache"], crashAfter, env })
	const refU = await run(pi, "uninterrupted", "pi")
	if (refU.code !== 0) throw new Error(`the reference's uninterrupted run failed: ${refU.stderr.slice(-800)}`)
	const all = readTrace(refU.trace).filter(l => l.t === "commit" && l.seq !== undefined).map(l => l.seq)
	const want = ledger(refU.stores[0])
	const result = { impls, commits: all.length, ledger: want, uninterrupted: {}, points: [], failed: {} }
	const candU = {}
	for (const cand of cands) {
		const u = candU[cand.label] = await run(cand, "uninterrupted", cand.label)
		result.failed[cand.label] = 0
		if (u.code !== 0) { result.uninterrupted[cand.label] = { ok: false, problem: `exited ${u.code} ${u.signal ?? ""}: ${u.stderr.slice(-600)}` }; result.failed[cand.label]++; continue }
		const same = diffTraces(readTrace(refU.trace), readTrace(u.trace), { storesA: refU.stores, storesB: u.stores })
		result.uninterrupted[cand.label] = same.equal ? { ok: true } : { ok: false, reports: same.reports }
		if (!same.equal) result.failed[cand.label]++
	}
	const queue = [...(points ?? all)]
	async function one(k) {
		const dir = `r${k}`
		rmSync(join(out, dir), { recursive: true, force: true })
		const rows = []
		const c1 = await run(pi, dir, "c1", { crashAfter: k })
		if (c1.signal !== "SIGKILL") return [{ k, impl: "pi", ok: false, problem: `the reference was not killed after commit ${k} (exit ${c1.code}): not a commit of the scenario` }]
		const c2 = c1.stores[0] && await run(pi, dir, "c2", { store: c1.stores[0] })
		if (!c2 || c2.code !== 0) return [{ k, impl: "pi", ok: false, problem: `control: ${c1.signal ?? c1.code} then ${c2?.code}: ${(c2?.stderr ?? c1.stderr).slice(-400)}` }]
		for (const cand of cands) {
			const row = { k, impl: cand.label, ok: false }
			rows.push(row)
			const x = await run(cand, dir, `${cand.label}-recycled`, { env: { CONTRACT_RECYCLE_AFTER: String(k) } })
			if (x.code !== 0) { row.problem = `the run exited ${x.code} ${x.signal ?? ""}: ${x.stderr.slice(-600)}`; continue }
			const injected = (x.stderr.match(/\[contract\] recycle injected/g) ?? []).length
			if (injected !== 1) { row.problem = `${injected} traps were injected after commit ${k}, not one: not a commit of the scenario`; continue }
			const lost = ledgerProblems(ledger(x.stores[0]), want)
			if (lost.length) { row.problem = "submissions lost or duplicated"; row.report = lost; continue }
			const dCrash = diffTraces(readTrace(c2.trace), readTrace(x.trace), { levels: ["output"] })
			const dUninterrupted = diffTraces(readTrace(candU[cand.label].trace), readTrace(x.trace), { levels: ["output"] })
			if (!dCrash.equal && !dUninterrupted.equal) { row.problem = "the output is neither the uninterrupted run's nor the reference's after the same crash"; row.report = { afterCrash: dCrash.reports, uninterrupted: dUninterrupted.reports }; continue }
			row.outputEquals = [dUninterrupted.equal ? "uninterrupted" : undefined, dCrash.equal ? "pi-crash" : undefined].filter(Boolean)
			const xDiff = storesDiffer(candU[cand.label].stores[0], x.stores[0])
			const piDiff = storesDiffer(c2.stores[0], x.stores[0])
			row.identical = !xDiff
			row.tablesDiffering = differing(xDiff)
			row.equalsPiCrash = !piDiff
			row.tablesDifferingFromPiCrash = differing(piDiff)
			row.ok = true
		}
		if (!keep && rows.every(r => r.ok)) rmSync(join(out, dir), { recursive: true, force: true })
		return rows
	}
	await Promise.all(Array.from({ length: Math.min(concurrency, queue.length) }, async () => {
		for (let k = queue.shift(); k !== undefined; k = queue.shift()) {
			const rows = await one(k)
			for (const row of rows) { if (!row.ok) result.failed[row.impl] = (result.failed[row.impl] ?? 0) + 1; result.points.push(row) }
			onPoint?.(rows)
		}
	}))
	result.points.sort((a, b) => a.k - b.k || a.impl.localeCompare(b.impl))
	return result
}
