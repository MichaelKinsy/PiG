// Run one scenario as a child process under the hooks and write its trace (CONTRACT sections 5.1 and 7.6). The child is a
// real process, so a crash (CONTRACT_CRASH_AFTER, SIGKILL after a commit) is a real crash: no close(), no flush.
import { spawn } from "node:child_process"
import { appendFileSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync, existsSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

export const CONTRACT_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..")
export const REGISTER = join(CONTRACT_DIR, "hooks", "register.mjs")

async function lock(dir) {
	mkdirSync(join(dir, ".."), { recursive: true })
	for (;;) {
		try { mkdirSync(dir); break } catch (e) { if (e.code !== "EEXIST") throw e; await new Promise(r => setTimeout(r, 50)) }
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
		PATH: process.env.PATH, HOME: join(tmp, "home"), TMPDIR: tmp, TZ: "UTC", LANG: "C.UTF-8",
		CONTRACT_PI_SOURCE: pi, CONTRACT_TRACE: childTrace, CONTRACT_STORE_DIR: storeDir,
		DETENV_SEED: process.env.DETENV_SEED ?? "1", DETENV_EPOCH: process.env.DETENV_EPOCH ?? "1800000000000",
		PI_DURABLE_SOURCE_ROOT: pi,
		// The core builds the integrator's gate hands the run (durable/core/contract/gate.sh) and the facade's toolchain choice.
		...Object.fromEntries(Object.entries(process.env).filter(([k]) => /^(DCORE_|DURABLE_CORE_)/.test(k))),
		...o.env,
	}
	if (o.candidate) env.CONTRACT_CANDIDATE = resolve(o.candidate)
	if (o.tape) { env.CONTRACT_TAPE = resolve(o.tape.path); env.CONTRACT_TAPE_MODE = o.tape.mode }
	if (o.crashAfter !== undefined) env.CONTRACT_CRASH_AFTER = String(o.crashAfter)
	const child = spawn(process.execPath, ["--conditions=source", "--experimental-strip-types", "--no-warnings", "--import", REGISTER, o.script, ...(o.args ?? [])], { cwd: join(pi, "packages", "durable"), env, stdio: ["ignore", "pipe", "pipe"] })
	let stdout = "", stderr = ""
	child.stdout.on("data", d => (stdout += d))
	child.stderr.on("data", d => (stderr += d))
	const timer = setTimeout(() => child.kill("SIGKILL"), o.timeoutMs ?? 120_000)
	const [code, signal] = await new Promise(res => child.on("close", (c, s) => res([c, s])))
	clearTimeout(timer)
	release()
	const trace = join(o.out, `${o.name}.trace`)
	writeFileSync(trace, JSON.stringify({ t: "meta", scenario: o.scenario ?? o.name, impl: o.impl ?? "pi-durable", pin: "da866ada", clock: Number(env.DETENV_EPOCH), seed: Number(env.DETENV_SEED), crashAfter: o.crashAfter }) + "\n")
	if (existsSync(childTrace)) appendFileSync(trace, readFileSync(childTrace, "utf8"))
	for (const line of stdout.split("\n").filter(Boolean)) appendFileSync(trace, JSON.stringify({ t: "out", stream: "stdout", text: line }) + "\n")
	appendFileSync(trace, JSON.stringify({ t: "end", code, signal }) + "\n")
	const stores = existsSync(storeDir) ? readdirSync(storeDir).filter(f => f.endsWith(".sqlite")).sort().map(f => join(storeDir, f)) : []
	return { trace, stores, storeDir, code, signal, stdout, stderr }
}
