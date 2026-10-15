// The count gate's arithmetic and ratchet (lib/counts.mjs): a run's cumulative samples become cold and warm counts, every
// number above or below its baseline fails, a new or missing number fails, and the tolerance applies only where the
// baseline names it. The last test runs the reference's bench program with counting on and checks the store counts.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import test from "node:test"
import { bar, BAR_HEAP_SIZE_RATIO, compare, coreStats, counters, describe, flatten, install, perTurn, reported, summarize, toleranceOf, irClocked, irWrapper, readIr } from "../lib/counts.mjs"
import { LIVE_WINDOW, LIVE_WINDOW_METRIC, NATIVE_LIVE_RUNS, liveWindowCells, nativeLiveGrowth, settleNativeLiveGrowth, valgrind } from "../lib/instr.mjs"
import { benchRunner } from "../lib/robust.mjs"

const samples = [
	{ turn: 0, statements: 10, reads: 4, writes: 6, rowsRead: 40, rowsWritten: 5, commits: 2, crossings: 100, allocBytes: 1000, mallocs: 10, bytesCrossed: 5000 },
	{ turn: 1, statements: 13, reads: 5, writes: 8, rowsRead: 41, rowsWritten: 8, commits: 3, crossings: 130, allocBytes: 1300, mallocs: 13, bytesCrossed: 5900 },
	{ turn: 2, statements: 16, reads: 6, writes: 10, rowsRead: 42, rowsWritten: 11, commits: 4, crossings: 160, allocBytes: 1600, mallocs: 16, bytesCrossed: 6900 },
	{ final: true, heap: 777, linear: 65536 },
]

test("summarize: cold is the first measured turn with the open, warm the sum of the others", () => {
	const s = summarize(samples)
	assert.equal(s.warmTurns, 2)
	assert.deepEqual(s.cold, { statements: 10, reads: 4, writes: 6, rowsRead: 40, rowsWritten: 5, commits: 2, crossings: 100, allocBytes: 1000, mallocs: 10, bytesCrossed: 5000, allocsPerCrossing: 0.1 })
	assert.deepEqual(s.warm, { statements: 6, reads: 2, writes: 4, rowsRead: 2, rowsWritten: 6, commits: 2, crossings: 60, allocBytes: 600, mallocs: 6, bytesCrossed: 1900, allocsPerCrossing: 0.1 })
	assert.equal(flatten(s)["warm.bytesCrossed"], 1900)
	assert.equal(flatten(s)["warm.allocsPerCrossing"], 0.1)
	assert.equal(flatten(s)["warm.crossings"], 60)
	assert.equal(flatten(s).heap, 777)
	assert.equal(flatten(s).linear, 65536)
})

test("summarize: a run without the alloc counters has none, and one measured turn is refused", () => {
	const plain = samples.map(({ allocBytes, mallocs, ...rest }) => rest)
	assert.equal("warm.allocBytes" in flatten(summarize(plain)), false)
	assert.equal("warm.allocsPerCrossing" in flatten(summarize(plain)), false, "no mallocs, no per-crossing figure")
	assert.throws(() => summarize(samples.slice(0, 1)), /need at least 2/)
})

test("summarize: allocsPerCrossing is the phase's mallocs over its crossings, rounded to two decimals", () => {
	// cold 10/30 = 0.333..., warm (30-10)/(45-30) = 1.333...: an unrounded or truncated ratio, or a ratio of the cumulative sums, differs.
	const s = summarize([
		{ turn: 0, crossings: 30, mallocs: 10 },
		{ turn: 1, crossings: 45, mallocs: 30 },
		{ final: true },
	])
	assert.equal(s.cold.allocsPerCrossing, 0.33)
	assert.equal(s.warm.allocsPerCrossing, 1.33)
	assert.equal(summarize([{ turn: 0, crossings: 3, mallocs: 2 }, { turn: 1, crossings: 6, mallocs: 4 }, { final: true }]).warm.allocsPerCrossing, 0.67, "rounded, not truncated")
	const none = summarize([{ turn: 0, crossings: 0, mallocs: 5 }, { turn: 1, crossings: 0, mallocs: 7 }, { final: true }])
	assert.equal("allocsPerCrossing" in none.warm, false, "no crossings, no per-crossing figure")
})

