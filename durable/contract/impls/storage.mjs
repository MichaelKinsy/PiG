// Wrappers of the reference's storage constructors (hooks/resolve.mjs, CONTRACT section 6): `MemoryStorage` and
// `openNodeJsonlStorage` return a SqliteStorage over capsql; `openNodeSqliteStorage` gets capsql directly. The stores live
// in CONTRACT_STORE_DIR as `store-<n>.sqlite`, one file per distinct requested path, so a scenario that closes and reopens
// a path (examples 13 and 31) reopens the same file. Every open is traced; every store that is still open at process exit
// writes its `final` line.
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import { piDurableDatabase } from "../lib/capsql.mjs"
import { openCapture } from "./capture.mjs"

export { openCapture }

const piUrl = rel => pathToFileURL(`${process.env.CONTRACT_PI_SOURCE}/packages/durable/src/${rel}`).href + "?real"
const { SqliteStorage } = await import(piUrl("storage/sqlite/storage.ts"))

// Profile `node` (CONTRACT_STORAGE_PROFILE): the reference's own Node adapter over a capsql DatabaseSync (hooks/resolve.mjs), the hops of a product adapter.
const nodeAdapter = process.env.CONTRACT_STORAGE_PROFILE === "node" ? await import(piUrl("storage/sqlite/node.ts")) : undefined
export const openSqlite = async requested => (nodeAdapter ? nodeAdapter.openNodeSqliteStorage(requested.startsWith(":memory:") ? ":memory:" : requested) : SqliteStorage.open(piDurableDatabase(openCapture(requested))))

/** A Storage whose methods wait for the underlying SqliteStorage to open (the reference's constructors are synchronous). */
function lazy(open) {
	const ready = open()
	ready.catch(() => {})
	return new Proxy({}, {
		get: (_, prop) => {
			if (prop === "then") return undefined
			return async (...args) => { const storage = await ready; return storage[prop](...args) }
		},
	})
}

let memory = 0
export const MemoryStorage = () => class MemoryStorage { constructor() { return lazy(() => openSqlite(`:memory:${memory++}`)) } }
export const openNodeJsonlStorage = () => async directory => openSqlite(join(directory, "jsonl-as-sqlite"))
export const openNodeSqliteStorage = () => async path => openSqlite(path)
