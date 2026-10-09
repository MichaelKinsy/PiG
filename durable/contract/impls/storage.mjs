// Wrappers of the reference's storage constructors (hooks/resolve.mjs, CONTRACT section 6): `MemoryStorage` and
// `openNodeJsonlStorage` return a SqliteStorage over capsql; `openNodeSqliteStorage` gets capsql directly. The stores live
// in CONTRACT_STORE_DIR as `store-<n>.sqlite`, one file per distinct requested path, so a scenario that closes and reopens
// a path (examples 13 and 31) reopens the same file. Every open is traced; every store that is still open at process exit
// writes its `final` line.
import { existsSync, mkdirSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import { Capture, piDurableDatabase } from "../lib/capsql.mjs"
import { copyStore } from "../lib/store.mjs"
import { emit } from "../lib/trace.mjs"

const KEY = Symbol.for("pig.contract.stores")
const state = () => (globalThis[KEY] ??= { dir: process.env.CONTRACT_STORE_DIR ?? mkdtempSync(join(tmpdir(), "contract-stores-")), files: new Map(), open: new Set(), hooked: false })

const piUrl = rel => pathToFileURL(`${process.env.CONTRACT_PI_SOURCE}/packages/durable/src/${rel}`).href + "?real"
const { SqliteStorage } = await import(piUrl("storage/sqlite/storage.ts"))

/** The capsql database for a requested path (a stable file per path), opened as the next store. */
export function openCapture(requested) {
	const s = state()
	mkdirSync(s.dir, { recursive: true })
	let file = s.files.get(requested)
	if (!file) { file = join(s.dir, `store-${s.files.size}.sqlite`); s.files.set(requested, file) }
	if (!existsSync(file) && existsSync(requested)) copyStore(requested, file)
	const cap = new Capture(file)
	s.open.add(cap)
	const finish = cap.finish.bind(cap)
	cap.finish = () => { s.open.delete(cap); finish() }
	if (!s.hooked) {
		s.hooked = true
		process.on("exit", code => { for (const c of [...s.open]) c.finish(); emit({ t: "exit", code }) })
	}
	return cap
}

export const openSqlite = async requested => SqliteStorage.open(piDurableDatabase(openCapture(requested)))

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
