#!/usr/bin/env node
// counts: deterministic count budgets of the Durable core builds with a ratchet (lib/counts.mjs).
//   counts run    [options]   measure and print
//   counts check  [options]   measure and compare with the baseline; exits 1 when a number is above its baseline, below it
//                             (lower the baseline in the same change: `counts update`), new or missing
//   counts update [options]   measure and write the baseline
//   counts bar    [options]   measure and judge the Durable 0.5.0 memory bar: no live-set growth per turn, heap independent of
//                             conversation length (--from FILE judges a counts file without measuring)
// Options:
//   --impls wasm-tinygo,wasm-go,native   builds (impls.toml; built first: make -C durable/core build-tinygo build-go,
//                                        sh durable/core/budget/build-native.sh). `pi` measures the reference; it has no
//                                        core, so only the store counts apply, and check never judges it.
//   --sizes 50,1000,3500   W1 fixtures (cli/fixtures.mjs seeds a missing one with pi-durable first)
//   --turns 6              measured turns: the first is the cold turn, the rest are the warm turns
//   --tools 8 --variant incr
//   -j 3                   runs at a time (the counts do not depend on load)
//   --ir 1|0               run the native core under valgrind's cachegrind and count `ir`, its instructions executed
//                          (default 1). The native counts use one valgrind, the pinned release named by $VALGRIND
//                          (tools/build-valgrind.sh); the run refuses without it.
//   --baseline <file>      default durable/contract/counts.json
//   --no-replay            skip the replay counts (lib/instr.mjs: instructions, allocations, live-set growth, and the
//                          live-set window: the last 30 of 60 warm turns of the Wasm builds at 50 and 3,500); `run` only: check
//                          and update would then lack numbers the baseline holds
//   --out <dir>            run directory (default: a temp dir)
//   --json <file>          the measured counts
import { execFileSync } from "node:child_process"
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { bar, compare, describe, flatten, irWrapper, perTurn, readIr, reported, summarize, toleranceOf } from "../lib/counts.mjs"
import { resolveImpl } from "../lib/gate.mjs"
import { LIVE_WINDOW, liveWindowCells, replayCells, valgrind } from "../lib/instr.mjs"
import { benchRunner } from "../lib/robust.mjs"
import { CONTRACT_DIR } from "../lib/run.mjs"
import { seed } from "./fixtures.mjs"

// The native core's heap also grows with its runtime's processor count (per-processor caches, about 28 KB each), which
// otherwise follows the CPUs the gate runs on: one fixed count gives the same number on every machine.
const GOMAXPROCS = "1"

const [cmd, ...rest] = process.argv.slice(2)
const flag = name => rest.includes(`--${name}`)
const opt = (name, fallback) => { const i = rest.findIndex(a => a === `--${name}` || a === `-${name}`); return i < 0 ? fallback : rest[i + 1] }
if (!["run", "check", "update", "bar"].includes(cmd)) {
	console.error("usage: counts run|check|update|bar [--impls a,b] [--sizes 50,1000,3500] [--turns 6] [-j 3] [--baseline file] [--out dir] [--json file]")
	process.exit(2)
}
// bar: the Durable 0.5.0 bar's memory rules (lib/counts.mjs bar) over a fresh measurement, or over a counts file with --from
// (the committed baseline must satisfy the bar too: `bar --from durable/contract/counts.json` measures nothing).
const reportBar = cells => {
	const failures = bar(cells)
	for (const f of failures) console.error(`FAIL bar: ${f.message}`)
	console.log(failures.length ? `counts: the bar fails ${failures.length} rule(s)` : "counts: the memory bar holds")
	process.exit(failures.length ? 1 : 0)
}
if (cmd === "bar" && opt("from")) reportBar(JSON.parse(readFileSync(opt("from"), "utf8")).cells)
const impls = opt("impls", "wasm-tinygo,wasm-go,native").split(",")
const sizes = opt("sizes", "50,1000,3500").split(",").map(Number)
const turns = Number(opt("turns", "6")), tools = Number(opt("tools", "8")), variant = opt("variant", "incr")
const jobs = Number(opt("j", "3"))
const baselineFile = opt("baseline", join(CONTRACT_DIR, "counts.json"))
const out = opt("out", mkdtempSync(join(tmpdir(), "contract-counts-")))
mkdirSync(out, { recursive: true })

