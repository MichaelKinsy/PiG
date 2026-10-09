// Implementations under test. An implementation runs the durable-bench scenario program on a store and writes a trace in
// the format of CONTRACT section 7.6; the matrix and the gate are written against this one function.
//   runBench(impl, { store, out, name, turns, tools, variant, stream, partialMs, seed, crashAfter, recover, parallel })
// `store` is a store file (with its WAL beside it) the run opens a copy of; the run's own store lands in `<out>/<name>.stores`.
import { existsSync } from "node:fs"
import { join } from "node:path"
import { injectedCopy } from "./inject.mjs"
import { CONTRACT_DIR, runScenario } from "./run.mjs"

let unique = 0

/** pi-durable (the reference checkout): bench.mjs under the hooks. */
async function runPi(o) {
	const args = ["--db", o.store ?? join(o.out, "empty.db"), "--turns", String(o.turns ?? 0), "--tools", String(o.tools ?? 8), "--variant", o.variant ?? "full"]
	if (o.seed) args.push("--seed", String(o.seed))
	if (o.stream) args.push("--stream", String(o.stream))
	if (o.partialMs) args.push("--partial-ms", String(o.partialMs))
	if (o.recover) args.push("--recover")
	if (o.label) args.push("--label", o.label)
	if (o.compaction) args.push("--compaction")
	if (o.window) args.push("--window", String(o.window))
	if (o.parallel) args.push("--parallel")
	if (o.fault) args.push("--fault", o.fault)
	if (o.subagent) args.push("--subagent")
	if (o.payloadKb) args.push("--payload-kb", String(o.payloadKb))
	return runScenario({ script: join(CONTRACT_DIR, "scenarios", "bench.mjs"), out: o.out, name: o.name, scenario: o.scenario, impl: o.implLabel ?? "pi-durable", args, crashAfter: o.crashAfter, candidate: o.candidate, env: o.env, tmpKey: `bench-${process.pid}-${unique++}`, timeoutMs: o.timeoutMs })
}

/**
 * A candidate JS facade (`candidate_dir`): the same bench scenario program, with the candidate's modules replacing the reference's.
 * Every candidate that has one needs no bench module of its own.
 */
export const facadeBench = ({ dir, env }) => o => runPi({ ...o, candidate: dir, env: { ...env, ...o.env } })

/** The vendored TypeScript control: the bench slice only, `bench` fingerprints only (no full-context `ctx`). */
async function runTs(o) {
	const args = ["--db", o.store, "--turns", String(o.turns ?? 0), "--tools", String(o.tools ?? 8)]
	if (o.variant === "incr") args.push("--stub", "jsincr")
	for (const [flagName, key] of [["seed", "seed"], ["stream", "stream"], ["fault", "fault"], ["payload-kb", "payloadKb"]]) if (o[key]) args.push(`--${flagName}`, String(o[key]))
	if (o.parallel) args.push("--parallel")
	if (o.recover) args.push("--recover")
	if (o.label) args.push("--label", o.label)
	return runScenario({ script: join(CONTRACT_DIR, "impls", "ts-driver.mjs"), out: o.out, name: o.name, scenario: o.scenario, impl: o.implLabel ?? "ts-core", args, crashAfter: o.crashAfter, env: o.env, tmpKey: `bench-${process.pid}-${unique++}`, timeoutMs: o.timeoutMs })
}

export const IMPLS = {
	pi: { label: "pi-durable@da866ada", reference: true, runBench: runPi },
	ts: {
		label: "ts-core (durable-core-ts de1fac200)", contexts: ["bench"], slice: "bench", runBench: runTs,
		/** What the bench slice of the control cannot run: one conversation, sequential rounds, no streaming, no faults, no sub-agents. */
		unsupported: o => (o.parallel ? "parallel rounds" : o.stream ? "streaming partials" : o.fault ? "provider faults and retries" : o.subagent ? "sub-agents" : o.seed ? "seeding" : undefined),
	},
}

/** Register an implementation (the vendored TS control and the Wasm and native cores add themselves). */
export function registerImpl(name, impl) { IMPLS[name] = impl }

export function runBench(name, options) {
	const impl = IMPLS[name]
	if (!impl) throw new Error(`unknown implementation ${name}; known: ${Object.keys(IMPLS).join(", ")}`)
	// A run on a store that is not there would silently create an empty one and pass nothing through.
	if (options.store && !existsSync(options.store)) throw new Error(`runBench ${name}: store ${options.store} does not exist`)
	// Unknown-field injection happens on a copy before the run, identically for every implementation.
	const store = options.inject?.length ? injectedCopy(options.store, options.out, `${options.name}.injected`, options.inject) : options.store
	return impl.runBench({ implLabel: impl.label, ...options, store })
}
