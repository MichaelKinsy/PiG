// The gate (CONTRACT sections 5-7): run corpus rows against an implementation, compare each with the reference capture, and
// report pass, fail, pending (the implementation cannot run the row yet) or skipped (nobody can, with the reason) per row and
// per owner lane. `pi` as the implementation is the control: the reference against itself, a determinism gate. A reference
// that does not run is a failed row, never a skipped one: a broken checkout or a timed-out reference must not turn the gate green.
import { killGroup, spawnGroup } from "./procs.mjs"
import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"
import { seed, fixturePath } from "../cli/fixtures.mjs"
import { driftedSources, loadCorpus, loadImpls, REPO, validateCorpus } from "./corpus.mjs"
import { IMPLS, facadeBench, outsideBenchSlice, registerImpl, runBench } from "./impl.mjs"
import { compareInterleavings, tokens } from "./interleave.mjs"
import { boundApplies, memoryDescribe, memorySummary, overBound, readSamples } from "./memory.mjs"
import { matrix, sample } from "./matrix.mjs"
import { diffTraces, readTrace } from "./rowdiff.mjs"
import { copyStore } from "./store.mjs"
import { CONTRACT_DIR, piSource, runScenario } from "./run.mjs"

const once = new Map()
/** Memoise an async computation by key, so concurrent rows share one reference capture. */
const memo = (key, fn) => { if (!once.has(key)) once.set(key, fn()); return once.get(key) }

/** Resolve an implementation name to what it can run. */
export async function resolveImpl(name) {
	if (name === "pi") return { name, control: true, examples: true, vitest: true, bench: true, native: false }
	// The vendored TypeScript control (durable-core-ts): the bench slice only, so it runs the W and H rows and nothing else.
	if (name === "ts") return { name, slice: "bench", bench: true, spec: { bench: "impls/ts-driver.mjs" } }
	const spec = loadImpls().find(i => i.name === name)
	if (!spec) throw new Error(`unknown implementation ${name}`)
	const dir = spec.candidate_dir && resolve(REPO, spec.candidate_dir)
	const bench = spec.bench && resolve(REPO, spec.bench)
	const exe = spec.native_exe && resolve(REPO, spec.native_exe)
	// `wasm`: the core module the facade runs. Without it the candidate is a facade with no core, which fails rows instead of leaving them pending.
	const wasm = spec.wasm && resolve(REPO, spec.wasm)
	const setup = spec.setup && resolve(REPO, spec.setup)
	const nativeCore = spec.native_core && resolve(REPO, spec.native_core)
	const why = wasm && !existsSync(wasm) ? `has no core module yet (${spec.wasm} is not built)` : nativeCore && !existsSync(nativeCore) ? `has no native core yet (${spec.native_core} is not built)` : setup && !existsSync(setup) ? `has no start-up module yet (${spec.setup} loads the core into the facade)` : undefined
	const facade = Boolean(dir && existsSync(dir)) && !why
	const scExe = spec.sc_exe && resolve(REPO, spec.sc_exe)
	const impl = { name, spec, why, scExe: scExe && existsSync(scExe) ? scExe : undefined, candidateDir: facade ? dir : undefined, env: { ...(wasm ? { DCORE_WASM: wasm } : {}), ...(nativeCore ? { DCORE_NATIVE: nativeCore } : {}), ...(setup ? { CONTRACT_CANDIDATE_SETUP: setup } : {}), ...spec.env }, examples: facade, vitest: facade, bench: facade || Boolean((bench && existsSync(bench)) || (exe && existsSync(exe))) }
	if (impl.bench && !IMPLS[name]) {
		if (exe) { const { runNative } = await import("../impls/native.mjs"); registerImpl(name, { label: name, contexts: ["bench"], ...(spec.slice === "bench" ? { slice: "bench", unsupported: outsideBenchSlice } : {}), runBench: o => runNative({ exe, o }) }) }
		else if (bench) registerImpl(name, { label: name, ...(await import(pathToFileURL(bench).href)) })
		else registerImpl(name, { label: name, runBench: facadeBench({ dir, env: impl.env }) })
	}
	return impl
}

/** Run one build command line; returns a note for the report and throws when the build fails. */
async function runBuild(line) {
	const [cmd, ...args] = line.split(" ")
	const child = spawnGroup(cmd, args, { cwd: REPO, stdio: ["ignore", "ignore", "pipe"] })
	let err = ""
	child.stderr.on("data", d => (err += d))
	const code = await new Promise(res => child.on("error", () => res(127)).on("close", res))
	if (code !== 0) throw new Error(`build \`${line}\` failed (${code}): ${err.trim().split("\n").slice(-2).join(" | ")}`)
	return `built with \`${line}\``
}