test("coreStats: bytesCrossed is the sum over the cores that count it", () => {
	const cores = [
		{ bytesCrossed: 100, allocStats: () => ({ bytes: 5n, mallocs: 2n }) },
		{ bytesCrossed: 23, allocStats: () => ({ bytes: 7, mallocs: 3 }) },
		{ allocStats: () => ({ bytes: 1, mallocs: 1 }) },
	]
	assert.deepEqual(coreStats(cores), { allocBytes: 13, mallocs: 6, bytesCrossed: 123 })
	assert.equal("bytesCrossed" in coreStats([{ allocStats: () => ({ bytes: 1, mallocs: 1 }) }]), false, "a core without the counter adds no field")
})

test("coreStats: a native core's gated heap is net of its threads; the raw heap, threads and perThread are reported", () => {
	const native = (heap, threads) => ({ heapStats: () => ({ heap, reserved: 0 }), threadStats: () => ({ heap: heap + 5320, threads: threads + 1, perThread: 5320, threadsBefore: threads }) })
	const base = coreStats([native(266488, 4)], { final: true }), extra = coreStats([native(266488 + 5320, 5)], { final: true })
	assert.deepEqual(base, { heap: 266488, heapNet: 266488 + 5320 - 5 * 5320, threads: 4, perThread: 5320 })
	assert.equal(extra.heapNet, base.heapNet, "one more thread moves the raw heap, not heapNet")
	assert.equal(coreStats([native(266488 + 48, 4)], { final: true }).heapNet - base.heapNet, 48, "one more object of the core's moves heapNet")
	const s = summarize([{ turn: 0 }, { turn: 1 }, { final: true, ...extra }])
	assert.deepEqual(flatten(s), { heapNet: base.heapNet }, "the gate sees heapNet, not the raw heap or the threads")
	assert.deepEqual(reported(s), { heap: 266488 + 5320, threads: 5, perThread: 5320 })
	// A Wasm core has no threads: its heap stays the gated figure and nothing is reported.
	const wasm = summarize([{ turn: 0 }, { turn: 1 }, { final: true, ...coreStats([{ heapStats: () => ({ heap: 137392, reserved: 0 }) }], { final: true }) }])
	assert.deepEqual([flatten(wasm), reported(wasm)], [{ heap: 137392 }, {}])
	assert.throws(() => coreStats([{ ...native(1, 1), threadStats: () => ({ heap: 1, threads: 1, perThread: 0, threadsBefore: 1 }) }], { final: true }), /did not measure the heap one thread holds/)
})

test("compare: native allocsPerCrossing has the mallocs slack; Wasm's is exact", () => {
	const tol = toleranceOf({})
	const base = { "native/50": { "warm.allocsPerCrossing": 100 }, "wasm-tinygo/50": { "warm.allocsPerCrossing": 100 } }
	assert.deepEqual(compare(base, { "native/50": { "warm.allocsPerCrossing": 100.2 }, "wasm-tinygo/50": { "warm.allocsPerCrossing": 100 } }, { tolerance: tol }), [])
	assert.deepEqual(compare(base, { "native/50": { "warm.allocsPerCrossing": 100.3 }, "wasm-tinygo/50": { "warm.allocsPerCrossing": 100.01 } }, { tolerance: tol }).map(x => `${x.cell} ${x.kind}`), ["native/50 higher", "wasm-tinygo/50 higher"])
})

test("perTurn: statements, mallocs and bytes per warm turn and collections per 100 warm turns come from the warm sums; a run without alloc counters has only statements", () => {
	const s = summarize(samples)
	// Warm is turns 1 and 2: 6 statements, 6 mallocs and 600 bytes over 2 turns.
	assert.deepEqual(perTurn(s), { statementsPerTurn: 3, mallocsPerTurn: 3, allocBytesPerTurn: 300 })
	// Collections: 1 over the 2 warm turns is 50 per 100 turns.
	const gc = samples.map((x, i) => (x.final ? x : { ...x, gcs: [4, 4, 5][i] }))
	assert.equal(perTurn(summarize(gc)).gcsPer100Turns, 50)
	assert.equal(flatten(summarize(gc))["cold.gcs"], 4)
	const plain = samples.map(({ allocBytes, mallocs, ...rest }) => rest)
	assert.deepEqual(Object.keys(perTurn(summarize(plain))), ["statementsPerTurn"])
})

