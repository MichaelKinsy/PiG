// The gate (CONTRACT sections 5-7): run corpus rows against an implementation, compare each with the reference capture, and
// report pass, fail, pending (the implementation cannot run the row yet) or skipped (nobody can, with the reason) per row and
// per owner lane. `pi` as the implementation is the control: the reference against itself, a determinism gate.
import { spawn } from "node:child_process"
import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"
import { seed, fixturePath } from "../cli/fixtures.mjs"
import { loadCorpus, loadImpls, REPO } from "./corpus.mjs"
import { IMPLS, facadeBench, registerImpl, runBench } from "./impl.mjs"
import { matrix, sample } from "./matrix.mjs"
import { diffTraces, readTrace } from "./rowdiff.mjs"
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
	const why = wasm && !existsSync(wasm) ? `has no core module yet (${spec.wasm} is not built)` : setup && !existsSync(setup) ? `has no start-up module yet (${spec.setup} loads the core into the facade)` : undefined
	const facade = Boolean(dir && existsSync(dir)) && !why
	const impl = { name, spec, why, candidateDir: facade ? dir : undefined, env: { ...(wasm ? { DCORE_WASM: wasm } : {}), ...(setup ? { CONTRACT_CANDIDATE_SETUP: setup } : {}), ...spec.env }, examples: facade, vitest: facade, bench: facade || Boolean((bench && existsSync(bench)) || (exe && existsSync(exe))) }
	if (impl.bench && !IMPLS[name]) {
		if (exe) { const { runNative } = await import("../impls/native.mjs"); registerImpl(name, { label: name, runBench: o => runNative({ exe, o }) }) }
		else if (bench) registerImpl(name, { label: name, ...(await import(pathToFileURL(bench).href)) })
		else registerImpl(name, { label: name, runBench: facadeBench({ dir, env: spec.env }) })
	}
	return impl
}

/** Build a candidate when its paths are missing; returns a note for the report (undefined when nothing was needed). */
export async function ensureBuilt(name) {
	const spec = loadImpls().find(i => i.name === name)
	if (!spec?.build) return undefined
	const missing = [spec.candidate_dir, spec.bench, spec.native_exe, spec.wasm, spec.setup].filter(Boolean).some(p => !existsSync(resolve(REPO, p)))
	if (!missing) return undefined
	const [cmd, ...args] = spec.build.split(" ")
	const child = spawn(cmd, args, { cwd: REPO, stdio: ["ignore", "ignore", "pipe"] })
	let err = ""
	child.stderr.on("data", d => (err += d))
	const code = await new Promise(res => child.on("error", () => res(127)).on("close", res))
	return code === 0 ? `built with \`${spec.build}\`` : `build \`${spec.build}\` failed (${code}): ${err.trim().split("\n").slice(-2).join(" | ")}`
}

/** Why a candidate cannot run a row yet: a missing build output or path, named. */
const lacks = impl => `${impl.name} ${impl.why ?? `has nothing to run yet (${impl.spec?.candidate_dir ?? impl.spec?.bench ?? impl.spec?.native_exe})`}`

const pending = (why) => ({ status: "pending", detail: why })
const failed = (detail) => ({ status: "fail", detail })
const passed = (detail) => ({ status: "pass", detail })

function levelsOf(row) { return (row.levels ?? []).filter(l => ["store", "commit", "context", "output"].includes(l)) }

/** E rows and the interop row: a scenario program, reference against implementation. */
async function runProgramRow(row, impl, ctx) {
	if (!impl.control && !impl.examples) return pending(lacks(impl))
	const script = row.runner === "example" ? join(piSource(), row.source) : join(REPO, row.source)
	const args = row.runner === "interop" ? [join(ctx.out, "interop-store.sqlite")] : []
	const common = { script, scenario: row.id, tmpKey: row.id, args }
	const ref = await memo(`ref:${row.id}`, () => runScenario({ ...common, out: join(ctx.out, "ref"), name: row.id }))
	if (ref.code !== 0) return { status: "skipped", detail: `the reference does not run it: ${ref.stderr.slice(0, 300)}` }
	const cand = await runScenario({ ...common, out: join(ctx.out, impl.name), name: row.id, impl: impl.name, candidate: impl.control ? undefined : impl.candidateDir, env: impl.control ? undefined : impl.env })
	if (cand.code !== 0) return failed({ problem: "the candidate run failed", code: cand.code, signal: cand.signal, stderr: cand.stderr.slice(0, 600) })
	const d = diffTraces(readTrace(ref.trace), readTrace(cand.trace), { levels: levelsOf(row), storesA: ref.stores, storesB: cand.stores })
	return d.equal ? passed(`${readTrace(cand.trace).filter(l => l.t === "commit").length} commits`) : failed(d.reports)
}

