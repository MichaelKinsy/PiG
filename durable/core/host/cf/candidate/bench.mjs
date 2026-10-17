// The bench module of the gate (durable/contract/lib/impl.mjs): the same scenario program as the reference, run with the
// candidate JS API above so the Durable core, not pi-durable, runs the turns.
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { CONTRACT_DIR, runScenario } from "@@CONTRACT@@/lib/run.mjs"

const here = dirname(fileURLToPath(import.meta.url))
let unique = 0

export function runBench(o) {
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
	return runScenario({
		script: join(CONTRACT_DIR, "scenarios", "bench.mjs"), candidate: join(here, "src"), out: o.out, name: o.name, scenario: o.scenario,
		impl: o.implLabel, args, crashAfter: o.crashAfter, tmpKey: `bench-${process.pid}-${unique++}`, timeoutMs: o.timeoutMs,
	})
}
