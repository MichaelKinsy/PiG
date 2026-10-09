// The capture pipeline against the real reference checkout: determinism, the tape, the candidate alias, the crash switch.
// The checkout comes from `make durable-contract-setup` (CONTRACT_PI_SOURCE); a missing checkout fails these tests.
import assert from "node:assert/strict"
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { runBench } from "../lib/impl.mjs"
import { diffTraces, readTrace } from "../lib/rowdiff.mjs"
import { piSource, runScenario } from "../lib/run.mjs"

const pi = piSource()
const example = name => join(pi, "packages/durable/test/examples", name)
const out = mkdtempSync(join(tmpdir(), "contract-pipeline-"))
test.after(() => rmSync(out, { recursive: true, force: true }))

const capture = (name, o = {}) => runScenario({ script: example(name), out: join(out, name), name: o.tag ?? "r", tmpKey: name, ...o })
const same = (a, b) => diffTraces(readTrace(a.trace), readTrace(b.trace), { storesA: a.stores, storesB: b.stores })

for (const name of ["00-conversation.ts", "14-chat.ts", "17-coding-tools.ts", "29-sandbox-per-conversation.ts"]) {
	test(`${name}: five captures are equal at every level (CONTRACT 5.3)`, async () => {
		const runs = []
		for (let i = 0; i < 5; i++) runs.push(await capture(name, { tag: `r${i}` }))
		for (const r of runs) assert.equal(r.code, 0, r.stderr)
		for (const r of runs.slice(1)) assert.deepEqual(same(runs[0], r), { equal: true, reports: [] })
		assert.ok(readTrace(runs[0].trace).some(l => l.t === "commit"))
	})
}

test("the virtual clock makes durations and timestamps constants, and real I/O still completes", async () => {
	const r = await capture("17-coding-tools.ts")
	const ts = readTrace(r.trace).filter(l => l.t === "commit").flatMap(l => l.changes).filter(c => c.table === "entries").map(c => c.after[4]).join("")
	assert.match(ts, /"timestamp":1800000000000/)
	assert.doesNotMatch(ts, /"timestamp":18000000[0-9]*[1-9]\b/)
})

test("a model example records its calls with context fingerprints and a bench-independent shape", async () => {
	const r = await capture("14-chat.ts")
	const models = readTrace(r.trace).filter(l => l.t === "model")
	assert.ok(models.length >= 1)
	for (const m of models) assert.match(m.ctx, /^[0-9a-f]{64}$/)
})