test("compare: equal passes; higher, lower, new and missing fail", () => {
	const base = { "wasm-tinygo/50": { "warm.rowsRead": 10, "warm.crossings": 3700, heap: 1000 } }
	assert.deepEqual(compare(base, { "wasm-tinygo/50": { "warm.rowsRead": 10, "warm.crossings": 3700, heap: 1000 } }), [])
	const f = compare(base, { "wasm-tinygo/50": { "warm.rowsRead": 11, "warm.crossings": 3699, mallocs: 5 }, "wasm-go/50": { heap: 1 } })
	assert.deepEqual(f.map(x => `${x.cell} ${x.kind} ${x.metric}`).sort(), [
		"wasm-go/50 new *",
		"wasm-tinygo/50 higher warm.rowsRead",
		"wasm-tinygo/50 lower warm.crossings",
		"wasm-tinygo/50 missing heap",
		"wasm-tinygo/50 new mallocs",
	])
	assert.equal(describe(f.find(x => x.kind === "higher")), "wasm-tinygo/50 warm.rowsRead 11 > 10 (+10.0%)")
})

test("compare: a tolerance applies to the implementation and metric it names only", () => {
	const base = { "native/50": { heap: 1000, "warm.rowsRead": 100 }, "wasm-go/50": { heap: 1000 } }
	const tolerance = { native: { heap: 0.02, } }
	assert.deepEqual(compare(base, { "native/50": { heap: 1019, "warm.rowsRead": 100 }, "wasm-go/50": { heap: 1000 } }, { tolerance }), [])
	assert.deepEqual(compare(base, { "native/50": { heap: 1021, "warm.rowsRead": 100 }, "wasm-go/50": { heap: 1000 } }, { tolerance }).map(x => x.kind), ["higher"])
	assert.deepEqual(compare(base, { "native/50": { heap: 1000, "warm.rowsRead": 101 }, "wasm-go/50": { heap: 1001 } }, { tolerance }).map(x => `${x.cell} ${x.kind}`), ["native/50 higher", "wasm-go/50 higher"])
})

test("compare: native live growth near zero has an absolute floor of 32 B/turn; Wasm and other metrics have none", () => {
	const base = { "native/50": { "warm.liveGrowthPerTurn": 0, "warm.rowsRead": 0 }, "wasm-go/50": { "warm.liveGrowthPerTurn": 0 } }
	assert.deepEqual(compare(base, { "native/50": { "warm.liveGrowthPerTurn": 32, "warm.rowsRead": 0 }, "wasm-go/50": { "warm.liveGrowthPerTurn": 0 } }), [])
	assert.deepEqual(compare(base, { "native/50": { "warm.liveGrowthPerTurn": -32, "warm.rowsRead": 0 }, "wasm-go/50": { "warm.liveGrowthPerTurn": 0 } }), [])
	assert.deepEqual(compare(base, { "native/50": { "warm.liveGrowthPerTurn": 33, "warm.rowsRead": 1 }, "wasm-go/50": { "warm.liveGrowthPerTurn": 1 } }).map(x => `${x.cell} ${x.metric} ${x.kind}`),
		["native/50 warm.liveGrowthPerTurn higher", "native/50 warm.rowsRead higher", "wasm-go/50 warm.liveGrowthPerTurn higher"])
})

