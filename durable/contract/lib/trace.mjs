// The trace (CONTRACT section 7.6): JSON Lines, one object per event, appended synchronously so a process killed after a
// commit has already written every line up to it. A trace belongs to one process; the matrix concatenates the runs of one
// scenario by writing each to its own file.
import { appendFileSync, closeSync, openSync, writeFileSync } from "node:fs"

const KEY = Symbol.for("pig.contract.trace")

/** Create the trace file and make `emit` write to it. */
export function openTrace(path) {
	writeFileSync(path, "")
	const fd = openSync(path, "a")
	globalThis[KEY] = { path, fd, emit: line => appendFileSync(fd, JSON.stringify(line) + "\n") }
	return globalThis[KEY]
}
export const trace = () => globalThis[KEY]
export const emit = line => globalThis[KEY]?.emit(line)
export const closeTrace = () => { const t = globalThis[KEY]; if (t) { closeSync(t.fd); delete globalThis[KEY] } }
