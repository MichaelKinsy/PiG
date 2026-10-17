// The U and SC rows' start-up for a candidate that runs a core (DCORE_WASM or DCORE_NATIVE): the module is compiled (or the native core started) here, once per test
// file, before the file's first test starts its timer. Pi pays its module load at import, outside every test, and workerd compiles
// a Worker's Wasm once per isolate at load; without this the first test of each file compiled the core inside its own 5 s budget
// (U-harness-generation-recovery on wasm-go timed out under load). Instance creation and _initialize stay inside each Harness.open.
import { existsSync } from "node:fs"
import { join } from "node:path"

const coreEnv = join(process.env.CONTRACT_CANDIDATE, "core-env.ts")
if (existsSync(coreEnv)) {
	const { coreFromEnvironment } = await import(coreEnv)
	await coreFromEnvironment()
}