test("toleranceOf: native allocations get their slack even from a baseline written without it; Wasm stays exact", () => {
	// A baseline file from before alloc_stats: only the native heap had slack.
	const tol = toleranceOf({ tolerance: { native: { heap: 0.02 } } })
	const base = { "native/50": { "warm.allocBytes": 13219904, "warm.mallocs": 263355, heap: 700000 }, "wasm-go/50": { "warm.allocBytes": 1000000 } }
	// dcore-int's mg60 run: +0.1% allocated bytes, +0.01% mallocs on native.
	assert.deepEqual(compare(base, { "native/50": { "warm.allocBytes": 13234600, "warm.mallocs": 263381, heap: 700000 } }, { tolerance: tol }), [])
	assert.equal(compare(base, { "native/50": { "warm.allocBytes": 13219904 * 1.01, "warm.mallocs": 263355, heap: 700000 } }, { tolerance: tol })[0]?.kind, "higher")
	assert.equal(compare(base, { "wasm-go/50": { "warm.allocBytes": 1000001 } }, { tolerance: tol })[0]?.kind, "higher")
	// The committed baseline keeps every Wasm count exact, the replay's instruction counts included.
	const committed = toleranceOf(JSON.parse(readFileSync(fileURLToPath(new URL("../counts.json", import.meta.url)), "utf8")))
	for (const impl of ["wasm-tinygo", "wasm-go"]) {
		assert.equal(compare({ [`${impl}/50`]: { "warm.instructions": 706777321 } }, { [`${impl}/50`]: { "warm.instructions": 706777322 } }, { tolerance: committed })[0]?.kind, "higher", impl)
	}
	// The committed baseline widens no slack: every gate in it is the floor's, which this file tests. A file-level ir of 0.03
	// (b4fd357b26) kept native ir at 3% on dcore-integrate-s after the floor went to 0.25%, and no test saw it; a wider slack
	// belongs in DEFAULT_TOLERANCE, where its own test reads it.
	assert.deepEqual(committed, toleranceOf({}))
	// A baseline may widen a slack, not narrow it.
	assert.equal(toleranceOf({ tolerance: { native: { heap: 0.05, mallocs: 0 } } }).native.heap, 0.05)
	assert.equal(toleranceOf({ tolerance: { native: { mallocs: 0 } } }).native.mallocs, 0.0025)
	// The native heap net of the runtime's threads is gated at 0.25%, floor and committed baseline alike: the 352 B the
	// contended runs read above the baseline passes, 700 B more of the core's own fails, and widening the slack fails here. The raw heap's
	// one-more-thread mode no longer reaches the gate (coreStats below; cmd/corenative TestHeapThreadsNetsOutAnExtraThread).
	assert.equal(toleranceOf({}).native.heapNet, 0.0025)
	assert.equal(committed.native.heapNet, 0.0025)
	const heap = { "native/1000": { heapNet: 266488 } }
	assert.deepEqual(compare(heap, { "native/1000": { heapNet: 266488 + 352 } }, { tolerance: committed }), [])
	assert.equal(compare(heap, { "native/1000": { heapNet: 266488 + 700 } }, { tolerance: committed })[0]?.kind, "higher")
	assert.equal(compare(heap, { "native/1000": { heapNet: 266488 + 5368 } }, { tolerance: committed })[0]?.kind, "higher")
	// Native collections: one either way in the 4 warm ones, none in the single cold one; the Wasm builds' count is exact.
	const gcs = { "native/50": { "cold.gcs": 1, "warm.gcs": 4 }, "wasm-go/50": { "warm.gcs": 5 } }
	const kinds = got => compare(gcs, got, { tolerance: toleranceOf({}) }).map(f => `${f.cell} ${f.metric} ${f.kind}`)
	assert.deepEqual(kinds({ "native/50": { "cold.gcs": 1, "warm.gcs": 5 } }), [])
	assert.deepEqual(kinds({ "native/50": { "cold.gcs": 1, "warm.gcs": 3 } }), [])
	assert.deepEqual(kinds({ "native/50": { "cold.gcs": 2, "warm.gcs": 6 } }), ["native/50 cold.gcs higher", "native/50 warm.gcs higher"])
	assert.deepEqual(kinds({ "wasm-go/50": { "warm.gcs": 6 } }), ["wasm-go/50 warm.gcs higher"])
})

