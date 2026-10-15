// Run one scenario as a child process under the hooks and write its trace (CONTRACT sections 5.1 and 7.6). The child is a
// real process, so a crash (CONTRACT_CRASH_AFTER, SIGKILL after a commit) is a real crash: no close(), no flush.
import { killGroup, spawnGroup } from "./procs.mjs"
import { appendFileSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync, existsSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

export const CONTRACT_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..")
export const REGISTER = join(CONTRACT_DIR, "hooks", "register.mjs")

/**
 * Whether a lock directory exists and was left by a process that is gone: its pid names no process, or it has no usable pid a few
 * seconds after it was made (its owner was killed between `mkdir` and writing the pid). A missing directory is not abandoned: a
 * waiter that removed it on that answer could remove a lock another waiter has just made. An empty pid file is not pid 0, which
 * `process.kill` would read as this process group.
 */
function lockAbandoned(dir) {
	let pid = NaN
	try { pid = Number(readFileSync(join(dir, "pid"), "utf8")) } catch { /* no pid yet */ }
	if (!(Number.isInteger(pid) && pid > 0)) { try { return Date.now() - statSync(dir).mtimeMs >= 5000 } catch { return false } }
	try { process.kill(pid, 0); return false } catch (e) { return e.code !== "EPERM" }
}

/**
 * Remove an abandoned lock under a takeover guard, and say whether this waiter held the guard. Paths, not directories, are renamed
 * and removed, so a waiter that judged the lock abandoned and then removed or renamed `dir` without the guard could remove the lock
 * another waiter had made in between, and two runs would hold one lock. Under the guard nobody else removes an abandoned lock (its
 * owner is gone, and `mkdir` fails while it exists), so the check and the removal see one directory. A guard left by a waiter killed
 * inside these few synchronous calls expires after a few seconds.
 */
function takeOver(dir) {
	const guard = `${dir}.takeover`
	try { mkdirSync(guard) } catch (e) {
		if (e.code !== "EEXIST") throw e
		try { if (Date.now() - statSync(guard).mtimeMs >= 5000) rmSync(guard, { recursive: true, force: true }) } catch { /* released meanwhile */ }
		return false
	}
	try { if (lockAbandoned(dir)) rmSync(dir, { recursive: true, force: true }) } finally { rmSync(guard, { recursive: true, force: true }) }
	return true
}

/** Serialise on a directory lock. A lock left by a process that was killed (SIGKILL cannot run a release) is taken over instead of waited on forever. */
export async function lock(dir) {
	mkdirSync(join(dir, ".."), { recursive: true })
	for (;;) {
		try { mkdirSync(dir) } catch (e) {
			if (e.code !== "EEXIST") throw e
			if (lockAbandoned(dir) && takeOver(dir)) continue
			await new Promise(r => setTimeout(r, 50))
			continue
		}
		writeFileSync(join(dir, "pid"), String(process.pid))
		break
	}
	return () => rmSync(dir, { recursive: true, force: true })
}

/** The reference checkout: CONTRACT_PI_SOURCE, else the cache `make durable-contract-setup` fills. */
export function piSource() {
	const dir = process.env.CONTRACT_PI_SOURCE ?? join(process.env.DURABLE_CONTRACT_CACHE ?? join(CONTRACT_DIR, ".cache"), "pi-main")
	if (!existsSync(join(dir, "packages", "durable", "src", "index.ts"))) throw new Error(`reference checkout not found at ${dir}; run make durable-contract-setup or set CONTRACT_PI_SOURCE`)
	return resolve(dir)
}

/** The release the corpus keeps readable (PIN `release`): CONTRACT_PI_104_SOURCE, else the cache `make durable-contract-setup` fills. */
export function legacySource() {
	const dir = process.env.CONTRACT_PI_104_SOURCE ?? join(process.env.DURABLE_CONTRACT_CACHE ?? join(CONTRACT_DIR, ".cache"), "pi-1.0.4")
	if (!existsSync(join(dir, "packages", "durable", "src", "index.ts"))) throw new Error(`1.0.4 checkout not found at ${dir}; run make durable-contract-setup or set CONTRACT_PI_104_SOURCE`)
	return resolve(dir)
}

export const readLines = path => readFileSync(path, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))

/**
 * Run `row.script` (a path relative to the reference checkout) and write `<out>/<name>.trace`.
 * @param {object} o
 * @param {string} o.script absolute path of the scenario module
 * @param {string} o.out output directory
 * @param {string} o.name file stem of the outputs
 * @param {string} [o.impl] label for the meta line
 * @param {string} [o.scenario] scenario id for the meta line
 * @param {string} [o.candidate] candidate source directory (aliases packages/durable/src)
 * @param {{mode: "record"|"replay", path: string}} [o.tape]
 * @param {number} [o.crashAfter] SIGKILL after the commit that consumed this seq
 * @param {string} [o.tmpKey] names the fixed temporary directory (default: the file stem before the first dot of `name`)
 * @param {boolean} [o.keepTmp] keep the temporary directory of an earlier run
 * @param {string} [o.storeDir] store directory (default <out>/<name>.stores)
 * @param {string} [o.source] reference checkout to run (default piSource())
 * @param {string[]} [o.args] scenario arguments
 * @param {Record<string,string>} [o.env]
 * @param {number} [o.timeoutMs]
 */
