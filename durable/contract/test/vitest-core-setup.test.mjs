// The U and SC rows compile a candidate's core module before a file's first test starts its timer (scenarios/vitest.core-setup.mjs),
// as Pi loads its modules at import: without it the first test of each file compiled the Go Wasm core inside its own 5 s budget,
// and U-harness-generation-recovery's first test timed out on wasm-go under load.
import assert from "node:assert/strict"
import { existsSync } from "node:fs"
import { join, resolve } from "node:path"
import test from "node:test"
import { pathToFileURL } from "node:url"
import { piSource } from "../lib/run.mjs"

const contract = resolve(import.meta.dirname, "..")
const repo = resolve(contract, "../..")
const facade = join(repo, "durable/core/host/cf/src/facade")
const wasm = join(repo, "durable/core/host/cf/dist/core-go.wasm")

test("the vitest config runs the core setup before a candidate's tests when the run names a core module", async () => {
	const saved = { candidate: process.env.CONTRACT_CANDIDATE, wasm: process.env.DCORE_WASM, pi: process.env.CONTRACT_PI_SOURCE }
	try {
		// The config resolves the reference at import; the gate's own resolution names it when the caller set none.
		process.env.CONTRACT_PI_SOURCE = piSource()
		process.env.CONTRACT_CANDIDATE = facade
		process.env.DCORE_WASM = wasm
		const { default: config } = await import(`${pathToFileURL(join(contract, "scenarios/vitest.contract.config.mjs"))}?with-core`)
		assert.ok(config.test.setupFiles?.some(f => f.endsWith("vitest.core-setup.mjs")), `setupFiles: ${JSON.stringify(config.test.setupFiles)}`)
		delete process.env.DCORE_WASM
		const { default: reference } = await import(`${pathToFileURL(join(contract, "scenarios/vitest.contract.config.mjs"))}?without-core`)
		assert.ok(!reference.test.setupFiles?.some(f => f.endsWith("vitest.core-setup.mjs")), "a run without a core module has no core setup")
	} finally {
		for (const [k, v] of [["CONTRACT_CANDIDATE", saved.candidate], ["DCORE_WASM", saved.wasm], ["CONTRACT_PI_SOURCE", saved.pi]]) if (v === undefined) delete process.env[k]; else process.env[k] = v
	}
})

test("after the core setup, the first Harness.open finds the core module already compiled", { skip: !existsSync(wasm) && "core-go.wasm is not built" }, async () => {
	process.env.CONTRACT_CANDIDATE = facade
	process.env.DCORE_WASM = wasm
	await import(pathToFileURL(join(contract, "scenarios/vitest.core-setup.mjs")).href)
	const { coreFromEnvironment } = await import(pathToFileURL(join(facade, "core-env.ts")).href)
	const pending = Symbol("pending")
	// A compile finishes in a later macrotask; a compiled module comes back within the async function's few microtask hops. Ten
	// microtasks separate the two without any timer.
	let ticks = Promise.resolve()
	for (let i = 0; i < 10; i++) ticks = ticks.then(() => {})
	const first = await Promise.race([coreFromEnvironment(), ticks.then(() => pending)])
	assert.notEqual(first, pending, "the core module was still compiling when the first test would start")
	assert.ok(first instanceof WebAssembly.Module)
})
