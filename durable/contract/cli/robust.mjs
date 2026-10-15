#!/usr/bin/env node
// robust: robustness proofs of a Durable core build (lib/robust.mjs).
//   robust induction --impl <name> [--n 500] [--warmup 50] [--tools 8] [--variant incr] [--bound 0] [--out dir] [--json file]
//                    retained memory at W+N, W+2N and W+4N turns of one Harness; exits 1 when a core metric grows between 2N and 4N
//   robust soak --impl <name> [--objects 4] [--rounds 5000] [--warmup 1000] [--every 25] [--window 100000] [--bound 1024] [--out dir] [--json file]
//                    `objects` Harnesses in one process (one isolate's Durable Objects) run `rounds` turns each with compaction on;
//                    the induction step over sparse samples; exits 1 when a core metric grows by more than `bound` bytes per
//                    object-turn between 2N and 4N
//   robust timetravel --impls wasm-tinygo,wasm-go,native [--size 3500] [--at-turn 100] [--scrub 8] [--out dir] [--json file]
//                    scenarios/time-travel.mjs on a copy of the size-turn fixture: each build must equal pi; prints each
//                    operation's wall time beside pi's; exits 1 when a build fails or differs
//   robust crash --impls wasm-tinygo,wasm-go,native [--points 3,17,40 | --every] [-j 1] [--out dir] [--json file]
//                    kill scenarios/crash-conversation.mjs after every commit (or the listed ones), restart it, compare with pi under
//                    the same crash; exits 1 when any crash point fails
//   robust recycle --impls wasm-tinygo,wasm-go [--points 3,17,40 | --every] [-j 1] [--out dir] [--json file]
//                    the same scenario with a core trap after every commit (or the listed ones) instead of a kill: the host recycles
//                    the Wasm instance in process and the program finishes; no submission may be lost or duplicated and the output
//                    must equal the uninterrupted run's or that of pi killed after the same commit and restarted
// The builds are the ones impls.toml names (wasm-tinygo, wasm-go, native) and `pi` for the reference's numbers; build them
// first (make -C durable/core build-tinygo build-go, sh durable/core/budget/build-native.sh).
import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { crash, induction, recycle, soak, timeTravel } from "../lib/robust.mjs"

const [cmd, ...rest] = process.argv.slice(2)
const opt = (name, fallback) => { const i = rest.indexOf(`--${name}`); return i < 0 ? fallback : rest[i + 1] }
const out = opt("out", mkdtempSync(join(tmpdir(), "contract-robust-")))
const json = opt("json")
const MiB = 1024 * 1024
const fmt = b => `${(b / MiB).toFixed(2)} MiB`