/**
 * Build the candidate: `build` for the core, `sc_build` for the SC provider (sc_exe). The build commands are incremental, and a
 * stale output would report an old core's verdicts as the current one's, so they run every time (CONTRACT_NO_BUILD=1 skips). A failed
 * build throws: running the rows on an earlier build's outputs would report that build's verdicts.
 */
export async function ensureBuilt(name) {
	const spec = loadImpls().find(i => i.name === name)
	if (!spec) return undefined
	if (process.env.CONTRACT_NO_BUILD) return undefined
	const notes = []
	if (spec.build) notes.push(await runBuild(spec.build))
	if (spec.sc_build && spec.sc_build !== spec.build) notes.push(await runBuild(spec.sc_build))
	return notes.length ? notes.join("; ") : undefined
}

/** Why a candidate cannot run a row yet: a missing build output or path, named. */
const lacks = impl => `${impl.name} ${impl.why ?? `has nothing to run yet (${impl.spec?.candidate_dir ?? impl.spec?.bench ?? impl.spec?.native_exe})`}`

const pending = (why) => ({ status: "pending", detail: why })
/** A pending row that waits for the owner's API keys or cassettes (reason "pending-owner: ...") does not fail --strict: nobody else can run it. */
export const blocking = r => r.status === "pending" && !/^pending-owner\b/.test(String(r.detail ?? ""))
const failed = (detail) => ({ status: "fail", detail })
const passed = (detail) => ({ status: "pass", detail })

function levelsOf(row) { return (row.levels ?? []).filter(l => ["store", "commit", "context", "output"].includes(l)) }
const referenceFailed = (what, r) => failed({ problem: `the reference ${what} did not run (code ${r.code}${r.signal ? `, ${r.signal}` : ""})`, stderr: r.stderr.slice(0, 600) })

/** E rows and the interop row: a scenario program, reference against implementation. */
export async function runProgramRow(row, impl, ctx) {
	if (!impl.control && !impl.examples) return pending(lacks(impl))
	const script = row.runner === "example" ? join(piSource(), row.source) : join(REPO, row.source)
	const args = row.runner === "interop" ? [join(ctx.out, "interop-store.sqlite")] : row.runner === "diff" ? ["--seed", String(row.seed), ...(row.args ?? [])] : []
	// A diff row that names a fixture (a W1 history) gets its own copy of that store in every run's output directory, as `--db`: the script writes to it. The copy is named for the row: rows run concurrently under one output directory.
	const withFixture = async dir => {
		if (!row.fixture) return args
		const match = /^(.*)-(\d+)$/.exec(row.fixture)
		const fixture = await memo(`fixture:${row.fixture}`, () => seed(Number(match[2]), { kind: match[1] }))
		return [...args, "--db", copyStore(fixture, join(dir, `${row.id}-store.sqlite`))]
	}
	// A diff row names the storage backend's hop profile (S7): `node` runs the reference's own Node SQLite adapter, for Pi and the candidate alike.
	const rowEnv = row.profile === "node" ? { CONTRACT_STORAGE_PROFILE: "node" } : {}
	const common = { script, scenario: row.id, tmpKey: row.id }
	const ref = await memo(`ref:${row.id}`, async () => runScenario({ ...common, args: await withFixture(join(ctx.out, "ref")), out: join(ctx.out, "ref"), name: row.id, env: rowEnv }))
	if (ref.code !== 0) return referenceFailed("scenario", ref)
	const candidate = { impl: impl.name, candidate: impl.control ? undefined : impl.candidateDir }
	const cand = await runScenario({ ...common, args: await withFixture(join(ctx.out, impl.name)), ...candidate, out: join(ctx.out, impl.name), name: row.id, env: { ...(impl.control ? {} : impl.env), ...rowEnv } })
	if (cand.code !== 0) return failed({ problem: "the candidate run failed", code: cand.code, signal: cand.signal, stderr: cand.stderr.slice(0, 600) })
	const d = diffTraces(readTrace(ref.trace), readTrace(cand.trace), { levels: levelsOf(row), storesA: ref.stores, storesB: cand.stores })
	if (!d.equal) return failed(d.reports)
	// The order level (CONTRACT 2.1, 5.8): the scenario's API calls and the commits in one sequence, exactly as Pi orders them. The
	// API trace marks a traced promise's rejection as handled, so it runs in a pair of its own and the runs above keep the untraced
	// exit status and output.
	if (row.levels?.includes("order")) {
		const traced = { ...rowEnv, CONTRACT_API_TRACE: "1" }
		const refOrder = await memo(`ref-order:${row.id}`, async () => runScenario({ ...common, args: await withFixture(join(ctx.out, "ref-order")), out: join(ctx.out, "ref-order"), name: row.id, env: traced }))
		if (refOrder.code !== 0) return referenceFailed("scenario with the API trace", refOrder)
		const candOrder = await runScenario({ ...common, args: await withFixture(join(ctx.out, impl.name, "order")), ...candidate, out: join(ctx.out, impl.name, "order"), name: row.id, env: { ...(impl.control ? {} : impl.env), ...traced } })
		if (candOrder.code !== 0) return failed({ level: "order", problem: "the candidate run with the API trace failed", code: candOrder.code, signal: candOrder.signal, stderr: candOrder.stderr.slice(0, 600) })
		const order = compareInterleavings(tokens(readTrace(refOrder.trace)), tokens(readTrace(candOrder.trace)))
		if (order) return failed([{ level: "order", ...order }])
	}
	return passed(`${readTrace(cand.trace).filter(l => l.t === "commit").length} commits`)
}

