// Preload (`node --import hooks/register.mjs`): installs the resolve hook, the deterministic environment and the trace.
// Everything that decides what a run captures is configured by environment variables, so a child process inherits it:
//   CONTRACT_PI_SOURCE    checkout of the reference (packages/*/src), required
//   CONTRACT_TRACE        trace file (JSON Lines, CONTRACT section 7.6), required
//   CONTRACT_CANDIDATE    module that replaces packages/durable/src/index.ts (the candidate's JS API, ADR D12)
//   CONTRACT_TAPE         tape file; CONTRACT_TAPE_MODE record | replay
//   CONTRACT_STORE_DIR    directory for the stores capsql creates (default: a temp dir)
//   CONTRACT_CRASH_AFTER  exit the process (no close, no flush) right after this many commits
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
		candidate: process.env.CONTRACT_CANDIDATE ? `${pathToFileURL(process.env.CONTRACT_CANDIDATE).href.replace(/\/$/, "")}/` : undefined,
	},
})

// The candidate's storage modules reach capsql through this global: `openStore(path)` returns a host-loop store (capturingStore)
// whose transactions are traced like the reference's, so the candidate cannot write a store the contract does not see.
globalThis[Symbol.for("pig.contract")] = {
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

// A candidate's own start-up (loading its core module, for one): impls.toml `setup`, run once before the scenario program.
if (process.env.CONTRACT_CANDIDATE_SETUP) await import(pathToFileURL(process.env.CONTRACT_CANDIDATE_SETUP).href)
