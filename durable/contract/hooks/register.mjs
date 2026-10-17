// Preload (`node --import hooks/register.mjs`): installs the resolve hook, the deterministic environment and the trace.
// Everything that decides what a run captures is configured by environment variables, so a child process inherits it:
//   CONTRACT_PI_SOURCE    checkout of the reference (packages/*/src), required
//   CONTRACT_TRACE        trace file (JSON Lines, CONTRACT section 7.6), required
//   CONTRACT_CANDIDATE    module that replaces packages/durable/src/index.ts (the candidate's JS API, ADR D12)
//   CONTRACT_TAPE         tape file; CONTRACT_TAPE_MODE record | replay
//   CONTRACT_STORE_DIR    directory for the stores capsql creates (default: a temp dir)
//   CONTRACT_CRASH_AFTER  exit the process (no close, no flush) right after this many commits
//   CONTRACT_COUNTS       count file of cli/counts.mjs (lib/counts.mjs)
import { register } from "node:module"
import { pathToFileURL } from "node:url"
import "../lib/detenv.mjs"
import { openTrace } from "../lib/trace.mjs"

for (const name of ["CONTRACT_PI_SOURCE", "CONTRACT_TRACE"]) if (!process.env[name]) throw new Error(`${name} is not set`)
openTrace(process.env.CONTRACT_TRACE)
register("./resolve.mjs", import.meta.url, {
	data: {
		piRoot: pathToFileURL(`${process.env.CONTRACT_PI_SOURCE}/`).href,
		contract: new URL("..", import.meta.url).href,
		apiTrace: !!process.env.CONTRACT_API_TRACE,
		storageProfile: process.env.CONTRACT_STORAGE_PROFILE,
		candidate: process.env.CONTRACT_CANDIDATE ? `${pathToFileURL(process.env.CONTRACT_CANDIDATE).href.replace(/\/$/, "")}/` : undefined,
	},
})

// The candidate's storage modules reach capsql through this global: `openStore(path)` returns a host-loop store (capturingStore)
// whose transactions are traced like the reference's, so the candidate cannot write a store the contract does not see.
globalThis[Symbol.for("pig.contract")] = {
	/** The Wasm cores the candidate facade instantiated (it adds each one): bench.mjs reads their linear memory per turn. */
	cores: new Set(),
	/** The SqliteDatabase facade of pi-durable's storage/sqlite over capsql, for a candidate that keeps Pi's SqliteStorage design. */
	openDatabase: async path => {
		const { openCapture } = await import("../impls/storage.mjs")
		const { piDurableDatabase } = await import("../lib/capsql.mjs")
		return piDurableDatabase(openCapture(path))
	},
	openStore: async path => {
		const { openCapture } = await import("../impls/storage.mjs")
		const { capturingStore } = await import("../lib/capsql.mjs")
		return capturingStore(openCapture(path))
	},
}

// CONTRACT_COUNTS=<file> (cli/counts.mjs): count statements, rows and core crossings; installed before the setup makes a core.
if (process.env.CONTRACT_COUNTS) await (await import("../lib/counts.mjs")).install()

// A candidate's own start-up (loading its core module, for one): impls.toml `setup`, run once before the scenario program.
if (process.env.CONTRACT_CANDIDATE_SETUP) await import(pathToFileURL(process.env.CONTRACT_CANDIDATE_SETUP).href)

// Microtask-hop profile (cli/ticks.mjs): a microtask that re-queues itself counts the passes of the microtask queue, so a
// commit's `tick` (lib/capsql.mjs) measures how many Promise hops the code took to reach it. Pi's ordering of concurrent work
// is a race of such hops; the count shows how far a candidate's ordering is from the reference's. The counter stops after a
// bound so a run that waits for a timer or I/O is not starved forever.
if (process.env.CONTRACT_TICKS) {
	let passes = 0
	globalThis.__contractTick = () => passes
	const spin = () => { if (passes++ < 1_000_000) queueMicrotask(spin) }
	queueMicrotask(spin)
}