const fixtures = {}
for (const size of sizes) fixtures[size] = await seed(size)
const runners = Object.fromEntries(await Promise.all(impls.map(async impl => [impl, await benchRunner(impl)])))

// The native build runs under valgrind's cachegrind for `ir` (lib/counts.mjs irWrapper). The baseline's native numbers are
// measured that way: without it (`--ir 0`, `run` only, since check and update need `ir`) heapNet reads 300-430 B (0.12-0.17%)
// lower, most of its 0.25% slack, while the allocation counts stay within 0.03%.
// The replay's native instructions use the same valgrind, so its release is the one the baseline names.
let valgrindVersion
const irEnv = {}
if (impls.includes("native") && (opt("ir", "1") === "1" || !flag("no-replay"))) {
	const vg = valgrind()
	try { valgrindVersion = execFileSync(vg, ["--version"], { encoding: "utf8" }).trim() } catch (e) { throw new Error(`counts: VALGRIND=${vg} does not run: ${e.message}`) }
	if (opt("ir", "1") === "1") {
		const exe = (await resolveImpl("native")).env?.DCORE_NATIVE
		if (!exe) throw new Error("counts: the native impl names no DCORE_NATIVE")
		irEnv.native = { DCORE_NATIVE: irWrapper(exe, vg, join(out, "corenative-ir")) }
	}
}

