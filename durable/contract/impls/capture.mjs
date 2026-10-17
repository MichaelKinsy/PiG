// The capsql databases the wrappers open (CONTRACT section 6): one stable store file per requested path, every open traced, every
// store still open at process exit writes its `final` line. Kept free of reference imports so that the `node:sqlite` stand-in
// (node-sqlite-capture.mjs) can use it while storage.mjs is still loading the reference.
import { existsSync, mkdirSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { Capture } from "../lib/capsql.mjs"
import { copyStore } from "../lib/store.mjs"
import { emit } from "../lib/trace.mjs"

const KEY = Symbol.for("pig.contract.stores")
const state = () => (globalThis[KEY] ??= { dir: process.env.CONTRACT_STORE_DIR ?? mkdtempSync(join(tmpdir(), "contract-stores-")), files: new Map(), open: new Set(), hooked: false })

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

