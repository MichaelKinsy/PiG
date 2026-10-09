// Native candidates (CONTRACT section 7.1): a process that is not Node and cannot produce SQLite session changesets. The
// executable applies its commits to the store it is given and writes a trace of statements only (durable/core/contracttest
// Writer); this module runs it, then replays its statements onto the store the run started from (lib/replay.mjs) so the
// trace has the row changes the comparison needs. The executable's own store file is the one compared at the store level.
//
//   exe --db <store file, opened in place> --trace <statement trace to write> [--turns N --tools N --variant full|incr
//       --seed N --stream CHARS --partial-ms MS --fault NAME --payload-kb K --parallel --recover --label L]
//
// CONTRACT_CRASH_AFTER=<seq> in its environment: it kills itself right after writing the commit that consumed that seq.
import { spawn } from "node:child_process"
import { appendFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { readTrace } from "../lib/rowdiff.mjs"
import { replay } from "../lib/replay.mjs"
import { copyStore } from "../lib/store.mjs"

/** Run `exe` as a bench candidate; returns what lib/run.mjs runScenario returns. */
export async function runNative({ exe, extraArgs = [], o }) {
	const storeDir = join(o.out, `${o.name}.stores`)
	rmSync(storeDir, { recursive: true, force: true })
	mkdirSync(storeDir, { recursive: true })
	const db = copyStore(o.store, join(storeDir, "store-0.sqlite"))
	const native = join(o.out, `${o.name}.native.jsonl`)
	const args = ["--db", db, "--trace", native, "--turns", String(o.turns ?? 0), "--tools", String(o.tools ?? 8), "--variant", o.variant ?? "full", ...extraArgs]
	for (const [flag, key] of [["seed", "seed"], ["stream", "stream"], ["partial-ms", "partialMs"], ["fault", "fault"], ["payload-kb", "payloadKb"], ["label", "label"]]) if (o[key]) args.push(`--${flag}`, String(o[key]))
	for (const flag of ["parallel", "recover"]) if (o[flag]) args.push(`--${flag}`)
	const env = { PATH: process.env.PATH, HOME: join(o.out, "home"), TZ: "UTC", ...o.env, ...(o.crashAfter !== undefined ? { CONTRACT_CRASH_AFTER: String(o.crashAfter) } : {}) }
	const child = spawn(exe, args, { env, stdio: ["ignore", "pipe", "pipe"] })
	let stdout = "", stderr = ""
	child.stdout.on("data", d => (stdout += d))
	child.stderr.on("data", d => (stderr += d))
	const timer = setTimeout(() => child.kill("SIGKILL"), o.timeoutMs ?? 600_000)
	const [code, signal] = await new Promise(res => child.on("close", (c, s) => res([c, s])))
	clearTimeout(timer)
	const trace = join(o.out, `${o.name}.trace`)
	const lines = readTrace(native)
	// A killed process has no `end` line; the status comes from the exit.
	const body = lines.filter(l => l.t !== "end")
	const replayed = replay(body, o.store, join(o.out, `${o.name}.replayed`))
	writeFileSync(trace, JSON.stringify({ t: "meta", scenario: o.scenario ?? o.name, impl: o.implLabel ?? "native", clock: 1_800_000_000_000, seed: 1, crashAfter: o.crashAfter }) + "\n")
	for (const l of replayed.trace) if (l.t !== "meta") appendFileSync(trace, JSON.stringify(l) + "\n")
	appendFileSync(trace, JSON.stringify({ t: "end", code, signal }) + "\n")
	void readFileSync
	return { trace, stores: [db], storeDir, code, signal, stdout, stderr }
}
