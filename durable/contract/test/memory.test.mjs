// The memory bound of a bench row (lib/memory.mjs): growth per measured turn is reported in the row's detail, and a candidate
// whose memory keeps growing fails its row (the TinyGo core doubled its linear memory to 3 GiB and trapped on W1-3500).
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { boundApplies, memoryDescribe, memorySummary, overBound } from "../lib/memory.mjs"

const MiB = 1024 * 1024
const series = (metric, mib) => mib.map((v, turn) => ({ turn, rss: 100 * MiB, [metric]: v * MiB }))

test("the bound judges candidates, not the pi reference", () => {
	assert.equal(boundApplies("pi"), false)
	for (const name of ["native", "wasm-go", "wasm-tinygo", "native-bench", "ts"]) assert.equal(boundApplies(name), true)
})

test("flat memory, a heap that doubles once and slow growth pass", () => {
	assert.equal(overBound(memorySummary(series("linear", Array(10).fill(192)))), undefined)
	assert.equal(overBound(memorySummary(series("linear", [48, 48, 48, 96, 96, 96, 96, 96, 96, 96]))), undefined) // W1-1000 on TinyGo
	assert.equal(overBound(memorySummary(series("linear", [6, 6, 12, 24]))), undefined) // W4c-50 on TinyGo: two doublings of a small heap
	assert.equal(overBound(memorySummary(series("rss", [60, 61, 62, 63, 64, 65, 66, 67, 68, 69]))), undefined)
})

test("the TinyGo 3 GiB doubling fails with the numbers, and so does a steady leak", () => {
	// linear memory after each turn of W1-3500 before the payload buffers were recycled: 96 MiB, doubling to the 3 GiB trap
	const doubling = overBound(memorySummary(series("linear", [96, 192, 192, 384, 384, 768, 768, 1536, 1536, 3072])))
	assert.match(doubling.problem, /memory grows 330\.7 MiB per measured turn \(linear: 96 MiB after turn 0, 3072 MiB after turn 9\)/)
	// the native build kept a decoded history per model call: 72 MiB after turn 0, 234 MiB after turn 9
	const leak = overBound(memorySummary(series("rss", [72, 90, 108, 126, 144, 162, 180, 198, 216, 234])))
	assert.match(leak.problem, /memory grows 18\.0 MiB per measured turn \(rss: 72 MiB/)
})

test("fewer than three samples are not a measurement; the summary names the metric and the growth", () => {
	assert.equal(memorySummary(series("linear", [10, 10])), undefined)
	assert.equal(memoryDescribe(memorySummary(series("linear", [192, 192, 192]))), "+0.0 MiB/turn linear 192 MiB over 3 turns")
	// the process resident set is the metric when a run has no linear memory (native builds)
	assert.match(memoryDescribe(memorySummary([{ turn: 0, rss: 50 * MiB }, { turn: 1, rss: 51 * MiB }, { turn: 2, rss: 52 * MiB }])), /^\+1\.0 MiB\/turn rss 52 MiB/)
})

// ---- the whole path: a native bench host that retains 64 MiB per turn fails the row of the gate, an unchanged one passes.

const repo = join(import.meta.dirname, "..", "..", "..")
const work = mkdtempSync(join(tmpdir(), "contract-memory-"))
test.after(() => rmSync(work, { recursive: true, force: true }))

function buildHost(name, mutate) {
	const source = join(repo, "durable", "core", "contracttest", "memory.go")
	const args = ["build", "-o", join(work, name)]
	if (mutate) {
		const text = readFileSync(source, "utf8")
		const from = "debug.FreeOSMemory()\n"
		assert.ok(text.includes(from), "memory.go no longer calls FreeOSMemory in Sample: update the mutation")
		const mutated = join(work, `memory-${name}.go`)
		writeFileSync(mutated, text.replace(from, `${from}\tleaked = append(leaked, make([]byte, 64<<20))\n\tfor i := 0; i < len(leaked[len(leaked)-1]); i += 4096 {\n\t\tleaked[len(leaked)-1][i] = 1\n\t}\n`) + "\nvar leaked [][]byte\n")
		const overlay = join(work, `overlay-${name}.json`)
		writeFileSync(overlay, JSON.stringify({ Replace: { [source]: mutated } }))
		args.push(`-overlay=${overlay}`)
	}
	args.push("./durable/core/contracttest/cmd/benchhost")
	execFileSync("go", args, { cwd: repo, env: { ...process.env, GOWORK: "" } })
	return join(work, name)
}

test("a host that retains 64 MiB per turn fails W1-50 with the growth, an unchanged host passes and reports its memory", async () => {
	const ok = buildHost("ok", false), leaky = buildHost("leaky", true)
	const impls = join(work, "impls.toml")
	writeFileSync(impls, ["ok", "leaky"].map(n => `[[impl]]\nname = "${n}"\nslice = "bench"\nnative_exe = "${n === "ok" ? ok : leaky}"\n`).join("\n"))
	process.env.CONTRACT_IMPLS = impls
	process.env.CONTRACT_NO_BUILD = "1"
	const { gate } = await import("../lib/gate.mjs")
	const [good] = await gate({ impl: "ok", ids: ["W1-50"], out: join(work, "out-ok"), concurrency: 1 })
	assert.equal(good.status, "pass", JSON.stringify(good).slice(0, 900))
	assert.match(good.detail, /770 commits; [+-]\d+\.\d MiB\/turn rss \d+ MiB over 10 turns/)
	const [bad] = await gate({ impl: "leaky", ids: ["W1-50"], out: join(work, "out-leaky"), concurrency: 1 })
	assert.equal(bad.status, "fail", JSON.stringify(bad).slice(0, 500))
	assert.match(bad.detail.problem, /memory grows 64\.\d MiB per measured turn \(rss: /)
	assert.match(bad.detail.reference, /MiB\/turn rss/)
})