/** The longest one vitest run may take before it is killed as hung (a spinning core never lets vitest time out). */
const VITEST_TIMEOUT_MS = Number(process.env.CONTRACT_VITEST_TIMEOUT_MS ?? 600_000)

/**
 * One vitest run: `include` is a durable-relative test file or the SC suite (SC with an sc_exe runs the reference's modules
 * unchanged: the provider brings the candidate's write encoder). The result maps test files to their statuses; `timedOut` is set
 * when the run was killed.
 */
export async function vitestRun(impl, ctx, include, label = "sc", isSc = label === "sc", timeoutMs = VITEST_TIMEOUT_MS) {
	const outFile = join(ctx.out, impl.name, `vitest-${label}.json`)
	mkdirSync(join(ctx.out, impl.name), { recursive: true })
	rmSync(outFile, { force: true })
	const sc = isSc && impl.scExe
	const env = { ...process.env, CONTRACT_PI_SOURCE: piSource(), ...(include ? { CONTRACT_TEST_INCLUDE: include } : {}), ...(impl.candidateDir && !sc ? { CONTRACT_CANDIDATE: impl.candidateDir, ...impl.env } : {}), ...(sc ? { CONTRACT_SC_EXE: impl.scExe, CONTRACT_SC_PROVIDER: join(CONTRACT_DIR, "impls", "sc-native-provider.ts") } : {}) }
	const bin = join(piSource(), "node_modules", ".bin", "vitest")
	const child = spawnGroup(bin, ["run", "--config", join(CONTRACT_DIR, "scenarios", "vitest.contract.config.mjs"), "--reporter=json", `--outputFile=${outFile}`], { cwd: join(piSource(), "packages", "durable"), env, stdio: ["ignore", "ignore", "pipe"] })
	let err = ""
	child.stderr.on("data", d => (err += d))
	let timedOut = false
	const timer = setTimeout(() => { timedOut = true; killGroup(child) }, timeoutMs)
	await new Promise(res => child.on("close", res))
	clearTimeout(timer)
	const files = new Map()
	files.timedOut = timedOut
	// A native core that outran its bound (durable/core/contracttest/guard) says so on stderr; the row fails with that cause.
	files.guard = [...err.matchAll(/^.*: guard: .*$/gm)].map(m => m[0].slice(0, 300))
	if (timedOut) return files
	if (!existsSync(outFile)) throw new Error(`vitest did not write a report: ${err.slice(0, 500)}`)
	const report = JSON.parse(readFileSync(outFile, "utf8"))
	for (const f of report.testResults) files.set(f.name.split("/test/").pop(), { status: f.status, message: f.message, tests: new Map(f.assertionResults.map(a => [a.fullName, a.status])), failures: new Map(f.assertionResults.filter(a => a.failureMessages?.length).map(a => [a.fullName, `after ${Math.round(a.duration ?? 0)} ms: ${a.failureMessages.join("\n")}`])) })
	return files
}
/**
 * U rows: one file per row (a hung file must not take the other rows with it), the reference's run of the file started together
 * with the implementation's. Several of the reference's tests have a 5 s budget that host load alone can exceed (reopening a
 * SQLite file under a busy gate: 171 ms alone, 2.1 s at -j 8, 9.2 s with the suite on one CPU, where the reference fails them
 * itself). A reference run made once, at another moment, judged the candidate's run against a different load: under load ~600
 * the candidate failed tests the earlier reference run had passed. Run together, a test both fail under the load of the moment
 * is not counted against the candidate, and the row names it; a test only the candidate fails still fails the row.
 */
