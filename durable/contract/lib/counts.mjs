// Count budgets of a Durable core build (plan 2 item 9): what a turn costs in work that does not depend on the machine.
// A run opens a W1 fixture, runs a few measured turns of the scripted model, and counts per turn:
//   statements, reads, writes   SQL statements the store ran (reads and writes; lib/capsql.mjs)
//   rowsRead                    rows the reads returned
//   rowsWritten                 row changes the store committed to Pi's tables (the session changeset of each commit)
//   commits                     transactions that changed Pi's tables
//   crossings                   calls across the core boundary: export and import calls of a Wasm core (an instance that
//                               imports `pig`), messages to and from a native core
//   allocBytes, mallocs         what the core's runtime allocated (TotalAlloc and Mallocs; the core's alloc_stats). For a
//                               Go build allocBytes is runtime/metrics' /gc/heap/allocs:bytes, read after ReadMemStats
//                               flushes the cached spans (cmd/corenative main_test.go)
//   gcs                         the collections the core's runtime completed (NumGC; alloc_stats)
//   bytesCrossed                payload bytes that crossed the boundary: events and read answers in, steps and read requests
//                               out (a thin `hop` answer counts its 12 bytes, an empty one none); the volume per turn. A native
//                               core counts every payload on its pipe, so its figure also holds the uuidv7 answers, the abi_id
//                               and sql_table answers of its start (cold) and the statistics answers a measurement asks for
//                               (alloc_stats each sample, mem_stats when observed): compare a build with its own baseline
//   allocsPerCrossing           mallocs / crossings of the phase, two decimals (derived): what one crossing costs the core's allocator
// and at the end of the run:
//   heap                        the core's heap after a full collection (mem_stats slot 6); for a native core it is
//                               reported, and heapNet is gated
//   heapNet                     a native core's heap net of the Go runtime's per-thread objects: heap - threads x
//                               perThread (corenative heap_threads), where threads (reported) is the runtime's live OS
//                               threads and perThread (reported) the heap one more thread holds, measured in the same
//                               process. The runtime starts a thread when a goroutine blocks and no idle one is left,
//                               which follows the host's scheduling: about one run in eight under CPU contention has one
//                               more and 5,368 B more heap, none of it the core's
//   linear                      the Wasm cores' linear memory, the high-water mark (it only grows)
// The deterministic environment (lib/detenv.mjs) makes a run repeat exactly, so the gate compares exact numbers with a
// committed baseline (counts.json): any number above its baseline fails, and any number below it fails until the baseline
// is lowered in the same change (a ratchet: an improvement is kept).
import { appendFileSync, readdirSync, readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { pathToFileURL } from "node:url"

const KEY = Symbol.for("pig.contract.counts")
export const COUNTERS = ["statements", "reads", "writes", "rowsRead", "rowsWritten", "commits", "crossings", "allocBytes", "mallocs", "gcs", "bytesCrossed"]
export const FINALS = ["heap", "heapNet", "linear", "ir"]
/** End-of-run figures reported beside the cells, never part of the baseline (raw `heap` too, where heapNet is gated). */
export const REPORTED_FINALS = ["threads", "perThread"]

/** The live counters of this process, or undefined when counting is off (CONTRACT_COUNTS unset). */
export const counters = () => globalThis[KEY]

/**
 * Turn counting on (hooks/register.mjs, before the candidate's setup instantiates its core): zeroed counters, every Wasm
 * instance's imports and exports counted, and a native core's messages counted.
 */
export async function install() {
	const c = { statements: 0, reads: 0, writes: 0, rowsRead: 0, rowsWritten: 0, commits: 0, crossings: 0 }
	globalThis[KEY] = c
	const count = fn => function (...args) { c.crossings++; return fn.apply(this, args) }
	const wrapImports = imports => {
		if (!imports) return imports
		const out = {}
		for (const [mod, fns] of Object.entries(imports)) {
			out[mod] = {}
			for (const [name, v] of Object.entries(fns)) out[mod][name] = typeof v === "function" ? count(v) : v
		}
		return out
	}
	const Base = WebAssembly.Instance
	// Only a core counts: an instance that imports the `pig` module (ABI section 4.2). A dependency's own Wasm (the reference
	// loads some too) is not a crossing of the core boundary.
	class CountedInstance extends Base {
		#exports
		#core
		constructor(module, imports) {
			const core = Boolean(imports?.pig)
			super(module, core ? wrapImports(imports) : imports)
			this.#core = core
		}
		get exports() {
			if (!this.#core) return super.exports
			if (!this.#exports) {
				const raw = super.exports
				const out = {}
				for (const [name, v] of Object.entries(raw)) out[name] = typeof v === "function" ? count(v) : v
				this.#exports = Object.freeze(out)
			}
			return this.#exports
		}
	}
	WebAssembly.Instance = CountedInstance
	const candidate = process.env.CONTRACT_CANDIDATE
	if (candidate && process.env.DCORE_NATIVE) {
		const { NativeCore } = await import(new URL("../native.ts", pathToFileURL(`${candidate.replace(/\/$/, "")}/`)).href)
		const send = NativeCore.prototype.send
		NativeCore.prototype.send = function (...args) { c.crossings++; return send.apply(this, args) }
	}
}

/** What the cores report now: the summed alloc counters (when the build has alloc_stats) and, with `final`, heap and linear memory. */
export function coreStats(cores, { final = false } = {}) {
	const list = [...(cores ?? [])]
	const out = {}
	const alloc = list.filter(core => typeof core.allocStats === "function").map(core => core.allocStats())
	if (alloc.length) {
		out.allocBytes = alloc.reduce((s, a) => s + Number(a.bytes), 0)
		out.mallocs = alloc.reduce((s, a) => s + Number(a.mallocs), 0)
		// Read before heapStats below, whose full collection is the gate's own, not the turns'.
		if (alloc.every(a => a.gcs !== undefined)) out.gcs = alloc.reduce((s, a) => s + Number(a.gcs), 0)
	}
	const crossed = list.filter(core => typeof core.bytesCrossed === "number")
	if (crossed.length) out.bytesCrossed = crossed.reduce((s, core) => s + core.bytesCrossed, 0)
	if (final) {
		const heap = list.filter(core => typeof core.heapStats === "function").map(core => core.heapStats())
		if (heap.length) out.heap = heap.reduce((s, h) => s + h.heap, 0)
		const threaded = list.filter(core => typeof core.threadStats === "function").map(core => core.threadStats())
		if (threaded.length) {
			const unmeasured = threaded.find(t => !(t.perThread > 0))
			if (unmeasured) throw new Error(`counts: a native core did not measure the heap one thread holds (heap_threads perThread ${unmeasured.perThread}; its stderr says why)`)
			out.heapNet = threaded.reduce((s, t) => s + t.heap - t.threads * t.perThread, 0)
			out.threads = threaded.reduce((s, t) => s + t.threadsBefore, 0)
			// One binary serves a run, so its cores agree; the largest is reported if they do not.
			out.perThread = Math.max(...threaded.map(t => t.perThread))
		}
		const wasm = list.filter(core => typeof core.memoryBytes === "number")
		if (wasm.length) out.linear = wasm.reduce((s, core) => s + core.memoryBytes, 0)
	}
	return out
}

/** bench.mjs: one cumulative sample per measured turn, then the final line. */
export function sampler(file) {
	writeFileSync(file, "")
	const cores = () => globalThis[Symbol.for("pig.contract")]?.cores
	return {
		turn: turn => appendFileSync(file, JSON.stringify({ turn, ...counters(), ...coreStats(cores()) }) + "\n"),
		final: () => appendFileSync(file, JSON.stringify({ final: true, ...coreStats(cores(), { final: true }) }) + "\n"),
	}
}

/**
 * A run's counts from its samples (cumulative per measured turn): `cold` is everything up to the end of the first measured
 * turn (the open and the first turn), `warm` the sum over the other measured turns, then the final heap and linear memory.
 */
export function summarize(samples) {
	const turns = samples.filter(s => !s.final)
	const fin = samples.find(s => s.final) ?? {}
	if (turns.length < 2) throw new Error(`counts: ${turns.length} measured turns, need at least 2`)
	const first = turns[0], last = turns.at(-1)
	const pick = (s, base) => Object.fromEntries(COUNTERS.filter(k => typeof s[k] === "number").map(k => [k, s[k] - (base?.[k] ?? 0)]))
	const out = { warmTurns: turns.length - 1, cold: pick(first), warm: pick(last, first) }
	for (const phase of [out.cold, out.warm]) if (typeof phase.mallocs === "number" && phase.crossings > 0) phase.allocsPerCrossing = Math.round(phase.mallocs * 100 / phase.crossings) / 100
	for (const k of [...FINALS, ...REPORTED_FINALS]) if (typeof fin[k] === "number") out[k] = fin[k]
	return out
}

/**
 * The per-turn view of a summary: SQL statements, core heap allocations (mallocs) and bytes allocated (allocBytes; the Go
 * runtime's /gc/heap/allocs:bytes) per warm turn and the core's collections per 100 warm turns, for reports. The gate
 * ratchets the warm sums they come from, and `check` refuses a baseline measured with another turn count, so a per-turn
 * number moves exactly when its sum does: it is reported, not gated a second time.
 */
export function perTurn(summary) {
	const n = summary.warmTurns
	const out = {}
	if (typeof summary.warm?.statements === "number") out.statementsPerTurn = summary.warm.statements / n
	if (typeof summary.warm?.mallocs === "number") out.mallocsPerTurn = summary.warm.mallocs / n
	if (typeof summary.warm?.allocBytes === "number") out.allocBytesPerTurn = summary.warm.allocBytes / n
	if (typeof summary.warm?.gcs === "number") out.gcsPer100Turns = (summary.warm.gcs / n) * 100
	return out
}

/** Flatten a summary to "cold.rowsRead" style keys. */
export function flatten(summary) {
	const out = {}
	for (const phase of ["cold", "warm"]) for (const [k, v] of Object.entries(summary[phase] ?? {})) out[`${phase}.${k}`] = v
	for (const k of FINALS) if (typeof summary[k] === "number" && !(k === "heap" && typeof summary.heapNet === "number")) out[k] = summary[k]
	return out
}

/** The end-of-run figures reported, not gated: threads and perThread, and the raw heap of a core whose heapNet is gated. */
export function reported(summary) {
	const out = {}
	if (typeof summary.heapNet === "number" && typeof summary.heap === "number") out.heap = summary.heap
	for (const k of REPORTED_FINALS) if (typeof summary[k] === "number") out[k] = summary[k]
	return out
}

// The native core's runtime is not deterministic: it collects on its own threads, and map growth, map iteration order
// and GC-timed allocations vary between runs of the same tree. Over 8 runs on one CPU (GOMAXPROCS=1) of the core at
// dcore-int 1b794d8a22 its heap after a collection moved by up to 0.75%, its allocated bytes by up to 0.14% and its malloc
// count by up to 0.016%; every other number of every build, and every number of the Wasm builds, repeats exactly. The
// slack below is about 3x (heap and allocated bytes) and 15x (mallocs) those spreads; the Wasm Go build, which runs the
// same Go code, still catches an allocation change exactly.
// These are the floor: the baseline file's tolerance adds to them or widens them, so a baseline written before a metric
// existed still gets its slack, and a change to either is reviewed with the numbers.
// The native core's heap is read after two closing collections, so nothing only a sync.Pool holds counts. About one run in
// eight under CPU contention read 5,368 B more on the same binary (2.01% at 1,000 turns): the Go scheduler started one
// more OS thread (an M, whose runtime structures stay live). The gate therefore takes heapNet, the heap net of the threads
// at the heap the same process measures one more thread to hold; the raw heap and the thread count are reported. Over 80
// runs of 50, 1,000 and 3,500 turns 8 at a time on one CPU, and 9 alone, heapNet stayed between 32 B below and 352 B
// (0.136%) above its baseline (spreads 128, 160 and 384 B by size); its slack, 0.25% (about 650 B), is about 1.8x that,
// where the raw heap's 2% was 5.2 KB.
// The native core's whole-process instruction total follows the clock and the host's timing, not the core's work: the
// scheduler, the network poller, the guard's ticker, sysmon, the scavenger and the reads and writes of the request fifos
// run more often the longer a run takes. Over 15 runs at 1,000 turns (dcore-int a311d05e75, quiet and loaded hosts) the total
// moved by 3.2% and followed wall time. `ir` therefore counts only the instructions of the code outside those packages
// (IR_CLOCKED): over 33 runs of 50, 1,000 and 3,500 turns, with one and with two closing collections, it moved by at most
// 0.06%, the rest coming from Go's randomized map seeds. Its slack, 0.25%, is about 4x that; a change in the code's own
// work fails it, while allocation and collection work, which `ir` no longer sees, is gated by allocBytes, mallocs and gcs.
/**
 * Native instruction counts (`ir`: instructions executed, the Ir event of cachegrind and callgrind). The native core
 * process runs under valgrind's cachegrind (instruction counting only, no cache simulation) over its whole life: start,
 * the open, every measured turn and the final statistics, on all of its threads. Per-turn dumps are not reliable for a Go
 * process (callgrind's --dump-before misses or repeats entries across goroutine stack switches), so `ir` is one number per
 * cell, like `heap`. The Go runtime runs with GODEBUG=asyncpreemptoff=1,gcstoptheworld=1: preemption signals and background
 * or idle-time mark work follow the clock, and with them the total moved by up to 67% between runs. Returns a script for
 * DCORE_NATIVE that runs `exe` so, writing its output files under $CONTRACT_COUNTS_IR.
 */
export function irWrapper(exe, valgrind, file) {
	writeFileSync(file, `#!/bin/sh\nGODEBUG=asyncpreemptoff=1,gcstoptheworld=1 exec "${valgrind}" --tool=cachegrind -q --cache-sim=no --cachegrind-out-file="$CONTRACT_COUNTS_IR/cg.%p" "${exe}" "$@"\n`, { mode: 0o755 })
	return file
}

/**
 * The Go packages whose instructions follow the clock and the host's timing (see the tolerance note above): the runtime, the
 * poller, the system-call and file layers the request fifos go through, timers, locks, the runtime's random source, and the
 * gate's guard, whose watcher checks the process every 100 ms (8 runs at a time on one CPU read its ticks as +0.4..0.57% ir).
 * A source file belongs to a package by its directory (`-trimpath` names: `runtime/proc.go`); every subpackage of `runtime`
 * and `internal/runtime` is the runtime's.
 */
export const IR_CLOCKED = ["runtime", "internal/runtime", "internal/poll", "internal/syscall", "syscall", "os", "time", "bufio", "sync", "internal/sync", "math/bits", "internal/chacha8rand", "github.com/MichaelKinsy/PiG/durable/core/contracttest/guard"]

/** Whether the instructions of source file `file` (a cachegrind `fl=` name) are clocked: in a package of IR_CLOCKED. */
export function irClocked(file) {
	const path = file.replace(/^(\.\/)+/, "")
	const dir = path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ""
	return IR_CLOCKED.some(pkg => dir === pkg || ((pkg === "runtime" || pkg === "internal/runtime") && dir.startsWith(`${pkg}/`)))
}

/**
 * The instructions of every core process of a run (irWrapper's output files) outside IR_CLOCKED: the sum, over the
 * processes, of the Ir counts of every source line whose file is not clocked. A file without counts, or whose counts do
 * not add up to its own summary line, is refused.
 */
export function readIr(dir) {
	let total = 0, files = 0
	for (const f of readdirSync(dir)) {
		if (!/^cg\.\d+$/.test(f)) continue
		const path = join(dir, f)
		let all = 0, counted = 0, clocked = false, summary = null
		for (const line of readFileSync(path, "utf8").split("\n")) {
			if (line.startsWith("fl=")) clocked = irClocked(line.slice(3))
			else if (line.startsWith("summary: ")) summary = Number(line.slice(9))
			else if (/^\d/.test(line)) {
				const ir = Number(line.split(" ")[1])
				all += ir
				if (!clocked) counted += ir
			}
		}
		if (summary === null) throw new Error(`counts: no Ir total in ${path}`)
		if (all !== summary) throw new Error(`counts: the Ir lines of ${path} add up to ${all}, not its summary ${summary}`)
		total += counted
		files++
	}
	if (!files) throw new Error(`counts: no cachegrind output in ${dir}`)
	return total
}

// The native core's collections (`gcs`) follow its pacer, which triggers on the allocated bytes that move by up to 0.14%:
// over 4 runs at 50 and 3,500 turns (with and without cachegrind) it completed 1 collection cold and 4 warm every time,
// but a run whose allocation crosses a trigger would complete one more or one fewer. Its slack, 25%, admits one
// collection either way in the 4 warm ones and none in the cold one; the Wasm Go build runs the same Go code and its
// count is exact.
// The replay counts (lib/instr.mjs) add the native core's instructions under callgrind (1%; measured over repeated runs 0.02% to
// 0.4%) and its live-set growth per turn (5%; measured 1% to 3%), which move with Go's randomized map iteration. The Wasm
// builds' instructions repeat exactly on every machine (the toolchains are pinned, and a toolchain update rewrites the baseline
// like every other count), so they have no slack.
export const DEFAULT_TOLERANCE = {
	native: { heap: 0.02, heapNet: 0.0025, allocBytes: 0.005, mallocs: 0.0025, allocsPerCrossing: 0.0025, ir: 0.0025, instructions: 0.01, liveGrowthPerTurn: 0.05, gcs: 0.25 },
}
// A floor in the metric's own units under the relative slack, for a count whose baseline is near zero: the native live set
// moves by a few bytes per turn between runs of a core that retains nothing per turn (measured 0 to 16 B/turn over six
// runs, where 5% of zero is no slack at all). 32 B/turn is under the 49 B/turn 5% gave the 984 B/turn the replay's own
// bookkeeping read as before, and a core keeping even 160 bytes per turn exceeds it.
export const ABSOLUTE_TOLERANCE = {
	native: { liveGrowthPerTurn: 32 },
}
/** The tolerance a check applies: DEFAULT_TOLERANCE, widened by the baseline file's own. */
export const toleranceOf = base => {
	const out = structuredClone(DEFAULT_TOLERANCE)
	for (const [impl, metrics] of Object.entries(base?.tolerance ?? {})) for (const [m, rel] of Object.entries(metrics)) (out[impl] ??= {})[m] = Math.max(rel, out[impl][m] ?? 0)
	return out
}

/**
 * The ratchet. `baseline` and `measured` map "impl/size" to flattened counts. Returns the failures: a number above its
 * baseline (`higher`), below it (`lower`: lower the baseline), a metric or cell the baseline lacks (`new`), or a baseline
 * metric the run did not produce (`missing`). `tolerance` maps an implementation to {metric: relative slack} (default
 * exact; metric without its cold./warm. prefix): a native core's runtime collects on its own threads, so its heap after a
 * collection moves by a few KiB between runs.
 */
export function compare(baseline, measured, { tolerance = {}, absolute = ABSOLUTE_TOLERANCE } = {}) {
	const failures = []
	for (const [cell, got] of Object.entries(measured)) {
		const want = baseline[cell]
		if (!want) { failures.push({ cell, kind: "new", metric: "*", got: undefined, want: undefined }); continue }
		for (const [metric, g] of Object.entries(got)) {
			const w = want[metric]
			if (w === undefined) { failures.push({ cell, kind: "new", metric, got: g, want: w }); continue }
			const impl = cell.split("/")[0], bare = metric.replace(/^(cold|warm)\./, "")
			const slack = Math.max(Math.abs(w) * (tolerance[impl]?.[bare] ?? 0), absolute[impl]?.[bare] ?? 0)
			if (g > w + slack) failures.push({ cell, kind: "higher", metric, got: g, want: w })
			else if (g < w - slack) failures.push({ cell, kind: "lower", metric, got: g, want: w })
		}
		for (const metric of Object.keys(want)) if (!(metric in got)) failures.push({ cell, kind: "missing", metric, got: undefined, want: want[metric] })
	}
	return failures
}

/** "wasm-tinygo/3500 warm.rowsRead 2490 > 2480 (+0.4%)" */
export function describe(f) {
	if (f.metric === "*") return `${f.cell}: no baseline for this cell`
	if (f.kind === "new") return `${f.cell} ${f.metric}: ${f.got} has no baseline`
	if (f.kind === "missing") return `${f.cell} ${f.metric}: baseline ${f.want}, not measured`
	const pct = f.want ? ` (${f.got > f.want ? "+" : ""}${(((f.got - f.want) / f.want) * 100).toFixed(1)}%)` : ""
	return `${f.cell} ${f.metric} ${f.got} ${f.kind === "higher" ? ">" : "<"} ${f.want}${pct}`
}

/** Increments for lib/capsql.mjs; a no-op when counting is off. */
export function countRead(rows) { const c = globalThis[KEY]; if (c) { c.statements++; c.reads++; c.rowsRead += rows } }
export function countWrite() { const c = globalThis[KEY]; if (c) { c.statements++; c.writes++ } }
export function countCommit(changes) { const c = globalThis[KEY]; if (c) { c.commits++; c.rowsWritten += changes } }

/**
 * The Durable bar for 0.5.0, item 3: memory is bounded by the model's context window, never by conversation length. A core that
 * keeps something per turn fails, whatever its baseline says: a baseline records what a build costs, the bar says what it may cost,
 * and no entry here may be raised to pass. Two rules over the cells ("impl/size" to flattened counts, the replay cells included):
 *  - `warm.liveGrowthPerTurn` is 0: the live set after a full collection does not grow from one warm turn to the next. The Wasm
 *    builds repeat exactly, so theirs is 0 exactly; the native runtime's live set moves by a few bytes per turn between runs of a
 *    core that keeps nothing (ABSOLUTE_TOLERANCE, measured 0 to 16 B/turn), so a native reading within that floor is 0;
 *  - `heap` (the live heap after the last turn) does not depend on how long the conversation is: the largest fixture's heap is at
 *    most BAR_HEAP_SIZE_RATIO of the smallest's, so a heap that follows the history fails (a lazily opened window, whose size is the
 *    same for every history, passes). A native cell's heap is `heapNet`, the heap the gate takes (the raw heap is reported only).
 * Returns the failures; [] means the bar holds.
 */
export const BAR_HEAP_SIZE_RATIO = 1.1
export function bar(cells) {
	const failures = []
	const bySize = {}
	for (const [key, counts] of Object.entries(cells)) {
		const [impl, size] = key.split("/")
		if (impl === "pi") continue
		const growth = counts["warm.liveGrowthPerTurn"]
		if (growth === undefined) failures.push({ key, rule: "growth", message: `${key}: warm.liveGrowthPerTurn is not measured` })
		else if (Math.abs(growth) > (ABSOLUTE_TOLERANCE[impl]?.liveGrowthPerTurn ?? 0)) failures.push({ key, rule: "growth", message: `${key}: the live set grows ${growth} bytes per turn (the bar is 0${ABSOLUTE_TOLERANCE[impl]?.liveGrowthPerTurn ? `, within the ${ABSOLUTE_TOLERANCE[impl].liveGrowthPerTurn} B/turn noise of the ${impl} runtime` : ""})` })
		const heap = counts.heapNet ?? counts.heap
		if (heap !== undefined) (bySize[impl] ??= []).push([Number(size), heap])
	}
	for (const [impl, points] of Object.entries(bySize)) {
		points.sort((a, b) => a[0] - b[0])
		const [[smallSize, small], [largeSize, large]] = [points[0], points.at(-1)]
		if (points.length > 1 && large > small * BAR_HEAP_SIZE_RATIO) failures.push({ key: impl, rule: "heap", message: `${impl}: the heap is ${large} bytes at ${largeSize} turns and ${small} at ${smallSize} (${(large / small).toFixed(2)}x; the bar is ${BAR_HEAP_SIZE_RATIO}x): memory follows the conversation, not the context window` })
	}
	return failures
}