test("tape: replay serves the recording and a changed request is a context failure at the first differing call", async () => {
	const tape = join(out, "chat.tape")
	const rec = await capture("14-chat.ts", { tag: "rec", tape: { mode: "record", path: tape } })
	assert.equal(rec.code, 0, rec.stderr)
	const entries = readFileSync(tape, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
	assert.ok(entries.some(e => e.k === "model" && e.events.length > 0))
	const rep = await capture("14-chat.ts", { tag: "rep", tape: { mode: "replay", path: tape } })
	assert.equal(rep.code, 0, rep.stderr)
	assert.deepEqual(same(rec, rep), { equal: true, reports: [] })
	// Change the recorded request of the first model call: the replay refuses it and the comparison names the call.
	const bad = entries.map(e => (e.k === "model" ? { ...e, request: { ...e.request, ctx: "0".repeat(64) } } : e))
	writeFileSync(tape, bad.map(e => JSON.stringify(e)).join("\n") + "\n")
	const rejected = await capture("14-chat.ts", { tag: "bad", tape: { mode: "replay", path: tape } })
	const mismatch = readTrace(rejected.trace).find(l => l.t === "mismatch")
	assert.equal(mismatch.kind, "model"); assert.equal(mismatch.index, 0)
	const d = same(rec, rejected)
	assert.equal(d.equal, false)
	assert.equal(d.reports[0].problem, "tape mismatch in B")
})

test("uuidv7: values are recorded, and replay returns the recorded ones whatever the seed", async () => {
	const tape = join(out, "uuid.tape")
	const rec = await capture("14-chat.ts", { tag: "rec", tape: { mode: "record", path: tape } })
	const recorded = readTrace(rec.trace).filter(l => l.t === "uuid").map(l => l.value)
	assert.ok(recorded.length >= 1, "the provider document asks for a session id")
	for (const v of recorded) assert.match(v, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
	const other = await capture("14-chat.ts", { tag: "seed2", env: { DETENV_SEED: "2" } })
	assert.notDeepEqual(readTrace(other.trace).filter(l => l.t === "uuid").map(l => l.value), recorded, "the seed changes a drawn value")
	const rep = await capture("14-chat.ts", { tag: "rep", env: { DETENV_SEED: "2" }, tape: { mode: "replay", path: tape } })
	assert.deepEqual(readTrace(rep.trace).filter(l => l.t === "uuid").map(l => l.value), recorded)
})

test("candidate alias: a directory laid out like durable/src replaces the reference's modules, and its stores are traced", async () => {
	const cand = join(out, "mirror")
	cpSync(join(pi, "packages/durable/src"), cand, { recursive: true })
	const ref = await capture("14-chat.ts", { tag: "ref" })
	// A candidate whose storage does not go through capsql writes nothing the contract can see.
	const untraced = await capture("14-chat.ts", { tag: "untraced", candidate: cand })
	assert.equal(untraced.code, 0, untraced.stderr)
	assert.equal(readTrace(untraced.trace).filter(l => l.t === "commit").length, 0)
	assert.equal(same(ref, untraced).equal, false)
	// The same mirror with its storage opened through the contract's store is equal to the reference.
	writeFileSync(join(cand, "storage/memory.ts"), [
		'import { SqliteStorage } from "./sqlite/storage.ts";',
		"export class MemoryStorage {",
		"	constructor() {",
		'		const ready = globalThis[Symbol.for("pig.contract")].openDatabase(":memory:").then((db) => SqliteStorage.open(db));',
		'		return new Proxy({}, { get: (_, prop) => (prop === "then" ? undefined : async (...args) => (await ready)[prop](...args)) });',
		"	}",
		"}",
	].join("\n") + "\n")
	const traced = await capture("14-chat.ts", { tag: "traced", candidate: cand })
	assert.equal(traced.code, 0, traced.stderr)
	assert.deepEqual(same(ref, traced), { equal: true, reports: [] })
	assert.ok(readTrace(traced.trace).filter(l => l.t === "commit").length > 0)
})

test("crash switch: SIGKILL after the commit that consumed the requested seq, the WAL keeps it, a second run recovers", async () => {
	const fixture = join(process.env.DURABLE_CONTRACT_CACHE ?? join(import.meta.dirname, "..", ".cache"), "fixtures", "pi-main-50.sqlite")
	assert.ok(existsSync(fixture), "run `contract fixtures 50` first")
	const first = await runBench("pi", { store: fixture, out, name: "crash1", turns: 1, crashAfter: 570 })
	assert.equal(first.signal, "SIGKILL")
	const seqs = readTrace(first.trace).filter(l => l.t === "commit").map(l => l.seq)
	assert.equal(seqs.at(-1), 570)
	const second = await runBench("pi", { store: first.stores[0], out, name: "crash2", recover: true })
	assert.equal(second.code, 0, second.stderr)
	assert.equal(readTrace(second.trace).find(l => l.t === "commit" && l.seq !== undefined).seq, 571)
})

test("SC: the reference passes the storage conformance suite, and a provider that drops commits fails it", async () => {
	const { runScRow } = await import("../lib/gate.mjs")
	const ctx = { out: join(out, "sc") }
	const ref = await runScRow({}, { name: "pi", control: true }, ctx)
	assert.equal(ref.status, "pass", JSON.stringify(ref))
	const dir = join(out, "bad-provider")
	mkdirSync(dir, { recursive: true })
	writeFileSync(join(dir, "contract-sc-provider.ts"), [
		'import { mkdtemp } from "node:fs/promises";',
		'import { tmpdir } from "node:os";',
		'import { join } from "node:path";',
		'import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node";',
		"export async function withStorage(use) {",
		'	const real = await openNodeSqliteStorage(join(await mkdtemp(join(tmpdir(), "bad-sc-")), "s.sqlite"));',
		"	// A write encoder that loses every commit but the first.",
		"	let n = 0;",
		'	await use(new Proxy(real, { get: (t, p) => (p === "commit" ? (w, c) => (n++ ? Promise.resolve(n) : t.commit(w, c)) : typeof t[p] === "function" ? t[p].bind(t) : t[p]) }));',
		"}",
	].join("\n") + "\n")
	const bad = await runScRow({}, { name: "bad", vitest: true, candidateDir: dir }, ctx)
	assert.equal(bad.status, "fail", JSON.stringify(bad))
	assert.match(bad.detail.problem, /cases failed/)
})

test("replay: the statements of a reference trace, replayed onto the fixture, reproduce its row changes and its store", async () => {
	const { replay } = await import("../lib/replay.mjs")
	const { diffCommits, diffStores } = await import("../lib/rowdiff.mjs")
	const { settled } = await import("../lib/store.mjs")
	const fixture = join(process.env.DURABLE_CONTRACT_CACHE ?? join(import.meta.dirname, "..", ".cache"), "fixtures", "pi-main-50.sqlite")
	const ref = await runBench("pi", { store: fixture, out, name: "replay-ref", turns: 1 })
	const trace = readTrace(ref.trace)
	// What a native candidate writes: statements only.
	const native = trace.map(l => (l.t === "commit" ? { t: "commit", store: l.store, seq: l.seq, stmts: l.stmts } : l)).filter(l => l.t !== "final")
	const r = replay(native, fixture, join(out, "replayed"))
	assert.equal(r.commits, 77)
	assert.equal(diffCommits(trace, r.trace), undefined)
	const [a, cleanA] = settled(ref.stores[0])
	assert.equal(diffStores(a, r.store), undefined)
	cleanA()
	// A statement that differs shows up in the replayed changes.
	const mutated = native.map(l => (l.t === "commit" && l.seq === 560 ? { ...l, stmts: l.stmts.map(s => (s.p.some(p => typeof p === "string" && p.includes('"kind":"pi.')) ? { ...s, p: s.p.map(p => (typeof p === "string" ? p.replace('"kind":"pi.', '"kind":"xx.') : p)) } : s)) } : l))
	assert.notEqual(diffCommits(trace, replay(mutated, fixture, join(out, "replayed-bad")).trace), undefined)
})

test("native path: a Go process writes the store through modernc SQLite and is killed after a commit; its statement trace replays to the reference's row changes and pi-durable recovers its store", async () => {
	const { execFileSync } = await import("node:child_process")
	const { runNative } = await import("../impls/native.mjs")
	const { diffCommits, diffStores } = await import("../lib/rowdiff.mjs")
	const { settled } = await import("../lib/store.mjs")
	const fixture = join(process.env.DURABLE_CONTRACT_CACHE ?? join(import.meta.dirname, "..", ".cache"), "fixtures", "pi-main-50.sqlite")
	const exe = join(out, "replayhost")
	execFileSync("go", ["build", "-o", exe, "./durable/core/contracttest/cmd/replayhost"], { cwd: join(import.meta.dirname, "..", "..", ".."), env: { ...process.env, GOWORK: "off" } })
	const control = await runBench("pi", { store: fixture, out, name: "native-control", turns: 1 })
	for (const k of [560, 590, 620]) {
		const crashed = await runBench("pi", { store: fixture, out, name: `native-c1-${k}`, turns: 1, crashAfter: k })
		const recovered = await runBench("pi", { store: crashed.stores[0], out, name: `native-c2-${k}`, recover: true })
		const host = await runNative({ exe, extraArgs: ["--script", control.trace], o: { store: fixture, out, name: `native-x1-${k}`, turns: 1, crashAfter: k } })
		assert.notEqual(host.signal ?? host.code, 0, "the host was killed after the commit")
		assert.deepEqual(diffCommits(readTrace(crashed.trace), readTrace(host.trace)), undefined)
		const [a, ca] = settled(crashed.stores[0]), [b, cb] = settled(host.stores[0])
		assert.equal(diffStores(a, b), undefined, "the store the Go process left equals the one pi-durable left at the same commit")
		ca(); cb()
		// pi-durable opens the store the Go process wrote, recovers the turn, and ends where the control run 2 ends.
		const p2 = await runBench("pi", { store: host.stores[0], out, name: `native-xp2-${k}`, recover: true })
		assert.equal(p2.code, 0, p2.stderr)
		const [c, cc] = settled(recovered.stores[0]), [d, cd] = settled(p2.stores[0])
		assert.equal(diffStores(c, d), undefined)
		cc(); cd()
	}
})

test("R-104: the 1.0.4 fixture is written by the 1.0.4 source (no task startedAt) and main extends it", async () => {
	const { seed } = await import("../cli/fixtures.mjs")
	const { DatabaseSync } = await import("node:sqlite")
	const count = (file, text) => {
		const db = new DatabaseSync(file, { readOnly: true })
		const n = db.prepare("select count(*) n from tasks where instr(cast(record as text), ?) > 0").get(text).n
		db.close()
		return n
	}
	const old = await seed(50, { kind: "v1.0.4" })
	const main = await seed(50)
	assert.equal(count(old, "startedAt"), 0)
	assert.ok(count(main, "startedAt") > 0)
	const ref = await runBench("pi", { store: old, out, name: "legacy-extend", turns: 1 })
	assert.equal(ref.code, 0, ref.stderr)
	const db = new DatabaseSync(ref.stores[0], { readOnly: true })
	const mixed = db.prepare("select sum(instr(cast(record as text), 'startedAt') > 0) s, count(*) n from tasks").get()
	db.close()
	assert.ok(mixed.s > 0 && mixed.s < mixed.n, "main wrote its shape for the new tasks and left the 1.0.4 rows as they were")
})

test("facade candidate: the bench scenario runs through a candidate_dir whose storage opens the contract's store, and equals the reference", async () => {
	const { facadeBench, registerImpl } = await import("../lib/impl.mjs")
	const { diffTraces } = await import("../lib/rowdiff.mjs")
	const { settled } = await import("../lib/store.mjs")
	const fixture = join(process.env.DURABLE_CONTRACT_CACHE ?? join(import.meta.dirname, "..", ".cache"), "fixtures", "pi-main-50.sqlite")
	const cand = join(out, "bench-mirror")
	cpSync(join(pi, "packages/durable/src"), cand, { recursive: true })
	// What a candidate's storage/sqlite/node.ts must be: a store over the contract's database (openDatabase) or its own host-loop store (openStore), not a file opened behind capsql's back.
	writeFileSync(join(cand, "storage/sqlite/node.ts"), [
		'import { SqliteStorage } from "./storage.ts";',
		'export const openNodeSqliteStorage = async (path: string) => SqliteStorage.open(await (globalThis as any)[Symbol.for("pig.contract")].openDatabase(path));',
	].join("\n") + "\n")
	// The start-up module of a candidate runs before the scenario, with the candidate's env.
	writeFileSync(join(out, "setup.ts"), 'import { writeFileSync } from "node:fs";\nwriteFileSync(`${process.env.CONTRACT_STORE_DIR}/setup.txt`, `${process.env.MIRROR}:${process.env.DCORE_WASM}`);\n')
	registerImpl("mirror", { label: "mirror", runBench: facadeBench({ dir: cand, env: { MIRROR: "1", DCORE_WASM: "core.wasm", CONTRACT_CANDIDATE_SETUP: join(out, "setup.ts") } }) })
	const ref = await runBench("pi", { store: fixture, out, name: "facade-ref", turns: 1 })
	const got = await runBench("mirror", { store: fixture, out, name: "facade-got", turns: 1 })
	assert.equal(got.code, 0, got.stderr)
	const [a, ca] = settled(ref.stores[0]), [b, cb] = settled(got.stores[0])
	const d = diffTraces(readTrace(ref.trace), readTrace(got.trace), { storesA: [a], storesB: [b], contexts: ["bench"] })
	ca(); cb()
	assert.deepEqual(d, { equal: true, reports: [] })
	assert.equal(readTrace(got.trace).filter(l => l.t === "commit").length, 77)
	assert.equal(readFileSync(join(got.storeDir ?? join(out, "facade-got.stores"), "setup.txt"), "utf8"), "1:core.wasm")
})
