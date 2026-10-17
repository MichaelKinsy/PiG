#!/usr/bin/env node
// contract: the Durable core conformance gate (CONTRACT sections 5-7).
//   contract corpus [--check]                      list the manifest; --check validates it and the sources' hashes
//   contract run [--strict] [ids...] --impl <name> [--lane L] [--out dir] [--json file] [-j N]
//                                                  run rows (ids may end in *) against an implementation: pi (the control),
//                                                  or a candidate from impls.toml; prints per-row status and a per-lane summary
//   contract all [--impls pi,wasm-tinygo,native | all] [--strict] [--lane L] [--no-diagnose]
//                                                  the make target: build each candidate, run every row, per-lane table and a totals
//                                                  line per build. The shipped builds (TinyGo Wasm, native) gate every merge; `all` adds
//                                                  standard Go Wasm and the bench engine (nightly, before a release). Without wasm-go
//                                                  selected, a failing TinyGo row is re-run on wasm-go as a non-gating diagnostic.
//   contract soak --seeds A-B [--profile gate|node|both] [--shrink | --args "<reductions>"] --impl <name> [--out dir] [-j N]
//                                                  the differential ordering rows F-<profile>-<seed> (scenarios/diff-ordering.mjs) for a
//                                                  seed range the manifest does not list; a failing seed is a row you can rerun by id
//   contract diff a.trace b.trace [...]            rowdiff
//   contract fixtures [sizes...]                   write the W1 fixtures with the reference
// Exit status: 1 when any row failed (pending and skipped rows do not fail the run, but are always printed).
import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { driftedSources, loadCorpus, LANES, validateCorpus } from "../lib/corpus.mjs"
import { describe, judge, loadBaseline, stale, validateBaseline } from "../lib/baseline.mjs"
import { blocking, diffRows, ensureBuilt, gate, summarize } from "../lib/gate.mjs"

/** The one-line totals of a build: the sums of its per-lane counts. */
const totals = (impl, lanes) => {
	const sum = { pass: 0, fail: 0, pending: 0, skipped: 0 }
	for (const l of Object.values(lanes)) for (const k of Object.keys(sum)) sum[k] += l[k]
	return `totals ${impl}: pass ${sum.pass}  fail ${sum.fail}  pending ${sum.pending}  skipped ${sum.skipped}`
}
const SHIPPED = "pi,wasm-tinygo,native"
const EVERY = "pi,wasm-tinygo,wasm-go,native,native-bench"

const [cmd, ...rest] = process.argv.slice(2)
const opt = (args, name, fallback) => { const i = args.indexOf(`--${name}`); return i < 0 ? fallback : args.splice(i, 2)[1] }
const has = (args, name) => { const i = args.indexOf(`--${name}`); if (i < 0) return false; args.splice(i, 1); return true }

