// Child process groups of the gate. A candidate's Node process spawns the native core (corenative) as its own child, so killing
// only the Node process on a timeout leaves the core running: a core stuck in a step then runs, and allocates, until the host is
// out of memory. Every child here leads its own process group, and the whole group is killed on a timeout and when the gate itself
// ends (exit, SIGINT, SIGTERM, SIGHUP).
import { spawn } from "node:child_process"

const live = new Set()

/** Kill a child's whole process group (a child that is already gone is fine). */
export function killGroup(child, signal = "SIGKILL") {
	try { process.kill(-child.pid, signal) } catch { /* already gone */ }
}

/** spawn with the child as a group leader that the gate kills when it ends. */
export function spawnGroup(cmd, args, options) {
	const child = spawn(cmd, args, { ...options, detached: true })
	live.add(child)
	child.once("close", () => live.delete(child))
	return child
}

/** The groups still running; for tests. */
export const liveGroups = () => live.size

const killAll = () => { for (const child of live) killGroup(child) }
process.on("exit", killAll)
for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) process.on(signal, () => { killAll(); process.exit(128) })