export async function runVitestRow(row, impl, ctx) {
	if (!impl.control && !impl.vitest) return pending(lacks(impl))
	const file = row.source.split("/test/").pop()
	const label = `u-${file.replace(/[^\w.-]/g, "_")}`
	const { ref, cand } = await memo(`vitest:${impl.name}:${file}`, async () => {
		const [ref, cand] = await Promise.all([vitestRun({ name: "ref" }, ctx, `test/${file}`, label, false), impl.control ? undefined : vitestRun(impl, ctx, `test/${file}`, label, false)])
		return { ref, cand: cand ?? ref }
	})
	if (ref.timedOut) return failed({ problem: `the reference did not finish ${file} within ${VITEST_TIMEOUT_MS / 1000} s` })
	if (cand.timedOut && cand.guard?.length) return failed({ problem: `the native core's guard ended a runaway request in ${file}`, guard: cand.guard.slice(0, 3) })
	if (cand.timedOut) return failed({ problem: `the candidate did not finish ${file} within ${VITEST_TIMEOUT_MS / 1000} s (a hung or spinning core)` })
	if (cand.guard?.length) return failed({ problem: `the native core's guard ended a runaway request in ${file}`, guard: cand.guard.slice(0, 3) })
	const r = ref.get(file), c = cand.get(file)
	if (!r) return failed({ problem: `the reference did not run ${file}` })
	if (!c) return failed({ problem: "the file did not run", reference: r.status })
	const bad = [...r.tests].filter(([name, st]) => st === "passed" && c.tests.get(name) !== "passed").map(([name]) => `${name}: ${c.tests.get(name) ?? "missing"}`)
	if (c.status === "failed" && !c.tests.size) return failed({ problem: "the file failed to load", message: c.message?.slice(0, 400) })
	// A test whose assertion depends on a real-time window the reference shares (corpus `real_time_tests`: a throttle interval
	// against a setTimeout of the test itself) fails in the reference too when the process stalls for longer than the window
	// (measured: both fail at an 85 ms stall of an 80 ms window, both pass at 70 ms). Such a test, and only such a test, gets one
	// rerun of its file, and the row says it was retried.
	let retried = []
	if (bad.length && row.real_time_tests?.length && bad.every(b => row.real_time_tests.some(t => b.startsWith(t)))) {
		const again = await vitestRun(impl, ctx, `test/${file}`, `u-${file.replace(/[^\w.-]/g, "_")}-retry`, false)
		const c2 = again.get(file)
		if (c2 && bad.every(b => c2.tests.get(b.slice(0, b.lastIndexOf(": "))) === "passed")) { retried = bad.map(b => b.slice(0, b.lastIndexOf(": "))); bad.length = 0 }
	}
	// The first failure's message and stack go into the row, so a failure that does not reproduce still leaves its cause.
	const detail = bad.slice(0, 2).map(b => c.failures.get(b.slice(0, b.lastIndexOf(": ")))?.slice(0, 1500)).filter(Boolean)
	if (bad.length) return failed({ problem: `${bad.length} test(s) the reference passes did not pass`, first: bad.slice(0, 5), ...(detail.length ? { detail } : {}) })
	const count = [...r.tests.values()].filter(s => s === "passed").length
	const refFailed = [...r.tests].filter(([, st]) => st === "failed").map(([name]) => name)
	const alongside = refFailed.length ? `; the reference failed ${refFailed.length} alongside: ${refFailed.slice(0, 5).join("; ")}` : ""
	if (retried.length) return passed(`${count} tests${alongside}; retried once (real-time window shared with the reference): ${retried.join("; ")}`)
	// A file whose every test the reference skips (a missing key) proves nothing about the candidate.
	return count ? passed(`${count} tests${alongside}`) : { status: "skipped", detail: `the reference passes no test of ${file} here (${r.tests.size} not passed${alongside})` }
}