/** vitest rows: one run of the reference's suite per implementation; a row passes when every test the reference passes passes. */
async function vitestRun(impl, ctx, include) {
	const outFile = join(ctx.out, impl.name, `vitest-${include ? "sc" : "u"}.json`)
	mkdirSync(join(ctx.out, impl.name), { recursive: true })
	const env = { ...process.env, CONTRACT_PI_SOURCE: piSource(), ...(include ? { CONTRACT_TEST_INCLUDE: include } : {}), ...(impl.candidateDir ? { CONTRACT_CANDIDATE: impl.candidateDir, ...impl.env } : {}) }
	const bin = join(piSource(), "node_modules", ".bin", "vitest")
	const child = spawn(bin, ["run", "--config", join(CONTRACT_DIR, "scenarios", "vitest.contract.config.mjs"), "--reporter=json", `--outputFile=${outFile}`], { cwd: join(piSource(), "packages", "durable"), env, stdio: ["ignore", "ignore", "pipe"] })
	let err = ""
	child.stderr.on("data", d => (err += d))
	await new Promise(res => child.on("close", res))
	if (!existsSync(outFile)) throw new Error(`vitest did not write a report: ${err.slice(0, 500)}`)
	const report = JSON.parse(readFileSync(outFile, "utf8"))
	const files = new Map()
	for (const f of report.testResults) files.set(f.name.split("/test/").pop(), { status: f.status, message: f.message, tests: new Map(f.assertionResults.map(a => [a.fullName, a.status])) })
	return files
}
async function runVitestRow(row, impl, ctx) {
	if (!impl.control && !impl.vitest) return pending(lacks(impl))
	const ref = await memo("vitest:ref", () => vitestRun({ name: "ref" }, ctx))
	const cand = impl.control ? ref : await memo(`vitest:${impl.name}`, () => vitestRun(impl, ctx))
	const file = row.source.split("/test/").pop()
	const r = ref.get(file), c = cand.get(file)
	if (!r) return { status: "skipped", detail: "the reference suite has no such file" }
	if (!c) return failed({ problem: "the file did not run", reference: r.status })
	const bad = [...r.tests].filter(([name, st]) => st === "passed" && c.tests.get(name) !== "passed").map(([name]) => `${name}: ${c.tests.get(name) ?? "missing"}`)
	if (c.status === "failed" && !c.tests.size) return failed({ problem: "the file failed to load", message: c.message?.slice(0, 400) })
	return bad.length ? failed({ problem: `${bad.length} test(s) the reference passes did not pass`, first: bad.slice(0, 5) }) : passed(`${[...r.tests.values()].filter(s => s === "passed").length} tests`)
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
	if (broken) return { status: "skipped", detail: `the reference does not run it: ${broken.stderr.slice(0, 300)}` }
	const cand = await run(impl.name, impl.name)
	const bad = cand.find(r => r.code !== 0)
	if (bad) return failed({ problem: "the candidate run failed", code: bad.code, stderr: bad.stderr.slice(0, 600) })
	let commits = 0
	for (const [i, c] of cand.entries()) {
		const last = i === cand.length - 1
		const d = diffTraces(readTrace(ref[i].trace), readTrace(c.trace), { levels: levelsOf(row).filter(l => last || l !== "store"), storesA: last ? ref[i].stores : [], storesB: last ? c.stores : [], contexts: IMPLS[impl.name]?.contexts })
		if (!d.equal) return failed({ turn: i, reports: d.reports })
		commits += readTrace(c.trace).filter(l => l.t === "commit").length
	}
	return passed(`${commits} commits`)
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
	if (!impl.control && !impl.vitest) return pending(lacks(impl))
	if (!impl.control && !existsSync(join(impl.candidateDir, "contract-sc-provider.ts"))) return pending(`${impl.name} has no contract-sc-provider.ts (the write encoder as a Storage) yet`)
	const include = join(CONTRACT_DIR, "scenarios", "sc.test.ts")
	const run = await vitestRun(impl.control ? { name: "sc-ref" } : impl, ctx, include)
	const file = [...run.values()][0]
	if (!file) return failed({ problem: "the suite did not run" })
	const bad = [...file.tests].filter(([, st]) => st !== "passed")
	if (!file.tests.size) return failed({ problem: "the suite registered no tests", message: file.message?.slice(0, 400) })
	return bad.length ? failed({ problem: `${bad.length} of ${file.tests.size} cases failed`, first: bad.slice(0, 5).map(([n]) => n) }) : passed(`${file.tests.size} cases`)
}

const RUNNERS = { example: runProgramRow, interop: runProgramRow, vitest: runVitestRow, "storage-conformance": runScRow, bench: runBenchRow, matrix: runMatrixRow }

/** Run the selected rows against one implementation; returns result rows. */
export async function gate({ impl: implName, ids, lanes, out, concurrency = 8, onResult }) {
	const corpus = loadCorpus()
	const impl = await resolveImpl(implName)
	mkdirSync(out, { recursive: true })
	const ctx = { out, concurrency: Math.max(1, Math.floor(concurrency / 2)) }
	const rows = corpus.scenario.filter(r => (!ids || ids.some(id => r.id === id || (id.endsWith("*") && r.id.startsWith(id.slice(0, -1))))) && (!lanes || lanes.includes(r.owner)))
	const results = []
	const queue = [...rows]
	await Promise.all(Array.from({ length: Math.min(concurrency, queue.length) }, async () => {
		for (let row = queue.shift(); row; row = queue.shift()) {
			const t0 = Date.now()
			let result
			if (row.status === "skipped") result = { status: "skipped", detail: row.reason }
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
	return results.sort((a, b) => order.get(a.id) - order.get(b.id))
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