// wasm-go's instructions are exact, and the Go linker writes the main module's version into the module: a VCS-stamped
// pseudo-version or "(devel)". The two differed by 7 in a Wasm instruction proxy, so a gate whose caller exports GOFLAGS=-buildvcs=false (or builds in
// a tree without git) would fail against a baseline measured with the stamp. The build pins the flag.
test("the Go core build is a function of the source, not of the caller's VCS stamping", { timeout: 300_000 }, () => {
	const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..")
	const out = mkdtempSync(join(tmpdir(), "counts-buildvcs-"))
	const build = (name, goflags) => {
		const file = join(out, `${name}.wasm`)
		execFileSync("sh", [join(root, "durable/core/budget/build-wasm.sh"), "go", join(root, "durable/core/cmd/corewasm"), file], { env: { ...process.env, GOFLAGS: goflags, PIG_BUILDVCS: "1" }, stdio: ["ignore", "ignore", "pipe"] })
		return readFileSync(file)
	}
	const stamped = build("stamped", "-buildvcs=auto"), unstamped = build("unstamped", "-buildvcs=false")
	assert.ok(stamped.equals(unstamped), "core-go.wasm differs with and without the VCS stamp")
})

test("the reference's bench program counts its store work and repeats it exactly", { timeout: 120_000 }, async () => {
	const run = await benchRunner("pi")
	const out = mkdtempSync(join(tmpdir(), "counts-test-"))
	const once = async name => {
		const file = join(out, `${name}.jsonl`)
		const r = await run({ out, name, turns: 3, tools: 2, variant: "incr", env: { CONTRACT_COUNTS: file } })
		assert.equal(r.code, 0, r.stderr)
		return readFileSync(file, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
	}
	const a = await once("a"), b = await once("b")
	assert.deepEqual(a, b)
	const s = summarize(a)
	assert.equal(s.warmTurns, 2)
	// Every warm turn commits and writes; reads return rows; the reference has no core, so nothing crosses.
	// rowsWritten counts row changes, not commits: each commit updates durable_metadata (its seq) and writes at least one row.
	assert.ok(s.warm.commits > 0 && s.warm.rowsWritten >= 2 * s.warm.commits && s.warm.writes > 0, JSON.stringify(s))
	assert.ok(s.warm.reads > 0 && s.warm.rowsRead > 0, JSON.stringify(s))
	assert.equal(s.warm.crossings, 0)
	assert.equal(s.warm.statements, s.warm.reads + s.warm.writes)
})

test("a Wasm core's crossings are counted, and only the core's", { timeout: 120_000 }, async t => {
	const wasm = new URL("../../core/host/cf/dist/core-tinygo.wasm", import.meta.url)
	if (!(await import("node:fs")).existsSync(wasm)) return t.skip("the TinyGo core is not built (make -C durable/core build-tinygo)")
	const run = await benchRunner("wasm-tinygo")
	const out = mkdtempSync(join(tmpdir(), "counts-test-"))
	const file = join(out, "c.jsonl")
	const r = await run({ out, name: "c", turns: 3, tools: 2, variant: "incr", env: { CONTRACT_COUNTS: file } })
	assert.equal(r.code, 0, r.stderr)
	const s = summarize(readFileSync(file, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l)))
	// Each warm turn runs steps of the core (exports) that read and write through the host (imports).
	assert.ok(s.warm.crossings >= 2 * s.warm.commits && s.warm.crossings < 10_000, JSON.stringify(s))
	assert.ok(s.linear >= 65536 && s.heap > 0, JSON.stringify(s))
	// alloc_stats reaches the counts: its three counters per phase, the completed collections far fewer than the allocations.
	for (const phase of [s.cold, s.warm]) {
		for (const k of ["allocBytes", "mallocs", "gcs"]) assert.ok(Number.isInteger(phase[k]) && phase[k] >= 0, `${k}: ${JSON.stringify(s)}`)
		assert.ok(phase.mallocs > 0 && phase.gcs < phase.mallocs, JSON.stringify(s))
	}
	// The bytes that crossed are the payload of the events and steps of those turns, and a crossing allocates in the core.
	assert.ok(s.warm.bytesCrossed > 1000 && s.warm.allocsPerCrossing > 1, JSON.stringify(s))
})