export async function runScenario(o) {
	const pi = o.source ?? piSource()
	mkdirSync(o.out, { recursive: true })
	// A run starts from an empty store directory: stores left by an earlier run of the same name would be reopened as history.
	const storeDir = o.storeDir ?? join(o.out, `${o.name}.stores`)
	if (!o.storeDir) rmSync(storeDir, { recursive: true, force: true })
	mkdirSync(storeDir, { recursive: true })
	const childTrace = join(o.out, `${o.name}.child.jsonl`)
	// A scenario's temporary directory is one fixed path, so a path a scenario stores is the same for every implementation.
	// Runs of one scenario are serialised on a lock; `keepTmp` leaves the directory for the run that recovers a crashed one.
	const tmp = join(tmpdir(), "pig-contract", o.tmpKey ?? o.name.replace(/\..*$/, ""))
	const release = await lock(`${tmp}.lock`)
	if (!o.keepTmp) rmSync(tmp, { recursive: true, force: true })
	mkdirSync(tmp, { recursive: true })
	const env = {
		// hooks/shims first: determinism shims (cat writes its diagnostics in one piece), the same for the reference and every candidate.
		PATH: `${join(CONTRACT_DIR, "hooks", "shims")}:${process.env.PATH}`, HOME: join(tmp, "home"), TMPDIR: tmp, TZ: "UTC", LANG: "C.UTF-8",
		CONTRACT_PI_SOURCE: pi, CONTRACT_TRACE: childTrace, CONTRACT_MEM: join(o.out, `${o.name}.mem.jsonl`), CONTRACT_STORE_DIR: storeDir,
		DETENV_SEED: process.env.DETENV_SEED ?? "1", DETENV_EPOCH: process.env.DETENV_EPOCH ?? "1800000000000",
		PI_DURABLE_SOURCE_ROOT: pi,
		// The core builds the integrator's gate hands the run (durable/core/contract/gate.sh) and the facade's toolchain choice.
		...Object.fromEntries(Object.entries(process.env).filter(([k]) => /^(DCORE_|DURABLE_CORE_|DETENV_DEBUG(_MIN)?$|CONTRACT_TICKS$|CONTRACT_API_TRACE$|CONTRACT_STORAGE_PROFILE$|CONTRACT_HEAPSNAP$|CONTRACT_HEAPPROF$|CONTRACT_CPUPROF$)/.test(k))),
		...o.env,
	}
	// The tape's depth diagnostic (lib/tape.mjs): each run writes its own <name>.depth.jsonl beside its trace.
	if (process.env.CONTRACT_DEPTH_DEBUG) env.CONTRACT_DEPTH_DEBUG = join(o.out, `${o.name}.depth.jsonl`)
	if (o.candidate) env.CONTRACT_CANDIDATE = resolve(o.candidate)
	if (o.tape) { env.CONTRACT_TAPE = resolve(o.tape.path); env.CONTRACT_TAPE_MODE = o.tape.mode }
	if (o.crashAfter !== undefined) env.CONTRACT_CRASH_AFTER = String(o.crashAfter)
	const child = spawnGroup(process.execPath, ["--conditions=source", "--experimental-strip-types", "--no-warnings", "--import", REGISTER, o.script, ...(o.args ?? [])], { cwd: join(pi, "packages", "durable"), env, stdio: ["ignore", "pipe", "pipe"] })
	let stdout = "", stderr = ""
	child.stdout.on("data", d => (stdout += d))
	child.stderr.on("data", d => (stderr += d))
	const timer = setTimeout(() => killGroup(child), o.timeoutMs ?? Number(process.env.CONTRACT_RUN_TIMEOUT_MS ?? 300_000))
	const [code, signal] = await new Promise(res => child.on("close", (c, s) => res([c, s])))
	clearTimeout(timer)
	release()
	const trace = join(o.out, `${o.name}.trace`)
	writeFileSync(trace, JSON.stringify({ t: "meta", scenario: o.scenario ?? o.name, impl: o.impl ?? "pi-durable", pin: "06a46693", clock: Number(env.DETENV_EPOCH), seed: Number(env.DETENV_SEED), crashAfter: o.crashAfter }) + "\n")
	if (existsSync(childTrace)) appendFileSync(trace, readFileSync(childTrace, "utf8"))
	for (const line of stdout.split("\n").filter(Boolean)) appendFileSync(trace, JSON.stringify({ t: "out", stream: "stdout", text: line }) + "\n")
	appendFileSync(trace, JSON.stringify({ t: "end", code, signal }) + "\n")
	const stores = existsSync(storeDir) ? readdirSync(storeDir).filter(f => f.endsWith(".sqlite")).sort().map(f => join(storeDir, f)) : []
	return { trace, stores, storeDir, code, signal, stdout, stderr, mem: join(o.out, `${o.name}.mem.jsonl`) }
}
