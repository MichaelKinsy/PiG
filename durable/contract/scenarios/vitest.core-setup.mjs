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

// The reference's read caches (session.ts CachedStorage) are the core's own here. A test that spies on `CachedStorage.prototype.published` meets
// the Session's write-through through the host's hook: each stored batch reaches the reference's class, as `host.stored` does at publication.
const piSource = process.env.CONTRACT_PI_SOURCE
if (piSource !== undefined) {
	const durable = join(piSource, "packages", "durable", "src")
	const { CachedStorage } = await import(join(durable, "session", "cached-storage.ts"))
	const { MemoryStorage } = await import(join(durable, "storage", "memory.ts"))
	const caches = new CachedStorage(new MemoryStorage())
	globalThis[Symbol.for("pi-durable.stored")] = writes => caches.published(writes)
}