const cells = impls.flatMap(impl => sizes.map(size => ({ impl, size })))
const measured = {}
const perCell = {}
const reportedCell = {}
const errors = []
const t0 = Date.now()
let next = 0
async function worker() {
	for (;;) {
		const cell = cells[next++]
		if (!cell) return
		const name = `counts-${cell.impl}-${cell.size}`
		const file = join(out, `${name}.counts.jsonl`)
		const ir = irEnv[cell.impl] && join(out, `${name}.ir`)
		if (ir) { rmSync(ir, { recursive: true, force: true }); mkdirSync(ir) }
		const r = await runners[cell.impl]({ out, name, store: fixtures[cell.size], turns, tools, variant, env: { CONTRACT_COUNTS: file, GOMAXPROCS, ...irEnv[cell.impl], ...(ir ? { CONTRACT_COUNTS_IR: ir } : {}) }, timeoutMs: 30 * 60_000 })
		if (r.code !== 0 || !existsSync(file)) {
			writeFileSync(join(out, `${name}.stderr`), String(r.stderr))
			errors.push(`${cell.impl}/${cell.size}: exited ${r.code} ${r.signal ?? ""}: ${String(r.stderr).slice(-600)} (all of stderr: ${join(out, `${name}.stderr`)})`)
			continue
		}
		const samples = readFileSync(file, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
		const summary = summarize(samples)
		measured[`${cell.impl}/${cell.size}`] = { ...flatten(summary), ...(ir ? { ir: readIr(ir) } : {}) }
		perCell[`${cell.impl}/${cell.size}`] = perTurn(summary)
		reportedCell[`${cell.impl}/${cell.size}`] = reported(summary)
		console.log(`${cell.impl}/${cell.size}: ${JSON.stringify(measured[`${cell.impl}/${cell.size}`])}`)
	}
}
await Promise.all(Array.from({ length: Math.max(1, jobs) }, worker))
console.log(`counts: ${Object.keys(measured).length} of ${cells.length} cells in ${Math.round((Date.now() - t0) / 1000)} s (turns ${turns}, tools ${tools}, variant ${variant}); runs in ${out}`)
// A cell that did not run is reported before the replay starts: the replay would fail on the same fixture and hide why.
if (errors.length) {
	for (const e of errors) console.error(`FAIL ${e}`)
	process.exit(1)
}
// Replay counts: the same fixtures, recorded once and replayed through each build (lib/instr.mjs).
if (!flag("no-replay") || cmd !== "run") {
	if (flag("no-replay")) { console.error("FAIL --no-replay is for `run`: check and update need every number of the baseline"); process.exit(2) }
	const replayed = await replayCells({ sizes, impls: impls.filter(i => i !== "pi"), fixtures, turns, tools, variant, out, jobs })
	for (const [k, v] of Object.entries(replayed)) {
		if (!measured[k]) { errors.push(`${k}: replay counts without the cell's own run`); continue }
		measured[k] = { ...measured[k], ...v }
		console.log(`${k} replay: ${JSON.stringify(v)}`)
	}
	// The live-set window (lib/instr.mjs LIVE_WINDOW): the last 30 of 60 warm turns of the Wasm builds at 50 and 3,500 turns.
	const windowSizes = LIVE_WINDOW.sizes.filter(s => sizes.includes(s)), windowImpls = LIVE_WINDOW.impls.filter(i => impls.includes(i))
	if (windowSizes.length && windowImpls.length) {
		const replayed = await replayCells({ sizes: windowSizes, impls: windowImpls, fixtures, turns: LIVE_WINDOW.turns, settle: LIVE_WINDOW.settle, tools, variant, out: join(out, "live-window"), jobs })
		for (const [k, v] of Object.entries(liveWindowCells(replayed))) {
			if (!measured[k]) { errors.push(`${k}: live-window counts without the cell's own run`); continue }
			measured[k] = { ...measured[k], ...v }
			console.log(`${k} live window: ${JSON.stringify(v)}`)
		}
	}
}
// One order for the files, whatever order the runs finished in.
const ordered = Object.fromEntries(cells.map(c => `${c.impl}/${c.size}`).filter(k => measured[k]).map(k => [k, measured[k]]))
const opts = { turns, tools, variant, ...(valgrindVersion ? { valgrind: valgrindVersion } : {}) }
// Per warm turn (lib/counts.mjs perTurn): reported beside the cells, never part of the baseline.
const perTurnOrdered = Object.fromEntries(Object.keys(ordered).filter(k => perCell[k]).map(k => [k, perCell[k]]))
const round = v => Math.round(v * 10) / 10
for (const [k, v] of Object.entries(perTurnOrdered)) console.log(`${k} per warm turn: ${Object.entries(v).map(([m, x]) => `${m.replace(/PerTurn$/, "").replace(/Per100Turns$/, " per 100 turns")} ${round(x)}`).join(", ")}`)
// End-of-run figures (lib/counts.mjs reported): the raw heap where heapNet is gated, the thread count and one thread's heap.
const reportedOrdered = Object.fromEntries(Object.keys(ordered).filter(k => Object.keys(reportedCell[k] ?? {}).length).map(k => [k, reportedCell[k]]))
for (const [k, v] of Object.entries(reportedOrdered)) console.log(`${k} reported: ${Object.entries(v).map(([m, x]) => `${m} ${x}`).join(", ")}`)
if (opt("json")) writeFileSync(opt("json"), JSON.stringify({ ...opts, cells: ordered, perTurn: perTurnOrdered, reported: reportedOrdered }, null, 1) + "\n")
for (const e of errors) console.error(`FAIL ${e}`)
if (errors.length) process.exit(1)

if (cmd === "bar") reportBar(ordered)
if (cmd === "update") {
	const old = existsSync(baselineFile) ? JSON.parse(readFileSync(baselineFile, "utf8")) : { cells: {} }
	const cellsOut = { ...old.cells, ...ordered }
	writeFileSync(baselineFile, JSON.stringify({ ...opts, tolerance: toleranceOf(old), cells: cellsOut }, null, 1) + "\n")
	console.log(`counts: wrote ${baselineFile}`)
} else if (cmd === "check") {
	const base = JSON.parse(readFileSync(baselineFile, "utf8"))
	for (const k of ["turns", "tools", "variant", "valgrind"]) if (base[k] !== opts[k]) { console.error(`FAIL the baseline was measured with ${k} ${base[k]}, this run with ${opts[k]}`); process.exit(1) }
	const judged = Object.fromEntries(Object.entries(ordered).filter(([k]) => !k.startsWith("pi/")))
	const failures = compare(base.cells, judged, { tolerance: toleranceOf(base) })
	for (const f of failures) console.log(`${f.kind === "lower" ? "LOWER" : "FAIL "} ${describe(f)}`)
	if (failures.some(f => f.kind === "lower")) console.log(`counts: a number fell below its baseline; keep the gain: node durable/contract/cli/counts.mjs update --impls ${impls.join(",")} --sizes ${sizes.join(",")} and commit ${baselineFile.replace(`${CONTRACT_DIR}/`, "durable/contract/")}`)
	console.log(failures.length ? `counts: ${failures.length} difference(s) from the baseline` : "counts: every number matches its baseline")
	process.exit(failures.length ? 1 : 0)
}
