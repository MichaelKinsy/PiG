import assert from "node:assert/strict"
import { mkdirSync, mkdtempSync, rmSync, utimesSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { Capture } from "../lib/capsql.mjs"
import { lock } from "../lib/run.mjs"
import { sample } from "../lib/matrix.mjs"
import { classify, diffCommits, diffContexts, diffOutput } from "../lib/rowdiff.mjs"
import { parseToml } from "../lib/toml.mjs"

test("toml: the subset corpus.toml uses, and nothing else", () => {
	const t = parseToml('# c\n[pin]\nx = "a#b" # c\n[[s]]\nid = "E00"\nl = ["a", "b"]\nn = [\n "x",\n "y",\n]\nt = { a = "b", c = 3 }\n[[s]]\nid = "E01"\nb = true\nk = 12\n')
	assert.deepEqual(t, { pin: { x: "a#b" }, s: [{ id: "E00", l: ["a", "b"], n: ["x", "y"], t: { a: "b", c: 3 } }, { id: "E01", b: true, k: 12 }] })
	assert.throws(() => parseToml("x = 1.2.3"), /trailing|cannot/)
	assert.throws(() => parseToml("x = @"), /cannot parse value/)
})

test("classify: key order, number format and value are different failures of the same text", () => {
	assert.equal(classify('{"a":1}', '{"a":1}'), undefined)
	assert.equal(classify('{"a":1,"b":2}', '{"b":2,"a":1}').kind, "key-order")
	assert.equal(classify('{"a":1}', '{"a":1.0}').kind, "number-format-or-escape")
	assert.equal(classify('{"a":"\\u00e9"}', '{"a":"é"}').kind, "number-format-or-escape")
	const v = classify('{"a":{"b":[1,2]}}', '{"a":{"b":[1,3]}}')
	assert.deepEqual([v.kind, v.path], ["value", "$.a.b[1]"])
	assert.equal(classify("x", "y").kind, "text")
})

const commit = (store, seq, changes) => ({ t: "commit", store, seq, changes })
const row = (table, after) => ({ table, op: "insert", pk: [1], after })

test("diffCommits names the first divergent commit, its table and the differing column", () => {
	const a = [commit("s0", 1, [row("entries", [1, "x"])]), commit("s0", 2, [row("entries", [2, '{"a":1,"b":2}'])])]
	const b = [commit("s0", 1, [row("entries", [1, "x"])]), commit("s0", 2, [row("entries", [2, '{"b":2,"a":1}'])])]
	const d = diffCommits(a, b)
	assert.equal(d.seq, 2); assert.equal(d.table, "entries"); assert.equal(d.columns[0].kind, "key-order")
	assert.equal(diffCommits(a, a), undefined)
	assert.equal(diffCommits(a, a.slice(0, 1)).problem, "only in A")
	assert.equal(diffCommits(a, [a[0], commit("s0", 3, a[1].changes)]).problem, "different seq")
})

test("diffContexts compares the fingerprints it is told to and ignores the others", () => {
	const a = [{ t: "model", ctx: "1", bench: "b" }], b = [{ t: "model", bench: "b" }]
	assert.ok(diffContexts(a, b))
	assert.equal(diffContexts(a, b, ["bench"]), undefined)
	assert.ok(diffContexts(a, [{ t: "model", ctx: "1", bench: "c" }]))
})

test("diffOutput reports a differing line and a differing exit status", () => {
	const o = (...t) => t.map(text => ({ t: "out", text }))
	assert.equal(diffOutput(o("a", "b"), o("a", "b")), undefined)
	assert.deepEqual(diffOutput(o("a", "b"), o("a", "c")), { level: "output", line: 1, a: "b", b: "c" })
	assert.equal(diffOutput([...o("a"), { t: "end", code: 0 }], [...o("a"), { t: "end", code: 1 }]).problem, "exit status")
})

test("sample: first and last crash point are always included", () => {
	const points = Array.from({ length: 77 }, (_, i) => 500 + i)
	const s = sample(points, 10)
	assert.equal(s.length, 10); assert.equal(s[0], 500); assert.equal(s.at(-1), 576)
	assert.deepEqual(sample(points, 0), points)
})

test("capsql: a commit is the same whatever order its statements touched the tables in", () => {
	const dir = mkdtempSync(join(tmpdir(), "capsql-test-"))
	try {
		const lines = []
		const make = (name, order) => {
			const cap = new Capture(join(dir, `${name}.sqlite`), { label: name, sink: l => lines.push(l) })
			cap.exec("CREATE TABLE durable_metadata (singleton INTEGER PRIMARY KEY, next_id TEXT NOT NULL, next_seq INTEGER NOT NULL); INSERT INTO durable_metadata VALUES (1, '2', 5); CREATE TABLE a (id INTEGER PRIMARY KEY, v TEXT); CREATE TABLE b (id INTEGER PRIMARY KEY, v TEXT)")
			cap.begin()
			for (const t of order) cap.run(`INSERT INTO ${t} (id, v) VALUES (?, ?)`, [1, t])
			cap.run("UPDATE durable_metadata SET next_seq = ? WHERE singleton = 1", [6])
			cap.commit()
			cap.finish()
		}
		make("x", ["a", "b"]); make("y", ["b", "a"])
		const [x, y] = ["x", "y"].map(s => lines.find(l => l.t === "commit" && l.store === s))
		assert.equal(x.seq, 5)
		assert.deepEqual(x.changes, y.changes)
		assert.deepEqual(x.changes.map(c => c.table), ["a", "b", "durable_metadata"])
		assert.deepEqual(x.stmts.map(s => s.k), ["w", "w", "w"])
	} finally { rmSync(dir, { recursive: true, force: true }) }
})

test("capsql: tables the sidecar and workerd own never reach a commit line", () => {
	const dir = mkdtempSync(join(tmpdir(), "capsql-test-"))
	try {
		const lines = []
		const cap = new Capture(join(dir, "s.sqlite"), { label: "s", sink: l => lines.push(l) })
		cap.exec("CREATE TABLE pig_index (id INTEGER PRIMARY KEY, v TEXT); CREATE TABLE _cf_KV (id INTEGER PRIMARY KEY, v TEXT)")
		cap.begin(); cap.run("INSERT INTO pig_index VALUES (1, 'x')"); cap.run("INSERT INTO _cf_KV VALUES (1, 'x')"); cap.commit()
		assert.equal(lines.filter(l => l.t === "commit").length, 0)
		cap.finish()
	} finally { rmSync(dir, { recursive: true, force: true }) }
})

test("CONTRACT_TICKS stamps each commit with the microtask-queue passes since the start (cli/ticks.mjs reads them)", async () => {
	const { execFileSync } = await import("node:child_process")
	const out = execFileSync(process.execPath, ["-e", `
		process.env.CONTRACT_TICKS = "1"
		await import(${JSON.stringify(new URL("../hooks/register.mjs", import.meta.url).href)})
		const first = globalThis.__contractTick()
		for (let i = 0; i < 5; i++) await Promise.resolve()
		console.log(JSON.stringify([first, globalThis.__contractTick()]))
	`, "--input-type=module"], { encoding: "utf8", env: { ...process.env, CONTRACT_PI_SOURCE: process.env.CONTRACT_PI_SOURCE ?? "/nonexistent", CONTRACT_TRACE: join(tmpdir(), `ticks-${process.pid}.jsonl`) } })
	const [first, later] = JSON.parse(out)
	assert.ok(later >= first + 4, `five awaits advanced the counter from ${first} to ${later}`)
})

test("detenv: performance.now differences are exact whatever the real start-up time (a 7.5 ms span rounded to 7 or 8 from run to run)", async () => {
	const { execFileSync } = await import("node:child_process")
	const detenv = new URL("../lib/detenv.mjs", import.meta.url).href
	const spans = new Set()
	// Each run starts at a different real fraction of a millisecond; dyadic spans are exact in binary, so any difference is the origin's rounding.
	for (let i = 0; i < 14; i++) {
		const out = execFileSync(process.execPath, ["--input-type=module", "-e", `
			const end = Date.now() + ${(i * 7) % 53}; while (Date.now() < end) {}
			await import(${JSON.stringify(detenv)})
			const a = performance.now()
			await new Promise(resolve => setTimeout(resolve, 7.5))
			const b = performance.now()
			await new Promise(resolve => setTimeout(resolve, 0.375))
			console.log(b - a, performance.now() - b)`], { encoding: "utf8", env: { ...process.env, DETENV_EPOCH: "1800000000000", DETENV_SEED: "1" } })
		spans.add(out.trim())
	}
	assert.deepEqual([...spans], ["7.5 0.375"], `spans ${[...spans].join(" | ")}`)
})

test("detenv: the virtual clock does not move while work is scheduled on the real event loop (a real timer, a chain of immediates)", async () => {
	const { execFileSync } = await import("node:child_process")
	const detenv = new URL("../lib/detenv.mjs", import.meta.url).href
	// A virtual 100 ms timer is pending while the program waits 40 real ms (node:timers/promises is not virtualised) and then spends 300 real
	// immediates; no step of that work is virtual time, so a stream or tool measured across it has the same span whatever the load.
	const out = execFileSync(process.execPath, ["--input-type=module", "-e", `
		import { setTimeout as realSleep } from "node:timers/promises"
		await import(${JSON.stringify(detenv)})
		let fired = false
		setTimeout(() => { fired = true }, 100)
		const start = performance.now()
		await realSleep(40)
		const afterSleep = performance.now() - start
		for (let i = 0; i < 300; i++) await new Promise(resolve => setImmediate(resolve))
		const afterImmediates = performance.now() - start
		console.log(JSON.stringify({ afterSleep, afterImmediates, fired }))`], { encoding: "utf8", env: { ...process.env, DETENV_EPOCH: "1800000000000", DETENV_SEED: "1" } })
	assert.deepEqual(JSON.parse(out), { afterSleep: 0, afterImmediates: 0, fired: false })
})

test("CONTRACT_DEPTH_DEBUG writes only its own file: the trace is the same set or unset, and a nested call names the op and call that set the depth", async () => {
	const { openTrace } = await import("../lib/trace.mjs")
	const { wrapModels } = await import("../lib/tape.mjs")
	const { readFileSync } = await import("node:fs")
	const dir = mkdtempSync(join(tmpdir(), "depth-debug-"))
	const saved = { tape: process.env.CONTRACT_TAPE, debug: process.env.CONTRACT_DEPTH_DEBUG, trace: globalThis[Symbol.for("pig.contract.trace")] }
	delete process.env.CONTRACT_TAPE
	const model = { provider: "faux", id: "m" }
	const context = { messages: [{ role: "user", content: "hi", timestamp: 0 }] }
	const run = async (name, debugFile) => {
		if (debugFile) process.env.CONTRACT_DEPTH_DEBUG = debugFile
		else delete process.env.CONTRACT_DEPTH_DEBUG
		const path = join(dir, `${name}.trace`)
		openTrace(path)
		const fake = {
			streamSimple: () => ({ push() {}, result: async () => ({}) }),
			async completeSimple(m, c) { this.streamSimple(m, c); return "summary" },
		}
		const models = wrapModels(fake, { createStream: () => ({ push() {}, result: async () => ({}) }) })
		assert.equal(await models.completeSimple(model, context), "summary")
		models.streamSimple(model, context)
		return readFileSync(path, "utf8")
	}
	try {
		const debugFile = join(dir, "depth.jsonl")
		const unset = await run("unset")
		const set = await run("set", debugFile)
		assert.equal(set, unset, "the trace is byte-identical with the diagnostic set")
		assert.deepEqual(unset.trim().split("\n").map(l => JSON.parse(l).t), ["effect", "model"], "the nested streamSimple is not traced, the top-level one is")
		const lines = readFileSync(debugFile, "utf8").trim().split("\n").map(l => JSON.parse(l))
		assert.equal(lines.length, 1, "one nested call")
		assert.equal(lines[0].op, "completeSimple")
		assert.equal(lines[0].call, 1)
		assert.ok(Array.isArray(lines[0].stack) && lines[0].stack.length > 0)
		await run("unset-again")
		assert.equal(readFileSync(debugFile, "utf8").trim().split("\n").length, 1, "unset, nothing is written to the debug file")
	} finally {
		if (saved.tape === undefined) delete process.env.CONTRACT_TAPE; else process.env.CONTRACT_TAPE = saved.tape
		if (saved.debug === undefined) delete process.env.CONTRACT_DEPTH_DEBUG; else process.env.CONTRACT_DEPTH_DEBUG = saved.debug
		globalThis[Symbol.for("pig.contract.trace")] = saved.trace
		rmSync(dir, { recursive: true, force: true })
	}
})

test("run lock: a lock left by a killed process is taken over, a lock held by a live process is waited on, release removes it", async () => {
	const base = mkdtempSync(join(tmpdir(), "contract-lock-"))
	try {
		const dir = join(base, "one.lock")
		// A dead owner: a child that exited. Its pid is no longer a process.
		const { pid: dead } = (await import("node:child_process")).spawnSync(process.execPath, ["-e", ""])
		mkdirSync(dir)
		writeFileSync(join(dir, "pid"), String(dead))
		const release = await lock(dir)
		let second = false
		const waiting = lock(dir).then(r => { second = true; return r })
		await new Promise(r => setTimeout(r, 200))
		assert.equal(second, false, "a lock held by this live process is not taken over")
		release()
		const release2 = await waiting
		assert.equal(second, true, "release lets the waiter in")
		release2()
		assert.throws(() => mkdirSync(join(base, "one.lock", "x")), "the released lock directory is gone")
	} finally { rmSync(base, { recursive: true, force: true }) }
})

test("run lock: of several processes that find the same abandoned lock, one at a time holds it", async () => {
	const { spawn, spawnSync } = await import("node:child_process")
	const base = mkdtempSync(join(tmpdir(), "contract-lock-race-"))
	// Each child takes the lock, marks itself as a holder, looks for another holder across a few event-loop turns, and releases.
	const child = `import { lock } from ${JSON.stringify(new URL("../lib/run.mjs", import.meta.url).href)}
		import { readdirSync, rmSync, writeFileSync } from "node:fs"
		const [dir, held] = process.argv.slice(1)
		const release = await lock(dir)
		writeFileSync(held + "/" + process.pid, "")
		let most = readdirSync(held).length
		await new Promise(r => setTimeout(r, 20))
		most = Math.max(most, readdirSync(held).length)
		rmSync(held + "/" + process.pid)
		release()
		process.exit(most === 1 ? 0 : 3)`
	try {
		const failures = []
		for (let trial = 0; trial < 20; trial++) {
			const dir = join(base, `t${trial}.lock`), held = join(base, `t${trial}.held`)
			mkdirSync(held)
			mkdirSync(dir)
			writeFileSync(join(dir, "pid"), String(spawnSync(process.execPath, ["-e", ""]).pid))
			const codes = await Promise.all(Array.from({ length: 8 }, () => new Promise(res => {
				const p = spawn(process.execPath, ["--input-type=module", "-e", child, dir, held], { stdio: ["ignore", "ignore", "pipe"] })
				let err = ""
				p.stderr.on("data", d => { err += d })
				p.on("close", code => res(code === 0 ? undefined : `trial ${trial}: exit ${code} ${err.split("\n").find(l => /Error/.test(l)) ?? ""}`))
			})))
			failures.push(...codes.filter(Boolean))
		}
		assert.deepEqual(failures, [], "two waiters held the lock at once, or a waiter lost the lock it had just made")
	} finally { rmSync(base, { recursive: true, force: true }) }
})

test("run lock: a lock without a pid is waited on for a few seconds, then taken over (an owner killed between mkdir and writing the pid)", { timeout: 20000 }, async () => {
	const base = mkdtempSync(join(tmpdir(), "contract-lock-nopid-"))
	try {
		for (const pid of [undefined, ""]) {
			const dir = join(base, `l${pid === undefined ? "none" : "empty"}.lock`)
			mkdirSync(dir)
			if (pid !== undefined) writeFileSync(join(dir, "pid"), pid)
			let taken = false
			const waiting = lock(dir).then(r => { taken = true; return r })
			await new Promise(r => setTimeout(r, 200))
			assert.equal(taken, false, "a fresh lock without a pid is waited on")
			const old = (Date.now() - 60000) / 1000
			utimesSync(dir, old, old)
			const release = await waiting
			release()
		}
	} finally { rmSync(base, { recursive: true, force: true }) }
})