/** W rows: the bench scenario on a copy of the fixture, reference against implementation. */
async function runBenchRow(row, impl, ctx) {
	if (!impl.control && !impl.bench) return pending(lacks(impl))
	const p = row.params
	const match = /^(.*)-(\d+)$/.exec(p.fixture)
	const store = await memo(`fixture:${p.fixture}`, () => seed(Number(match[2]), { kind: match[1] }))
	const options = { turns: p.turns, tools: p.tools, variant: p.variant, stream: p.stream, partialMs: p.partialMs, payloadKb: p.payloadKb, parallel: p.parallel, fault: p.fault, subagent: p.subagent, inject: p.inject, scenario: row.id }
	const outside = IMPLS[impl.name]?.unsupported?.(options)
	if (outside) return pending(`${impl.name} cannot run ${outside} (outside its slice)`)
	const turns = p.restartEachTurn ? Array.from({ length: p.turns }, (_, i) => ({ ...options, turns: 1, label: `m${i}` })) : [options]
	const run = async (name, dir) => {
		let at = store
		const runs = []
		for (const [i, o] of turns.entries()) { const r = await runBench(name, { ...o, store: at, out: join(ctx.out, dir), name: `${row.id}.${i}` }); runs.push(r); if (r.code !== 0) break; at = r.stores[0] }
		return runs
	}
	const ref = await memo(`ref:${row.id}`, () => run("pi", "ref"))
	const broken = ref.find(r => r.code !== 0)
	if (broken) return referenceFailed("bench run", broken)
	const cand = await run(impl.name, impl.name)
	const bad = cand.find(r => r.code !== 0)
	if (bad) return failed({ problem: "the candidate run failed", code: bad.code, stderr: bad.stderr.slice(0, 600) })
	let commits = 0
	// A row that restarts the process each turn has no in-process growth to measure.
	const memory = cand.length === 1 ? memorySummary(readSamples(cand[0].mem)) : undefined
	for (const [i, c] of cand.entries()) {
		const last = i === cand.length - 1
		const d = diffTraces(readTrace(ref[i].trace), readTrace(c.trace), { levels: levelsOf(row).filter(l => last || l !== "store"), storesA: last ? ref[i].stores : [], storesB: last ? c.stores : [], contexts: IMPLS[impl.name]?.contexts })
		if (!d.equal) return failed({ turn: i, reports: d.reports })
		commits += readTrace(c.trace).filter(l => l.t === "commit").length
	}
	const over = boundApplies(impl.name) ? overBound(memory) : undefined
	if (over) {
		const reference = ref.length === 1 ? memorySummary(readSamples(ref[0].mem)) : undefined
		return failed({ ...over, commits, ...(reference ? { reference: memoryDescribe(reference) } : {}) })
	}
	return passed(`${commits} commits${memory ? `; ${memoryDescribe(memory)}` : ""}`)
}

/** H rows: the crash handoff matrix. */
async function runMatrixRow(row, impl, ctx, corpus) {
	if (!impl.control && !impl.bench) return pending(lacks(impl))
	const base = corpus.scenario.find(s => s.id === row.scenario)
	const p = base.params
	const name = row.fixture ?? p.fixture
	const match = /^(.*)-(\d+)$/.exec(name)
	const fixture = await memo(`fixture:${name}`, () => seed(Number(match[2]), { kind: match[1] }))
	const scenario = { turns: p.turns && 1, tools: p.tools, variant: p.variant, stream: p.stream, partialMs: p.partialMs, parallel: p.parallel, subagent: p.subagent, scenario: row.id }
	const outside = IMPLS[impl.name]?.unsupported?.(scenario)
	if (outside) return pending(`${impl.name} cannot run ${outside} (outside its slice)`)
	const all = await memo(`points:${row.scenario}`, async () => {
		const r = await runBench("pi", { ...scenario, store: fixture, out: join(ctx.out, "points", row.scenario), name: "points" })
		return readTrace(r.trace).filter(l => l.t === "commit" && l.seq !== undefined).map(l => l.seq)
	})
	const points = row.crash_points === "all" ? all : sample(all, Number(row.crash_points))
	const r = await matrix({ fixture, scenario, candidate: impl.name, work: join(ctx.out, impl.name, row.id), points, orders: row.orders.map(o => o), concurrency: ctx.concurrency, injectAtCrash: row.inject_at_crash })
	const bad = r.rows.filter(x => !x.ok)
	return bad.length ? failed({ problem: `${bad.length} of ${r.rows.length} handoffs differ`, first: bad.slice(0, 3) }) : passed(`${r.rows.length} handoffs over ${points.length} crash points`)
}

