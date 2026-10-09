#!/usr/bin/env node
// contract: the Durable core conformance gate (CONTRACT sections 5-7).
//   contract corpus [--check]                      list the manifest; --check validates it and the sources' hashes
//   contract run [--strict] [ids...] --impl <name> [--lane L] [--out dir] [--json file] [-j N]
//                                                  run rows (ids may end in *) against an implementation: pi (the control),
//                                                  or a candidate from impls.toml; prints per-row status and a per-lane summary
//   contract all [--impls pi,wasm-tinygo,wasm-go,native] [--strict] [--lane L]
//                                                  the make target: build each candidate, run every row, per-lane table
//   contract diff a.trace b.trace [...]            rowdiff
//   contract fixtures [sizes...]                   write the W1 fixtures with the reference
// Exit status: 1 when any row failed (pending and skipped rows do not fail the run, but are always printed).
import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { driftedSources, loadCorpus, LANES, validateCorpus } from "../lib/corpus.mjs"
import { ensureBuilt, gate, summarize } from "../lib/gate.mjs"

const [cmd, ...rest] = process.argv.slice(2)
const opt = (args, name, fallback) => { const i = args.indexOf(`--${name}`); return i < 0 ? fallback : args.splice(i, 2)[1] }
const has = (args, name) => { const i = args.indexOf(`--${name}`); if (i < 0) return false; args.splice(i, 1); return true }

if (cmd === "corpus") {
	const corpus = loadCorpus()
	if (has(rest, "check")) {
		const problems = [...validateCorpus(corpus), ...driftedSources(corpus).map(d => `${d.id}: source ${d.source} ${d.problem}`)]
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
	for (const [name, l] of Object.entries(lanes)) console.log(`  ${name.padEnd(18)} pass ${String(l.pass).padStart(3)}  fail ${String(l.fail).padStart(3)}  pending ${String(l.pending).padStart(3)}  skipped ${String(l.skipped).padStart(2)}${l.failed.length ? `  failed: ${l.failed.join(", ")}` : ""}`)
	if (json) writeFileSync(json, JSON.stringify({ impl, lanes, results }, null, 1))
	process.exit(results.some(r => r.status === "fail" || (strict && r.status === "pending")) ? 1 : 0)
} else if (cmd === "all") {
	// The make target: the control, then each candidate build (building it when its paths are missing), a per-lane report.
	const impls = opt(rest, "impls", "pi,wasm-tinygo,wasm-go,native").split(",")
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
	console.log(`\nper-lane results (pass/fail/pending/skipped)   report: ${out}`)
	const lanes = [...new Set(Object.values(all).flatMap(a => Object.keys(a.lanes)))].sort()
	console.log(["lane".padEnd(18), ...impls.map(i => i.padEnd(22))].join(" "))
	for (const l of lanes) console.log([l.padEnd(18), ...impls.map(i => { const x = all[i].lanes[l]; return (x ? `${x.pass}/${x.fail}/${x.pending}/${x.skipped}` : "-").padEnd(22) })].join(" "))
	for (const i of impls) if (all[i].built) console.log(`${i}: ${all[i].built}`)
	writeFileSync(join(out, "report.json"), JSON.stringify(all, null, 1))
	const failed = Object.values(all).some(a => Object.values(a.lanes).some(l => l.fail))
	const pendingRows = Object.values(all).some(a => Object.values(a.lanes).some(l => l.pending))
	process.exit(failed || (strict && pendingRows) ? 1 : 0)
} else if (cmd === "diff") {
	process.argv.splice(2, 1)
	await import("./rowdiff.mjs")
} else if (cmd === "fixtures") {
	await (await import("./fixtures.mjs")).cli(rest)
} else {
	console.error("usage: contract (corpus [--check] | run [ids] --impl name | diff a b | fixtures [sizes])")
	process.exit(2)
}
