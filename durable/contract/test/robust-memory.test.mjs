// The memory induction step as a test (robust-durable, routed to dcore-int: open/memory). A long-lived Harness runs W + 4N
// turns of the bench workload (8 lookups a turn); the core's live heap after a full collection (mem_stats slot 6) must not grow
// from turn W+2N to turn W+4N. Measured at 51a5e42725 with N=500: the native core keeps ~49 KB per turn and the TinyGo core
// ~71 KB per turn (TinyGo reaches its 96 MiB linear-memory cap after ~700 turns and then collects on every allocation, ~4 s a
// turn). Cause: durable/core/mirror.go Commit puts every entry, task and submission this handle writes into the mirror
// (mirror_lazy.go: "the mirror holds every record a commit of this handle wrote") and nothing leaves it, where Pi's Session
// keeps no per-record state (pi-durable src/session/session.ts reads records through Storage on demand).
import assert from "node:assert/strict"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { ensureBuilt } from "../lib/gate.mjs"
import { seed } from "../cli/fixtures.mjs"
import { readSamples } from "../lib/memory.mjs"
import { benchRunner, induction } from "../lib/robust.mjs"

const N = 40, WARMUP = 10
/** Bytes per turn the heap may grow from 2N to 4N: collector and buffer-sizing noise, not a record per turn. */
const BOUND = 1024

for (const impl of ["native", "wasm-tinygo"]) {
	test(`${impl}: a long-lived core keeps no memory per turn`, { timeout: 900_000 }, async () => {
		await ensureBuilt(impl)
		const out = mkdtempSync(join(tmpdir(), `robust-memory-${impl}-`))
		try {
			const r = await induction({ impl, n: N, warmup: WARMUP, tools: 8, out, bound: BOUND })
			assert.equal(r.stopped ?? r.incomplete, undefined)
			const h = r.metrics.heap
			assert.ok(h.bounded, `the core's heap after a collection grows ${Math.round(h.step2)} bytes per turn from turn ${r.points[1]} to ${r.points[2]} (${Math.round(h.step1)} from ${r.points[0]} to ${r.points[1]}; ${h.at.map(b => (b / 1048576).toFixed(2)).join(" -> ")} MiB); the bound is ${BOUND}`)
		} finally { rmSync(out, { recursive: true, force: true }) }
	})
}

// Four Durable Objects with a 3,500-turn history each must fit one 128 MB isolate (Cloudflare's limit, JS heap and Wasm
// together): 32 MB an object, of which the facade's own JS heap takes about 8 MB (durable-bench, pi-head's whole isolate is
// 33.4 MB for four), so a core may reserve at most 24 MiB. ADR-0001 D10 budgets "steady 9 MB with snapshot and indexes at
// 3,500 turns". Measured at 51a5e42725: the TinyGo core reserves 48 MiB of linear memory (live heap ~21 MiB after a collection),
// and four objects retain 219.9 MB in one Miniflare isolate (robust-durable bench/isolate.ts). Owner: dcore-int.
test("wasm-tinygo: a core that opened a 3,500-turn history and ran 10 turns reserves at most 24 MiB", { timeout: 900_000 }, async () => {
	await ensureBuilt("wasm-tinygo")
	const out = mkdtempSync(join(tmpdir(), "robust-memory-3500-"))
	try {
		const run = await benchRunner("wasm-tinygo")
		const r = await run({ store: await seed(3500), out, name: "w1-3500", turns: 10, tools: 8, env: { CONTRACT_MEM_HEAP: "1", CONTRACT_TRACE: "/dev/null" } })
		assert.equal(r.code, 0, r.stderr.slice(-600))
		const last = readSamples(r.mem).at(-1)
		const MiB = 1048576
		assert.ok(last.reserved <= 24 * MiB, `linear memory ${(last.reserved / MiB).toFixed(1)} MiB (heap after a collection ${(last.heap / MiB).toFixed(1)} MiB) after 10 turns on 3,500; four objects need at most 24 MiB each`)
	} finally { rmSync(out, { recursive: true, force: true }) }
})

// A conversation can grow past 3,500 turns: pi-durable runs 10,000- and 25,000-turn histories (durable-bench, Miniflare: warm
// p50 628-701 ms at 10,000 and 1,240-1,554 ms at 25,000, the scripted model's walk of the context dominating). The TinyGo core
// traps on the first turn after opening a 10,000-turn conversation ("fatal error: out of memory" at its 96 MiB cap); a build
// without the cap reserves 192 MiB at 10,000 and 384 MiB at 25,000 turns (~15 MiB per 1,000 turns of history), so the core
// holds O(history) after the first turn. Owner: dcore-int (memory); dcore-history (context assembly) informed.
test("wasm-tinygo: a core on a 10,000-turn history runs a turn", { timeout: 1_800_000 }, async () => {
	await ensureBuilt("wasm-tinygo")
	const out = mkdtempSync(join(tmpdir(), "robust-memory-10000-"))
	try {
		const run = await benchRunner("wasm-tinygo")
		const r = await run({ store: await seed(10000), out, name: "w1-10000", turns: 2, tools: 8, env: { CONTRACT_MEM_HEAP: "1", CONTRACT_TRACE: "/dev/null" }, timeoutMs: 1_500_000 })
		assert.equal(r.code, 0, `the run exited ${r.code} ${r.signal ?? ""}: ${r.stderr.match(/FatalError: [^\n]*/)?.[0] ?? r.stderr.slice(-400)}`)
		const last = readSamples(r.mem).at(-1)
		assert.ok(last.reserved <= 24 * 1048576, `linear memory ${(last.reserved / 1048576).toFixed(1)} MiB after 2 turns on 10,000`)
	} finally { rmSync(out, { recursive: true, force: true }) }
})