/** SC: the upstream storage conformance suite over the candidate's Storage (reference: SqliteStorage on node:sqlite). */
export async function runScRow(row, impl, ctx) {
	if (!impl.control && !impl.vitest && !impl.scExe) return pending(lacks(impl))
	if (!impl.control && !impl.scExe && !existsSync(join(impl.candidateDir, "contract-sc-provider.ts"))) return pending(`${impl.name} has no contract-sc-provider.ts (the write encoder as a Storage) yet`)
	const include = join(CONTRACT_DIR, "scenarios", "sc.test.ts")
	const run = await vitestRun(impl.control ? { name: "sc-ref" } : impl, ctx, include, "sc", true)
	if (run.timedOut) return failed({ problem: `the suite did not finish within ${VITEST_TIMEOUT_MS / 1000} s (a hung or spinning core)` })
	const file = [...run.values()][0]
	if (!file) return failed({ problem: "the suite did not run" })
	const bad = [...file.tests].filter(([, st]) => st !== "passed")
	if (!file.tests.size) return failed({ problem: "the suite registered no tests", message: file.message?.slice(0, 400) })
	return bad.length ? failed({ problem: `${bad.length} of ${file.tests.size} cases failed`, first: bad.slice(0, 5).map(([n]) => n) }) : passed(`${file.tests.size} cases`)
}

const RUNNERS = { example: runProgramRow, interop: runProgramRow, diff: runProgramRow, vitest: runVitestRow, "storage-conformance": runScRow, bench: runBenchRow, matrix: runMatrixRow }

/** Run the selected rows against one implementation; returns result rows. */
export async function gate({ impl: implName, ids, lanes, out, concurrency = 8, onResult, rows: given }) {
	const corpus = loadCorpus()
	// An invalid manifest or a reference checkout that is not the pin's would make every verdict meaningless.
	const problems = [...validateCorpus(corpus), ...driftedSources(corpus).map(d => `${d.id}: source ${d.source} ${d.problem}`)]
	if (problems.length) throw new Error(`the corpus is not runnable:\n  ${problems.join("\n  ")}`)
	const impl = await resolveImpl(implName)
	mkdirSync(out, { recursive: true })
	const ctx = { out, concurrency: Math.max(1, Math.floor(concurrency / 2)) }
	const rows = given ?? corpus.scenario.filter(r => (!ids || ids.some(id => r.id === id || (id.endsWith("*") && r.id.startsWith(id.slice(0, -1))))) && (!lanes || lanes.includes(r.owner)))
	const results = []
	const queue = [...rows]
	await Promise.all(Array.from({ length: Math.min(concurrency, queue.length) }, async () => {
		for (let row = queue.shift(); row; row = queue.shift()) {
			const t0 = Date.now()
			let result
			if (row.status === "skipped") result = { status: "skipped", detail: row.reason }
			else if (row.status === "pending") result = pending(row.reason)
			else if (!RUNNERS[row.runner]) result = pending(`no runner for ${row.runner} rows yet`)
			else {
				try { result = await RUNNERS[row.runner](row, impl, ctx, corpus) } catch (e) { result = failed({ problem: "the gate itself failed", error: String(e?.stack ?? e).slice(0, 800) }) }
			}
			const r = { id: row.id, owner: row.owner, family: row.family, impl: implName, ms: Date.now() - t0, ...result }
			results.push(r)
			onResult?.(r)
		}
	}))
	const order = new Map(corpus.scenario.map((s, i) => [s.id, i]))
	return results.sort((a, b) => (order.get(a.id) ?? Infinity) - (order.get(b.id) ?? Infinity) || (a.id < b.id ? -1 : 1))
}

/** Per-lane pass/fail summary of result rows. */
export function summarize(results) {
	const lanes = {}
	for (const r of results) {
		const l = (lanes[r.owner] ??= { pass: 0, fail: 0, pending: 0, skipped: 0, failed: [] })
		l[r.status]++
		if (r.status === "fail") l.failed.push(r.id)
	}
	return lanes
}
void rmSync; void fixturePath

/** The manifest rows of a seed range of the differential scenario (`contract soak`): the same rows `F-<profile>-<seed>` the corpus lists, for seeds it does not. */
export function diffRows(from, to, profiles = ["gate", "node"]) {
	const rows = []
	for (const profile of profiles) for (let seed = from; seed <= to; seed++) {
		rows.push({ id: `F-${profile}-${seed}`, family: "differential", runner: "diff", source: "durable/contract/scenarios/diff-ordering.mjs", seed, profile, owner: "dcore-loop", levels: ["store", "commit", "context", "output", "order"], status: "ready" })
	}
	return rows
}