test("the native counts take valgrind only from VALGRIND: without it they refuse, never fall back to another release", () => {
	const saved = process.env.VALGRIND
	try {
		delete process.env.VALGRIND
		assert.throws(() => valgrind(), /pinned valgrind: sh durable\/contract\/tools\/build-valgrind\.sh/)
		process.env.VALGRIND = "/opt/vg/bin/valgrind"
		assert.equal(valgrind(), "/opt/vg/bin/valgrind")
	} finally {
		if (saved === undefined) delete process.env.VALGRIND
		else process.env.VALGRIND = saved
	}
})

test("only a core's calls are crossings: an instance that imports pig counts its export and import calls, others none", async () => {
	const Instance = WebAssembly.Instance
	const key = Symbol.for("pig.contract.counts")
	try {
		await install()
		// (import "pig" "f" (func)) (func (export "g") call 0): one export call that makes one import call.
		const core = new Uint8Array([
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
			0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
			0x02, 0x09, 0x01, 0x03, 0x70, 0x69, 0x67, 0x01, 0x66, 0x00, 0x00,
			0x03, 0x02, 0x01, 0x00,
			0x07, 0x05, 0x01, 0x01, 0x67, 0x00, 0x01,
			0x0a, 0x06, 0x01, 0x04, 0x00, 0x10, 0x00, 0x0b,
		])
		let called = 0
		const g = new WebAssembly.Instance(new WebAssembly.Module(core), { pig: { f: () => { called++ } } }).exports.g
		// (func (export "h")): a module that does not import pig.
		const plain = new Uint8Array([
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
			0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
			0x03, 0x02, 0x01, 0x00,
			0x07, 0x05, 0x01, 0x01, 0x68, 0x00, 0x00,
			0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
		])
		const other = new WebAssembly.Instance(new WebAssembly.Module(plain)).exports
		other.h(); other.h()
		assert.equal(counters().crossings, 0, "a module that does not import pig is not the core")
		g()
		assert.equal(called, 1)
		assert.equal(counters().crossings, 2)
	} finally {
		WebAssembly.Instance = Instance
		delete globalThis[key]
	}
})

test("native live growth is the median over the replay processes: one-off objects either way are not growth, every-turn retention is", () => {
	assert.ok(NATIVE_LIVE_RUNS >= 9 && NATIVE_LIVE_RUNS % 2 === 1)
	// dcore-int's eight processes of one binary at 50 turns, plus one that read 37 and one that lost an object.
	assert.equal(nativeLiveGrowth([16, 0, 0, 16, 0, 16, 0, 37, -5]), 0)
	assert.equal(nativeLiveGrowth([-5, -4, 0, 0, 0, 0, 16, -5, 8]), 0)
	// A core that keeps 40 B a turn shows it in every process, the odd one-off object on top or missing.
	assert.equal(nativeLiveGrowth([40, 56, 40, 35, 61, 40, 40, 56, 40]), 40)
	assert.equal(nativeLiveGrowth([10, 20]), 15)
	assert.throws(() => nativeLiveGrowth([]), /no native replay figures/)
})

test("the replay settles the native live growth on the median of NATIVE_LIVE_RUNS processes; other cells are untouched", async () => {
	// The first process caught a one-off 37 B/turn object; the eight others read the core's own 0 or 16.
	const later = [0, 16, 0, 0, 16, 0, -5, 0]
	const cells = { "native/50": { "warm.liveGrowthPerTurn": 37, heap: 1 }, "wasm-go/50": { "warm.liveGrowthPerTurn": 37 } }
	let calls = 0
	await settleNativeLiveGrowth(cells, 50, async () => ({ "native/50": { "warm.liveGrowthPerTurn": later[calls++] } }))
	assert.equal(calls, NATIVE_LIVE_RUNS - 1)
	assert.deepEqual(cells, { "native/50": { "warm.liveGrowthPerTurn": 0, heap: 1 }, "wasm-go/50": { "warm.liveGrowthPerTurn": 37 } })
	// No native figure (a run without the native build): no extra process.
	const wasmOnly = { "wasm-go/50": { "warm.liveGrowthPerTurn": 37 } }
	await settleNativeLiveGrowth(wasmOnly, 50, async () => { throw new Error("no native replay expected") })
	assert.deepEqual(wasmOnly, { "wasm-go/50": { "warm.liveGrowthPerTurn": 37 } })
})

