// Replay counts (plan 2 item 9): what the core costs on a recorded workload, which no machine, clock or load changes.
//
// counts.mjs counts rows, crossings, heap and linear memory on the full host. This adds, from durable/core/contracttest/counts:
//   instructions    Wasm instructions the core executed (wasm-tinygo, wasm-go: the shipped module instrumented with a counter per
//                   basic block, run under wazero; durable/core/contracttest/fuel), and, for native, the instructions of the
//                   core's steps under callgrind (Valgrind). cold.instructions and warm.instructions for the Wasm builds; the
//                   whole run for native.
//   allocCalls, allocRequestedBytes   calls of the Wasm allocator and the bytes they asked for (TinyGo's runtime.alloc, by
//                   function name; the Go build keeps no names, so it has none). The runtime's own totals stay with counts.mjs.
//   warm.liveGrowthPerTurn   bytes the live set grows per warm turn after a full collection: zero when a turn with no live work
//                   retains nothing. It is the slope between the lowest readings of the two halves of the warm turns
//                   (counts.liveGrowth), so a conservative collector's one falsely retained object is not growth
// The workload is the bench run of a fixture on the native stack, recorded as everything the core was given (events, pig.read
// answers, uuids). The core is a pure function of that stream, so each build replays it with no storage and no clock, and every
// step is compared with the step the recorded run produced before any count is kept.
import { execFile as execFileCb } from "node:child_process"
import { mkdirSync, readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { promisify } from "node:util"
import { CONTRACT_DIR } from "./run.mjs"
import { copyStore } from "./store.mjs"

const execFile = promisify(execFileCb)
const REPO = join(CONTRACT_DIR, "..", "..")
// The Go programs build through the tree's go.work, as the Makefile's gates and the k8s bundle build them: the root module
// uses the workspace's extensions/sdk, which can be newer than its published require. An empty GOWORK selects the go.work
// found from the working directory, so a caller's GOWORK=off does not apply.
const GO_ENV = { ...process.env, GOWORK: "", GOTOOLCHAIN: process.env.GOTOOLCHAIN ?? `go${readFileSync(join(REPO, ".go-version"), "utf8").trim()}` }

/**
 * Valgrind: the pinned release from tools/build-valgrind.sh, named by $VALGRIND (its tools and libexec come from the same
 * prefix). The count gate refuses to run without it rather than count with another release; the baseline names the release.
 */
export function valgrind() {
	const exe = process.env.VALGRIND
	if (!exe) throw new Error("counts: the native counts need the pinned valgrind: sh durable/contract/tools/build-valgrind.sh PREFIX, then VALGRIND=PREFIX/bin/valgrind")
	return exe
}

// The native replay's live set after a collection also holds one-off objects the Go runtime makes at a random point of each
// process: a type-assertion cache it rebuilds on a random 1 in 1,024 misses (runtime typeAssert) and keeps, and buffers whose
// liveness moves between readings. Each is 16 to 64 B at a random turn, so over the five warm turns one process reads -5 to
// 37 B/turn where the next reads 0 (dcore-int: 16 0 0 16 0 16 0 16 at 50 turns on one binary; 37 failed the 32 B floor).
// Retention the core makes every turn shows in every process. The native figure is therefore the median of NATIVE_LIVE_RUNS
// replay processes of the one recording (each about 0.1 s): the least would read the processes that lost an object
// (-4, -5). The Wasm builds' figures are exact and come from one replay.
export const NATIVE_LIVE_RUNS = 9

/** The native warm.liveGrowthPerTurn of a recording from the figures of its replay processes: their median. */
export function nativeLiveGrowth(figures) {
	if (figures.length === 0) throw new Error("counts: no native replay figures")
	const sorted = [...figures].sort((a, b) => a - b)
	const mid = sorted.length >> 1
	return sorted.length % 2 ? sorted[mid] : Math.round((sorted[mid - 1] + sorted[mid]) / 2)
}

/**
 * Replaces the native cell's warm.liveGrowthPerTurn of `size` in `cells` (the figure of the first replay process) with the
 * median over NATIVE_LIVE_RUNS processes: `measureAgain` runs one more replay process of the same recording and resolves to
 * its `countgate measure` output. A cells map without that figure is left as it is.
 */
export async function settleNativeLiveGrowth(cells, size, measureAgain) {
	const native = `native/${size}`
	if (cells[native]?.["warm.liveGrowthPerTurn"] === undefined) return
	const growth = [cells[native]["warm.liveGrowthPerTurn"]]
	for (let i = 1; i < NATIVE_LIVE_RUNS; i++) growth.push((await measureAgain())[native]["warm.liveGrowthPerTurn"])
	cells[native]["warm.liveGrowthPerTurn"] = nativeLiveGrowth(growth)
}

// The live-set window. The contract's own run has 6 turns, so its warm.liveGrowthPerTurn compares the lowest of warm turns 1-2
// with the lowest of 4-5. A live set that keeps a few hundred bytes a turn in steps that a 5-turn window straddles reads 0 there.
// The window replays a 61-turn recording (one cold and 60 warm turns) of the smallest and the largest fixture through both Wasm
// builds, leaves the first 30 warm turns out (countgate measure --settle, counts.SettleWarm), and keeps the same halves-minimum
// slope over the last 30 as warm.settledLiveGrowthPerTurn. Settling keeps a bounded step out: on TinyGo at 50 turns the live set
// rises by 10,176 B in three steps by warm turn 19 and is flat for the next 100, which an unsettled 30-turn slope reads as
// 336 B/turn. Retention that every turn adds shows in the settled turns as well. A bounded step after warm turn 30 reads as
// growth: the window fails closed, and the README says why it is not to be widened. Native is not in
// the window: its 6-turn figure already comes from nine processes (NATIVE_LIVE_RUNS).
export const LIVE_WINDOW = { turns: 61, settle: 30, sizes: [50, 3500], impls: ["wasm-tinygo", "wasm-go"] }
export const LIVE_WINDOW_METRIC = "warm.settledLiveGrowthPerTurn"

/** The cells of the live-set window: each replayed cell's warm.liveGrowthPerTurn as LIVE_WINDOW_METRIC, for the window's builds. */
export function liveWindowCells(replayed) {
	const out = {}
	for (const [cell, m] of Object.entries(replayed)) {
		if (!LIVE_WINDOW.impls.includes(cell.split("/")[0]) || m["warm.liveGrowthPerTurn"] === undefined) continue
		out[cell] = { [LIVE_WINDOW_METRIC]: m["warm.liveGrowthPerTurn"] }
	}
	return out
}

/**
 * The replay cells of each size: "impl/size" to {"cold.instructions": n, ...}, for the builds in `impls`. `fixtures` maps a size
 * to its W1 fixture; the recording runs `turns` measured turns, as the contract's own cells do. `settle` leaves the first warm
 * turns out of the Wasm builds' warm phase (LIVE_WINDOW).
 */
export async function replayCells({ sizes, impls, fixtures, turns, tools, variant, out, jobs = 2, settle = 0 }) {
	const bin = join(out, "bin")
	mkdirSync(bin, { recursive: true })
	// -buildvcs=false: countgate's native core is the callgrind subject; a VCS stamp would tie its count to the checkout.
	await execFile("go", ["build", "-buildvcs=false", "-o", join(bin, "bench"), "./durable/core/sqlhost/cmd/bench"], { cwd: REPO, env: GO_ENV })
	await execFile("go", ["build", "-buildvcs=false", "-o", join(bin, "countgate"), "./durable/core/contracttest/cmd/countgate"], { cwd: REPO, env: GO_ENV })
	const wasm = {}
	for (const [impl, target, flag] of [["wasm-tinygo", "build-tinygo", "--tinygo"], ["wasm-go", "build-go", "--go"]]) {
		if (!impls.includes(impl)) continue
		await execFile("make", ["-s", "-C", "durable/core", target], { cwd: REPO, env: process.env })
		wasm[flag] = join(REPO, "durable", "core", "host", "cf", "dist", `core-${impl.slice(5)}.wasm`)
	}
	const vg = impls.includes("native") ? valgrind() : undefined
	const cells = {}
	let next = 0
	async function worker() {
		for (;;) {
			const size = sizes[next++]
			if (size === undefined) return
			const dir = join(out, `replay-${size}`)
			rmSync(dir, { recursive: true, force: true })
			mkdirSync(join(dir, "home"), { recursive: true })
			const db = join(dir, "store.sqlite"), rec = join(dir, "run.rec")
			copyStore(fixtures[size], db)
			// A run never touches a lane's or a user's agent directory or credentials.
			const env = { PATH: process.env.PATH, HOME: join(dir, "home"), PIG_CODING_AGENT_DIR: join(dir, "agent"), PI_CODING_AGENT_DIR: join(dir, "agent"), PIG_HOME: join(dir, "pig"), PI_HOME: join(dir, "pig"), TZ: "UTC", DETENV_EPOCH: "1800000000000", GOMAXPROCS: "1" }
			await execFile(join(bin, "bench"), ["--db", db, "--trace", join(dir, "run.trace"), "--record", rec, "--turns", String(turns), "--tools", String(tools), "--variant", variant], { env, maxBuffer: 1 << 26 })
			const measured = JSON.parse((await execFile(join(bin, "countgate"), ["measure", "--rec", rec, "--size", String(size), "--settle", String(settle), ...Object.entries(wasm).flat()], { env, maxBuffer: 1 << 26 })).stdout)
			const add = found => { for (const [k, v] of Object.entries(found)) cells[k] = { ...cells[k], ...v } }
			add(measured)
			if (impls.includes("native")) await settleNativeLiveGrowth(cells, size, async () => JSON.parse((await execFile(join(bin, "countgate"), ["measure", "--rec", rec, "--size", String(size)], { env, maxBuffer: 1 << 26 })).stdout))
			if (vg) add(JSON.parse((await execFile(join(bin, "countgate"), ["instructions", "--rec", rec, "--size", String(size)], { env: { ...env, VALGRIND: vg }, maxBuffer: 1 << 26 })).stdout))
			rmSync(db, { force: true })
		}
	}
	await Promise.all(Array.from({ length: Math.max(1, jobs) }, worker))
	// `measure` reports every build it was given; the native cell has the allocation counts, and only the builds asked for count.
	const wanted = Object.fromEntries(Object.entries(cells).filter(([k]) => impls.includes(k.split("/")[0])).map(([k, v]) => [k, Object.fromEntries(Object.entries(v).sort())]))
	return wanted
}
