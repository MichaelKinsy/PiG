// Run Pi's pi.live draft helpers (packages/durable/src/harness/live.ts endRun, addCompactionStatus, removeCompactionStatus, toolSlot, finishSlot, clearProgress, compactionStatus; generation.ts handOver via a load hook) on a tracked document (chord delta track) over programs from stdin.
// Program: {live, steps: [{op: "endRun", task, settlement} | {op: "addCompaction", status} | {op: "removeCompaction", task} | {op: "finishSlot", task, entry?} | {op: "clearProgress", task} | {op: "handOver", from, to} | {op: "setAttempt", task, attempt}]}.
// stdout: per program {settled: [[submission, settlement]], ops, value} after preparing the change.
// usage: node live_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai)
import module from "node:module";
import { pathToFileURL } from "node:url";
import { chordSourceURL } from "../../internal/oraclesupport/chord_resolve.mjs";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		const chordSource = chordSourceURL(packages, specifier);
		if (chordSource) return next(chordSource, context);
		if (specifier.startsWith("@earendil-works/pi-ai")) return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
});
const load = (path) => import(pathToFileURL(`${packages}${path}`).href);
const live = await load("durable/src/harness/live.ts");
const { handOver } = await load("durable/src/harness/generation.ts");
const { track } = await load("chord/src/delta/tracker.ts");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const program of JSON.parse(input)) {
	const tracker = track(program.live);
	const change = tracker.beginChange();
	const state = change.state;
	const settled = [];
	const tx = { settleSubmission: (id, settlement) => settled.push([id, settlement]) };
	for (const step of program.steps) {
		switch (step.op) {
			case "endRun": live.endRun(tx, state, step.task, step.settlement); break;
			case "addCompaction": live.addCompactionStatus(state, step.status); break;
			case "removeCompaction": live.removeCompactionStatus(state, step.task); break;
			// Pi 1.2.0 finishSlot(slot) takes no entry (live.ts:201); a model-issued call's caller sets it after (tool.ts:624-625).
			case "finishSlot": { const slot = live.toolSlot(state, step.task); if (slot !== undefined) { live.finishSlot(slot); if (step.entry !== undefined) slot.entry = step.entry; } break; }
			case "clearProgress": { const slot = live.toolSlot(state, step.task); if (slot !== undefined) live.clearProgress(slot); break; }
			case "handOver": handOver(state, step.from, step.to); break;
			case "setAttempt": { const status = live.compactionStatus(state, step.task); if (status !== undefined) status.attempt = step.attempt; break; }
		}
	}
	const prepared = change.prepare();
	results.push({ settled, ops: prepared.ops, value: prepared.value });
}
process.stdout.write(JSON.stringify(results));
