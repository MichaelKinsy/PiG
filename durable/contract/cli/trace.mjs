#!/usr/bin/env node
// trace record|check|verify [ids...] [--impl X] [--out dir]
//   record   run the reference (Pi at the PIN commit) with the API trace on and write the golden interleaving of each row to traces/<row>.pi.jsonl
//   check    run a candidate and compare its interleaving with the golden one, exactly (exit 1 on any difference; ticks are reported)
//   verify   run the reference again and compare it with the golden files (determinism, and drift when the pin changes)
//   record-u | check-u [--impl X]   the same for the named vitest tests of traces/u-tests.json (the reference's own tests, run with the trace
//                                    setup file scenarios/vitest.trace-setup.mjs): golden traces/<row>.<n>.pi.jsonl per listed test
// Rows are the E rows and the interop row I (program rows). Golden files are generated: never edit them; run `trace record`.
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, existsSync, readdirSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { loadCorpus } from "../lib/corpus.mjs"
import { ensureBuilt, gate, vitestRun } from "../lib/gate.mjs"
import { compareInterleavings, testTokens, tokens } from "../lib/interleave.mjs"
import { resolveImpl } from "../lib/gate.mjs"
import { readTrace } from "../lib/rowdiff.mjs"

const DIR = join(dirname(fileURLToPath(import.meta.url)), "..", "traces")
const [cmd, ...rest] = process.argv.slice(2)
const opt = (name, fallback) => { const i = rest.indexOf(`--${name}`); return i < 0 ? fallback : rest.splice(i, 2)[1] }
const impl = cmd === "check" || cmd === "check-u" ? opt("impl", "native") : "pi"
const out = opt("out", mkdtempSync(join(tmpdir(), "contract-trace-")))
const wanted = rest.filter(a => !a.startsWith("--"))
const slug = (row, name) => `${row}.${name.replace(/[^\w]+/g, "-").replace(/^-|-$/g, "").slice(0, 60)}`

/** record-u / check-u: run the listed tests' files with the trace and compare (or write) each test's interleaving. */
async function vitestTraces() {
	const wanted = JSON.parse(readFileSync(join(DIR, "u-tests.json"), "utf8"))
	const side = cmd === "record-u" ? "pi" : impl
	const target = side === "pi" ? { name: "ref" } : await resolveImpl(side)
	if (side !== "pi") { const built = await ensureBuilt(side); if (built) console.log(`${side}: ${built}`) }
	let bad = 0
	for (const { row, file, test } of wanted) {
		if (rest.length && !rest.includes(row)) continue
		const traceFile = join(out, `${row}.${side}.vitest-trace.jsonl`)
		mkdirSync(out, { recursive: true })
		process.env.CONTRACT_VITEST_TRACE = traceFile
		await vitestRun(target, { out }, `test/${file}`, `trace-${row}`, false)
		const lines = existsSync(traceFile) ? readFileSync(traceFile, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l)) : []
		const have = testTokens(lines, test)
		const goldenFile = join(DIR, `${slug(row, test)}.pi.jsonl`)
		if (!have) { console.log(`FAIL    ${row}  the test "${test.slice(0, 60)}" left no trace`); bad++; continue }
		if (cmd === "record-u") { writeFileSync(goldenFile, have.map(t => JSON.stringify(t)).join("\n") + "\n"); console.log(`RECORD  ${row}  "${test.slice(0, 50)}"  ${have.length} events`); continue }
		const want = readFileSync(goldenFile, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
		const d = compareInterleavings(want, have)
		if (!d) { console.log(`PASS    ${row}  "${test.slice(0, 50)}"  ${want.length} events`); continue }
		bad++
		console.log(`FAIL    ${row}  "${test.slice(0, 50)}"  first difference at event ${d.index}\n          agreed:   ${d.before.join(" | ")}\n          expected: ${d.expected.join(" | ")}\n          got:      ${d.got.join(" | ")}`)
	}
	process.exit(bad ? 1 : 0)
}

const programRows = loadCorpus().scenario.filter(r => r.runner === "example" || r.runner === "interop").map(r => r.id)
const ids = wanted.length ? wanted : programRows
if (cmd === "record-u" || cmd === "check-u") await vitestTraces()
if (!["record", "check", "verify"].includes(cmd) || ids.some(id => !programRows.includes(id))) { console.error("usage: trace record|check|verify [E-row or I ...] [--impl X] [--out dir]"); process.exit(2) }

process.env.CONTRACT_API_TRACE = "1"
process.env.CONTRACT_TICKS = "1"
if (impl !== "pi") { const built = await ensureBuilt(impl); if (built) console.log(`${impl}: ${built}`) }
const results = await gate({ impl, ids, out, concurrency: 4 })
const golden = id => join(DIR, `${id}.pi.jsonl`)
const traceOf = (dir, id) => tokens(readTrace(join(out, dir, `${id}.trace`)))
let bad = 0, good = 0, skipped = 0
for (const id of ids) {
	const r = results.find(x => x.id === id)
	if (!existsSync(join(out, "ref", `${id}.trace`))) { skipped++; console.log(`SKIP    ${id}  no reference trace (${r?.status}: ${typeof r?.detail === "string" ? r.detail : "the reference did not run"})`); continue }
	const ref = traceOf("ref", id)
	if (cmd === "record") {
		mkdirSync(DIR, { recursive: true })
		writeFileSync(golden(id), ref.map(t => JSON.stringify(t)).join("\n") + "\n")
		console.log(`RECORD  ${id}  ${ref.length} events`)
		continue
	}
	if (!existsSync(golden(id))) { console.log(`MISSING ${id}  no golden trace: run trace record ${id}`); bad++; continue }
	const want = readFileSync(golden(id), "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
	const have = cmd === "verify" ? ref : existsSync(join(out, impl, `${id}.trace`)) ? traceOf(impl, id) : undefined
	if (!have) { console.log(`FAIL    ${id}  the ${impl} run left no trace (${r?.status})`); bad++; continue }
	const d = compareInterleavings(want, have)
	if (!d) { good++; console.log(`PASS    ${id}  ${want.length} events`); continue }
	bad++
	console.log(`FAIL    ${id}  first difference at event ${d.index}\n          agreed:   ${d.before.join(" | ")}\n          expected: ${d.expected.join(" | ")}\n          got:      ${d.got.join(" | ")}`)
}
if (cmd === "check" || cmd === "verify") console.log(`${good} of ${ids.length - skipped} rows match Pi's interleaving${skipped ? ` (${skipped} skipped)` : ""}`)
void readdirSync
process.exit(bad ? 1 : 0)