if (cmd === "corpus") {
	const corpus = loadCorpus()
	if (has(rest, "check")) {
		const problems = [...validateCorpus(corpus), ...validateBaseline(loadBaseline(), corpus), ...driftedSources(corpus).map(d => `${d.id}: source ${d.source} ${d.problem}`)]
		for (const p of problems) console.log(p)
		console.log(problems.length ? `${problems.length} problem(s)` : `${corpus.scenario.length} scenarios, sources match the pin`)
		process.exit(problems.length ? 1 : 0)
	}
	for (const s of corpus.scenario) console.log(`${s.id.padEnd(22)} ${s.owner.padEnd(18)} ${(s.status ?? "").padEnd(8)} ${s.about ?? s.source ?? ""}`)
} else if (cmd === "run") {
	const impl = opt(rest, "impl", "pi")
	const lane = opt(rest, "lane")
	const out = opt(rest, "out", mkdtempSync(join(tmpdir(), "contract-run-")))
	const json = opt(rest, "json")
	const jobs = Number(opt(rest, "j", "8"))
	if (lane && !LANES.includes(lane)) { console.error(`unknown lane ${lane}; lanes: ${LANES.join(", ")}`); process.exit(2) }
	const strict = has(rest, "strict")
	// The row command of the integrator's gate (durable/core/contract/corpus.tsv): build the candidate when its paths are missing.
	const built = impl === "pi" || impl === "ts" ? undefined : await ensureBuilt(impl)
	if (built) console.log(`${impl}: ${built}`)
	const results = await gate({ impl, ids: rest.length ? rest : undefined, lanes: lane ? [lane] : undefined, out, concurrency: jobs, onResult: r => console.log(`${r.status.toUpperCase().padEnd(7)} ${r.id.padEnd(22)} ${r.owner.padEnd(18)} ${String(r.ms).padStart(6)} ms  ${typeof r.detail === "string" ? r.detail : r.status === "fail" ? JSON.stringify(r.detail).slice(0, 300) : ""}`) })
	const lanes = summarize(results)
	console.log(`\nimplementation ${impl}`)
	console.log(totals(impl, lanes))
	for (const [name, l] of Object.entries(lanes)) console.log(`  ${name.padEnd(18)} pass ${String(l.pass).padStart(3)}  fail ${String(l.fail).padStart(3)}  pending ${String(l.pending).padStart(3)}  skipped ${String(l.skipped).padStart(2)}${l.failed.length ? `  failed: ${l.failed.join(", ")}` : ""}`)
	if (json) writeFileSync(json, JSON.stringify({ impl, lanes, results }, null, 1))
	// The baseline (lib/baseline.mjs): a listed differential row stays red but does not fail the run; any other failure does.
	const verdict = judge(impl, results)
	for (const line of describe(impl, verdict)) console.log(line)
	if (verdict.passing.length) console.log(`passing on ${impl} and listed in baseline.toml: ${verdict.passing.map(r => r.id).join(" ")} (delete the entry once it passes on every build)`)
	process.exit(verdict.fresh.length || (strict && results.some(blocking)) ? 1 : 0)
} else if (cmd === "soak") {
	const impl = opt(rest, "impl", "pi")
	const range = opt(rest, "seeds", "1-64")
	const profile = opt(rest, "profile", "both")
	const out = opt(rest, "out", mkdtempSync(join(tmpdir(), "contract-soak-")))
	const jobs = Number(opt(rest, "j", "8"))
	const shrinkIt = has(rest, "shrink")
	// --args "<reductions>": run the reduced program a shrink printed (the scenario's own flags, see scenarios/diff-ordering.mjs).
	const reduction = opt(rest, "args")?.split(" ").filter(Boolean)
	const [from, to = from] = range.split("-").map(Number)
	if (!Number.isInteger(from) || !Number.isInteger(to) || from < 0 || to < from || !["gate", "node", "both"].includes(profile)) { console.error("usage: contract soak --seeds A-B [--profile gate|node|both] --impl name"); process.exit(2) }
	const built = impl === "pi" || impl === "ts" ? undefined : await ensureBuilt(impl)
	if (built) console.log(`${impl}: ${built}`)
	const results = await gate({ impl, out, concurrency: jobs, rows: diffRows(from, to, profile === "both" ? ["gate", "node"] : [profile]).map(r => (reduction ? { ...r, args: reduction } : r)), onResult: r => console.log(`${r.status.toUpperCase().padEnd(7)} ${r.id.padEnd(22)} ${String(r.ms).padStart(6)} ms  ${typeof r.detail === "string" ? r.detail : r.status === "fail" ? JSON.stringify(r.detail).slice(0, 300) : ""}`) })
	const failed = results.filter(r => r.status === "fail").map(r => r.id)
	const verdict = judge(impl, results)
	for (const line of describe(impl, verdict)) console.log(line)
	// --shrink: cut every failing seed down to the smallest program that still differs; the reference's plan line gives the cuts.
	if (shrinkIt) {
		const { shrink } = await import("../lib/shrink.mjs")
		for (const id of failed) {
			const [, p, seed] = id.split("-")
			const small = await shrink({ impl, seed: Number(seed), profile: p, out: join(out, `shrink-${id}`), trace: join(out, "ref", `${id}.trace`), log: line => console.log(`  ${id} ${line.slice(0, 160)}`) })
			console.log(`shrunk ${id}: contract soak --seeds ${seed} --profile ${p} --impl ${impl} --args "${small.args.join(" ")}"\n  ${JSON.stringify(small.detail).slice(0, 400)}`)
		}
	}
	console.log(`\nsoak ${impl}: ${results.length - failed.length} of ${results.length} seeds equal Pi${failed.length ? `; failed: ${failed.join(" ")}` : ""}`)
	process.exit(verdict.fresh.length ? 1 : 0)
} else if (cmd === "all") {
	// The make target: the control, then each candidate build (building it when its paths are missing), a per-lane report.
	const picked = opt(rest, "impls", SHIPPED)
	const impls = (picked === "all" ? EVERY : picked).split(",")
	const diagnose = !has(rest, "no-diagnose")
	const out = opt(rest, "out", mkdtempSync(join(tmpdir(), "contract-all-")))
	const jobs = Number(opt(rest, "j", "8"))
	const strict = has(rest, "strict")
	const lane = opt(rest, "lane")
	const all = {}
	for (const name of impls) {
		const built = name === "pi" ? undefined : await ensureBuilt(name)
		const results = await gate({ impl: name, ids: rest.length ? rest : undefined, lanes: lane ? [lane] : undefined, out: join(out, name), concurrency: jobs, onResult: r => { if (r.status === "fail") console.log(`FAIL ${name} ${r.id} (${r.owner}): ${JSON.stringify(r.detail).slice(0, 240)}`) } })
		all[name] = { built, lanes: summarize(results), results }
	}
	// Standard Go Wasm is a diagnostic when it is not a selected build: whenever TinyGo fails a row, those rows run on wasm-go, so the
	// report says at once whether the failure is the source or TinyGo's build. It never changes the exit status.
	const diagnostic = {}
	const tinygoFailed = (all["wasm-tinygo"]?.results ?? []).filter(r => r.status === "fail").map(r => r.id)
	if (diagnose && !impls.includes("wasm-go") && tinygoFailed.length) {
		console.log(`\ndiagnostic: wasm-tinygo failed ${tinygoFailed.join(", ")}; running them on wasm-go (not gating)`)
		const built = await ensureBuilt("wasm-go")
		const results = await gate({ impl: "wasm-go", ids: tinygoFailed, out: join(out, "wasm-go-diagnostic"), concurrency: jobs, onResult: r => console.log(`DIAGNOSTIC wasm-go ${r.status.toUpperCase()} ${r.id} (${r.owner})`) })
		diagnostic["wasm-go"] = { built, lanes: summarize(results), results }
	}
	console.log(`\nper-lane results (pass/fail/pending/skipped)   report: ${out}`)
	const lanes = [...new Set(Object.values(all).flatMap(a => Object.keys(a.lanes)))].sort()
	console.log(["lane".padEnd(18), ...impls.map(i => i.padEnd(22))].join(" "))
	for (const l of lanes) console.log([l.padEnd(18), ...impls.map(i => { const x = all[i].lanes[l]; return (x ? `${x.pass}/${x.fail}/${x.pending}/${x.skipped}` : "-").padEnd(22) })].join(" "))
	for (const i of impls) if (all[i].built) console.log(`${i}: ${all[i].built}`)
	for (const i of impls) console.log(totals(i, all[i].lanes))
	for (const [i, d] of Object.entries(diagnostic)) console.log(`${totals(i, d.lanes)}  (diagnostic: only the rows wasm-tinygo failed)`)
	writeFileSync(join(out, "report.json"), JSON.stringify({ ...all, ...(Object.keys(diagnostic).length ? { diagnostic } : {}) }, null, 1))
	// The baseline: listed differential rows stay red in the table; a failure it does not list fails the run, and so does a listed row that
	// passes on every build that ran it (the entry is deleted with the fix).
	const verdicts = Object.fromEntries(impls.map(i => [i, judge(i, all[i].results)]))
	for (const i of impls) for (const line of describe(i, verdicts[i])) console.log(line)
	const fixed = impls.length > 1 || impls[0] !== "pi" ? stale(verdicts) : []
	if (fixed.length) console.log(`FIXED: ${fixed.join(" ")} pass on every build; delete them from baseline.toml`)
	const failed = impls.some(i => verdicts[i].fresh.length)
	const pendingRows = Object.values(all).some(a => a.results.some(blocking))
	process.exit(failed || fixed.length || (strict && pendingRows) ? 1 : 0)
} else if (cmd === "diff") {
	process.argv.splice(2, 1)
	await import("./rowdiff.mjs")
} else if (cmd === "fixtures") {
	await (await import("./fixtures.mjs")).cli(rest)
} else {
	console.error("usage: contract (corpus [--check] | run [ids] --impl name | soak --seeds A-B --impl name | diff a b | fixtures [sizes])")
	process.exit(2)
}
