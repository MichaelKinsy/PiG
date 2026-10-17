// Turn-tail bench case: where the durable-core profile's warm p95 comes from. It runs the two programs build.mjs makes
// (bench-pig.mjs on the Durable core, bench-pi.mjs on Pi Durable) on one seeded size, round after round on a fresh copy
// of each seeded store, and prints per turn index the median CPU and wall time of each program, the warm (turns 1..9)
// p50/p95 of both, and the rounds whose wall time exceeds their CPU time by more than half (time the process spent off
// the CPU, which a pinned-CPU benchmark must not count against either program).
//
//   node durable/core/host/cf/bench/turn-tail.mjs --bench <dir with bench-pig.mjs and bench-pi.mjs> --turns 1000 \
//        [--rounds 6] [--cpu 11] [--out <dir>]
//
// bench-pig.mjs runs DCORE_WASM when set (else the dist/ beside --bench). Both programs print one JSON line per sample
// with turn (wall ms) and turnCpu (CPU ms) per measured turn, turn 0 being the first turn after open.
import { execFileSync } from "node:child_process"
import { copyFileSync, mkdirSync, readFileSync, existsSync } from "node:fs"
import { join, resolve } from "node:path"
import { tmpdir } from "node:os"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { bench: { type: "string" }, turns: { type: "string", default: "1000" }, rounds: { type: "string", default: "6" }, cpu: { type: "string" }, out: { type: "string" } } })
if (!values.bench) throw new Error("usage: turn-tail.mjs --bench <dir> --turns N [--rounds R] [--cpu C] [--out dir]")
const bench = resolve(values.bench), turns = Number(values.turns), rounds = Number(values.rounds)
const out = resolve(values.out ?? join(tmpdir(), `turn-tail-${turns}`))
const programs = { pig: join(bench, "bench-pig.mjs"), pi: join(bench, "bench-pi.mjs") }
const run = args => execFileSync(values.cpu ? "taskset" : args[0], values.cpu ? ["-c", values.cpu, ...args] : args.slice(1), { encoding: "utf8", maxBuffer: 1 << 26 })
const node = (program, ...rest) => ["node", "--no-warnings", "--expose-gc", program, ...rest]

const samples = { pig: [], pi: [] }
for (const [name, program] of Object.entries(programs)) {
	const dir = join(out, name)
	mkdirSync(dir, { recursive: true })
	const seeded = join(dir, `pi-${turns}.sqlite`)
	if (!existsSync(seeded)) run(node(program, "seed", dir, String(turns)))
}
for (let round = 1; round <= rounds; round++) {
	for (const [name, program] of Object.entries(programs)) {
		const store = join(out, name, "sample.sqlite")
		copyFileSync(join(out, name, `pi-${turns}.sqlite`), store)
		const lines = run(node(program, "sample", store, String(turns), String(round))).trim().split("\n")
		const sample = lines.map(line => { try { return JSON.parse(line) } catch { return undefined } }).findLast(x => x?.turnCpu)
		if (!sample) throw new Error(`${name} round ${round}: no sample line`)
		samples[name].push(sample)
	}
}

const quantile = (values, q) => { const s = [...values].sort((a, b) => a - b); return s[Math.min(s.length - 1, Math.ceil(q * s.length) - 1)] }
const ms = x => Math.round(x).toString().padStart(5)
console.log(`turn-tail: ${turns} turns, ${rounds} rounds${values.cpu ? `, CPU ${values.cpu}` : ""}; median per turn index (turn 0 is the first turn after open)`)
console.log(`turn        ${Array.from({ length: samples.pig[0].turn.length }, (_, i) => String(i).padStart(5)).join("")}`)
for (const name of Object.keys(programs)) {
	for (const field of ["turnCpu", "turn"]) {
		const n = samples[name][0][field].length
		const medians = Array.from({ length: n }, (_, i) => quantile(samples[name].map(s => s[field][i]), 0.5))
		console.log(`${name.padEnd(4)} ${field === "turn" ? "wall" : "cpu "}  ${medians.map(ms).join("")}`)
	}
}
for (const name of Object.keys(programs)) {
	const warm = field => samples[name].flatMap(s => s[field].slice(1))
	console.log(`${name.padEnd(4)} warm cpu p50 ${ms(quantile(warm("turnCpu"), 0.5))} p95 ${ms(quantile(warm("turnCpu"), 0.95))} | wall p50 ${ms(quantile(warm("turn"), 0.5))} p95 ${ms(quantile(warm("turn"), 0.95))}`)
	samples[name].forEach((s, i) => {
		const wall = s.turn.slice(1).reduce((a, b) => a + b, 0), cpu = s.turnCpu.slice(1).reduce((a, b) => a + b, 0)
		if (wall > 1.5 * cpu) console.log(`${name.padEnd(4)} round ${i + 1}: warm wall ${Math.round(wall)} ms is ${(wall / cpu).toFixed(2)}x its CPU ${Math.round(cpu)} ms (off-CPU time)`)
	})
}