if (cmd === "induction") {
	const impl = opt("impl", "wasm-tinygo")
	const r = await induction({ impl, n: Number(opt("n", "500")), warmup: Number(opt("warmup", "50")), tools: Number(opt("tools", "8")), variant: opt("variant", "incr"), bound: Number(opt("bound", "0")), out })
	console.log(`induction ${impl}: ${r.turns} turns of ${r.tools} lookups in ${Math.round(r.ms / 1000)} s; points at turns ${r.points.join(", ")} (each the largest sample of the interval ending there)`)
	if (r.stopped) console.log(`  STOPPED: ${r.stopped}`)
	if (r.incomplete) console.log(`  incomplete: ${r.incomplete}`)
	for (const [metric, m] of Object.entries(r.metrics)) console.log(`  ${metric.padEnd(8)} ${m.at.map(fmt).join(" -> ")}${m.step1 === undefined ? ` (turns ${m.turns.join(" -> ")})` : `  step N->2N ${m.step1.toFixed(1)} B/turn  step 2N->4N ${m.step2.toFixed(1)} B/turn`}  slope ${m.slope.toFixed(1)} B/turn  max ${fmt(m.max)}${["heap", "reserved"].includes(metric) ? (m.bounded ? "  bounded" : "  GROWS") : ""}`)
	if (json) writeFileSync(json, JSON.stringify(r, null, 1))
	// The core's own metrics decide: the host's V8 heap and the process's resident set carry the shared model path (the
	// scripted model and the context it is given), which grows with the transcript in Pi too.
	process.exit(r.stopped || r.incomplete || ["heap", "reserved"].some(m => r.metrics[m] && !r.metrics[m].bounded) ? 1 : 0)
} else if (cmd === "soak") {
	const impl = opt("impl", "wasm-tinygo")
	const r = await soak({ impl, objects: Number(opt("objects", "4")), rounds: Number(opt("rounds", "5000")), warmup: Number(opt("warmup", "1000")), every: Number(opt("every", "25")), window: Number(opt("window", "100000")), tools: Number(opt("tools", "8")), bound: Number(opt("bound", "1024")), out })
	console.log(`soak ${impl}: ${r.objects} objects x ${r.rounds} turns (${r.turns} turns of ${r.tools} lookups, compaction window ${r.window}) in ${Math.round(r.ms / 1000)} s; points at rounds ${r.points.join(", ")} (each the largest sample of the interval ending there)`)
	if (r.stopped) console.log(`  STOPPED: ${r.stopped}`)
	if (r.incomplete) console.log(`  incomplete: ${r.incomplete}`)
	for (const [metric, m] of Object.entries(r.metrics)) console.log(`  ${metric.padEnd(8)} ${m.at.map(fmt).join(" -> ")}${m.step1 === undefined ? ` (rounds ${m.turns.join(" -> ")})` : `  step N->2N ${m.perTurn.step1.toFixed(1)} B/object-turn  step 2N->4N ${m.perTurn.step2.toFixed(1)} B/object-turn`}  slope ${m.perTurn.slope.toFixed(1)} B/object-turn  max ${fmt(m.max)}${["heap", "reserved"].includes(metric) ? (m.bounded ? "  bounded" : "  GROWS") : ""}`)
	if (json) writeFileSync(json, JSON.stringify(r, null, 1))
	process.exit(r.stopped || r.incomplete || ["heap", "reserved"].some(m => r.metrics[m] && !r.metrics[m].bounded) ? 1 : 0)
} else if (cmd === "timetravel") {
	const impls = opt("impls", "wasm-tinygo,wasm-go,native").split(",")
	const r = await timeTravel({ impls, size: Number(opt("size", "3500")), atTurn: Number(opt("at-turn", "100")), scrub: Number(opt("scrub", "8")), out })
	const sum = ops => { const by = {}; for (const o of ops) by[o.op] = (by[o.op] ?? 0) + o.ms; return by }
	const names = [...new Set(Object.values(r.runs).flatMap(x => x.ops.map(o => o.op)))]
	console.log(`timetravel on ${r.size} turns at h${r.atTurn}, ${r.scrub} scrub points; ms per operation (scrub: total)`)
	console.log(["build".padEnd(12), ...names.map(n => n.padStart(10))].join(" "))
	for (const [label, x] of Object.entries(r.runs)) {
		const by = sum(x.ops)
		console.log([label.padEnd(12), ...names.map(n => (by[n] === undefined ? "-" : by[n].toFixed(1)).padStart(10))].join(" ") + (x.ok ? "" : `  FAIL ${x.problem}${x.reports ? " " + JSON.stringify(x.reports).slice(0, 400) : ""}`))
	}
	if (json) writeFileSync(json, JSON.stringify(r, null, 1))
	process.exit(Object.values(r.runs).every(x => x.ok) ? 0 : 1)
} else if (cmd === "crash") {
	const impls = opt("impls", "wasm-tinygo,wasm-go,native").split(",")
	const points = opt("points")?.split(",").map(Number)
	const r = await crash({ impls, out, points, concurrency: Number(opt("j", "1")), keep: rest.includes("--keep"), onPoint: rows => console.log(rows.map(r => `k=${r.k} ${r.impl} ${r.ok ? (r.identical ? "ok identical" : `ok (differs from uninterrupted in ${r.tablesDiffering.join(",")})`) : `FAIL ${r.problem}${r.report ? " " + JSON.stringify(r.report).slice(0, 400) : ""}`}`).join("\n")) })
	console.log(`crash: ${r.commits} commits in the uninterrupted run; ${r.points.length / (impls.length + 1)} crash points`)
	for (const [impl, u] of Object.entries(r.uninterrupted)) console.log(`  ${impl} uninterrupted ${u.ok ? "equals pi" : `DIFFERS ${JSON.stringify(u.reports ?? u.problem).slice(0, 600)}`}`)
	for (const impl of new Set(["pi", ...impls])) {
		const rows = r.points.filter(p => p.impl === impl)
		console.log(`  ${impl.padEnd(12)} ${rows.filter(p => p.ok).length}/${rows.length} crash points pass; ${rows.filter(p => p.identical).length} byte-identical to the uninterrupted run; failed ${r.failed[impl] ?? 0}`)
	}
	if (json) writeFileSync(json, JSON.stringify(r, null, 1))
	process.exit(Object.entries(r.failed).some(([, n]) => n > 0) ? 1 : 0)
} else if (cmd === "recycle") {
	const impls = opt("impls", "wasm-tinygo,wasm-go").split(",")
	const points = opt("points")?.split(",").map(Number)
	const r = await recycle({ impls, out, points, concurrency: Number(opt("j", "1")), keep: rest.includes("--keep"), onPoint: rows => console.log(rows.map(r => `k=${r.k} ${r.impl} ${r.ok ? (r.identical ? "ok identical" : `ok (output equals ${r.outputEquals.join(" and ")}; store ${r.equalsPiCrash ? "equals pi's after the crash" : `differs from pi's in ${r.tablesDifferingFromPiCrash.join(",")}`})`) : `FAIL ${r.problem}${r.report ? " " + JSON.stringify(r.report).slice(0, 400) : ""}`}`).join("\n")) })
	console.log(`recycle: ${r.commits} commits in the uninterrupted run; ${r.points.length / impls.length} trap points`)
	for (const [impl, u] of Object.entries(r.uninterrupted)) console.log(`  ${impl} uninterrupted ${u.ok ? "equals pi" : `DIFFERS ${JSON.stringify(u.reports ?? u.problem).slice(0, 600)}`}`)
	for (const impl of impls) {
		const rows = r.points.filter(p => p.impl === impl)
		console.log(`  ${impl.padEnd(12)} ${rows.filter(p => p.ok).length}/${rows.length} trap points pass; ${rows.filter(p => p.identical).length} byte-identical to the uninterrupted run; failed ${r.failed[impl] ?? 0}`)
	}
	if (json) writeFileSync(json, JSON.stringify(r, null, 1))
	process.exit(Object.entries(r.failed).some(([, n]) => n > 0) ? 1 : 0)
} else {
	console.error("usage: robust induction --impl <name> [--n 500] [--warmup 50] [--tools 8]")
	process.exit(2)
}