test("ir: the wrapper runs the core under cachegrind with the clock-driven runtime work off; readIr sums the unclocked lines of every process", () => {
	const dir = mkdtempSync(join(tmpdir(), "counts-ir-"))
	const script = readFileSync(irWrapper("/core", "/vg/valgrind", join(dir, "w")), "utf8")
	assert.match(script, /^#!\/bin\/sh\nGODEBUG=asyncpreemptoff=1,gcstoptheworld=1 exec "\/vg\/valgrind" --tool=cachegrind -q --cache-sim=no --cachegrind-out-file="\$CONTRACT_COUNTS_IR\/cg\.%p" "\/core" "\$@"\n$/)
	assert.throws(() => readIr(dir), /no cachegrind output/)
	// The core's own code and the standard library it calls count; the runtime, the poller, the fifo I/O, timers and locks do not.
	writeFileSync(join(dir, "cg.11"), "desc: --cache-sim=no\ncmd: /core\nevents: Ir\nfl=./github.com/MichaelKinsy/PiG/durable/core/core.go\nfn=core.step\n1 500\n2 400\nfl=./runtime/proc.go\nfn=runtime.findRunnable\n7 90000\nfl=./encoding/json/encode.go\nfn=encoding/json.Marshal\n3 100\nfl=./runtime/metrics/sample.go\nfn=runtime/metrics.Read\n4 7\nfl=./internal/runtime/maps/map.go\nfn=x\n5 8\nfl=./internal/poll/fd_unix.go\nfn=y\n6 9\nfl=./os/file.go\nfn=z\n8 10\nfl=./runtimex/a.go\nfn=w\n9 34\nsummary: 91068\n")
	writeFileSync(join(dir, "cg.12"), "events: Ir\nfl=???\nfn=???\n0 200\nsummary: 200\n")
	writeFileSync(join(dir, "cg.12.1"), "events: Ir\nfl=a.go\n1 999999\nsummary: 999999\n")
	assert.equal(readIr(dir), 500 + 400 + 100 + 34 + 200)
	for (const [file, clocked] of [["./runtime/proc.go", true], ["runtime/asm_amd64.s", true], ["./internal/runtime/atomic/types.go", true], ["./sync/mutex.go", true], ["./time/sleep.go", true], ["./bufio/bufio.go", true], ["./math/bits/bits.go", true], ["./github.com/MichaelKinsy/PiG/durable/core/contracttest/guard/guard.go", true], ["./github.com/MichaelKinsy/PiG/durable/core/core.go", false], ["./encoding/json/encode.go", false], ["./math/big/nat.go", false], ["./internal/runtimex/a.go", false], ["./github.com/x/runtime/a.go", false], ["././<autogenerated>", false], ["???", false]]) assert.equal(irClocked(file), clocked, file)
	writeFileSync(join(dir, "cg.13"), "events: Ir\nfl=a.go\n1 5\nsummary: 6\n")
	assert.throws(() => readIr(dir), /add up to 5, not its summary 6/)
	writeFileSync(join(dir, "cg.13"), "events: Ir\n")
	assert.throws(() => readIr(dir), /no Ir total/)
	assert.equal(toleranceOf({}).native.ir, 0.0025)
	assert.equal(toleranceOf({}).wasm?.ir, undefined)
})

test("bar: growth per turn must be 0 and the heap must not follow the conversation; the reference is not judged", () => {
	const flat = { "warm.liveGrowthPerTurn": 0, heap: 1000 }
	const cells = { "wasm-go/50": flat, "wasm-go/3500": { ...flat, heap: 1000 * BAR_HEAP_SIZE_RATIO }, "native/50": flat, "pi/50": { heap: 1 } }
	assert.deepEqual(bar(cells), [])
	const grows = bar({ ...cells, "native/3500": { ...flat, "warm.liveGrowthPerTurn": 4096 } })
	assert.deepEqual(grows.map(f => f.rule), ["growth"])
	assert.match(grows[0].message, /native\/3500: the live set grows 4096 bytes per turn/)
	const follows = bar({ ...cells, "wasm-go/3500": { ...flat, heap: 1000 * BAR_HEAP_SIZE_RATIO + 1 } })
	assert.deepEqual(follows.map(f => f.rule), ["heap"])
	assert.equal(bar({ "native/50": { heap: 1 } })[0].rule, "growth", "a cell without the measurement fails: nothing passes by not measuring")
	// The native runtime's noise is not growth (0 to 16 B/turn between runs of a core that keeps nothing); a core that keeps more
	// fails, and the Wasm builds, which repeat exactly, have no noise.
	assert.deepEqual(bar({ ...cells, "native/1000": { ...flat, "warm.liveGrowthPerTurn": 16 }, "native/3500": { ...flat, "warm.liveGrowthPerTurn": -12 } }), [])
	assert.deepEqual(bar({ ...cells, "native/3500": { ...flat, "warm.liveGrowthPerTurn": 33 } }).map(f => f.key), ["native/3500"])
	assert.deepEqual(bar({ ...cells, "wasm-go/3500": { ...flat, heap: 1000, "warm.liveGrowthPerTurn": 1 } }).map(f => f.key), ["wasm-go/3500"])
	// The sizes are compared by turn count, not by the order of the cells.
	assert.deepEqual(bar({ "wasm-go/3500": { ...flat, heap: 2000 }, "wasm-go/50": flat }).map(f => f.rule), ["heap"])
})

test("bar: the committed baseline measures live growth and heap in every build's cells, so the bar can judge it without a run", () => {
	const baseline = JSON.parse(readFileSync(fileURLToPath(new URL("../counts.json", import.meta.url)), "utf8"))
	for (const [key, counts] of Object.entries(baseline.cells)) {
		if (key.startsWith("pi/")) continue
		assert.equal(typeof counts["warm.liveGrowthPerTurn"], "number", `${key} has warm.liveGrowthPerTurn`)
		assert.equal(typeof (counts.heapNet ?? counts.heap), "number", `${key} has heap (heapNet where it is gated)`)
	}
})

test("bar: a native cell's heap is its gated heapNet, so a heap net of the threads that follows the conversation fails", () => {
	const flat = { "warm.liveGrowthPerTurn": 0 }
	assert.deepEqual(bar({ "native/50": { ...flat, heapNet: 1000 }, "native/3500": { ...flat, heapNet: 1000 * BAR_HEAP_SIZE_RATIO } }), [])
	assert.deepEqual(bar({ "native/50": { ...flat, heapNet: 1000 }, "native/3500": { ...flat, heapNet: 1000 * BAR_HEAP_SIZE_RATIO + 1 } }).map(f => f.rule), ["heap"])
})

test("the live-set window gates the settled slope of the Wasm builds at 50 and 3,500 turns, under its own name", () => {
	assert.deepEqual(LIVE_WINDOW, { turns: 61, settle: 30, sizes: [50, 3500], impls: ["wasm-tinygo", "wasm-go"] })
	assert.equal(LIVE_WINDOW_METRIC, "warm.settledLiveGrowthPerTurn")
	const replayed = {
		"wasm-tinygo/50": { "warm.liveGrowthPerTurn": 336, "warm.instructions": 9 },
		"wasm-go/3500": { "warm.liveGrowthPerTurn": 0 },
		"native/50": { "warm.liveGrowthPerTurn": 16 },
		"wasm-go/50": { "warm.instructions": 9 },
	}
	assert.deepEqual(liveWindowCells(replayed), { "wasm-tinygo/50": { "warm.settledLiveGrowthPerTurn": 336 }, "wasm-go/3500": { "warm.settledLiveGrowthPerTurn": 0 } })
	// The window is a separate metric: a 6-turn reading of 0 does not hide it, and the Wasm cells have no slack.
	const base = { "wasm-tinygo/50": { "warm.liveGrowthPerTurn": 0, "warm.settledLiveGrowthPerTurn": 0 } }
	assert.deepEqual(compare(base, { "wasm-tinygo/50": { "warm.liveGrowthPerTurn": 0, "warm.settledLiveGrowthPerTurn": 1 } }).map(f => `${f.metric} ${f.kind}`), ["warm.settledLiveGrowthPerTurn higher"])
})
