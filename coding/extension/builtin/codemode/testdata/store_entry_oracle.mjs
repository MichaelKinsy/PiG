// Runs the pinned Pi executeCodemode with a session context whose branch is empty, on the scripts read from stdin, and returns the data of the codemode-store entry each one appends.
// A plain process, not the Pi oracle's eval: a Worker started from there inherits --input-type and refuses to start.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { Worker } from "node:worker_threads";
const root = process.env.PI_ORACLE_ROOT;
const { executeCodemode } = await import(pathToFileURL(root + "/pi-coding-agent/extensions/codemode/execute.js"));
// A worker keeps its slot of the two executeCodemode admits until it has exited, which is after executeCodemode has returned (execute.ts acquireWorker), and a script that
// finds both slots taken waits for one, and that wait counts toward its timeout_ms (#11184). Each script therefore starts once the workers of the earlier ones have exited, so none waits. The exit listeners are
// added after executeCodemode's own, which give the slot back, so the slot is free when they run. executeCodemode unrefs a worker it has asked to terminate, so the wait refs it again.
const workers = [];
const once = Worker.prototype.once;
Worker.prototype.once = function (event, listener) {
	const result = once.call(this, event, listener);
	if (event === "exit") workers.push({ worker: this, exited: new Promise((resolve) => once.call(this, "exit", resolve)) });
	return result;
};
const out = [];
for (const code of JSON.parse(readFileSync(0, "utf8"))) {
	const entries = [];
	const ctx = { sessionManager: { getBranch: () => [] }, tools: [], cwd: "/" };
	await executeCodemode("call", { code }, undefined, undefined, ctx, { appendEntry: (type, data) => entries.push({ type, data: JSON.stringify(data) }) });
	out.push(entries);
	await Promise.all(
		workers.splice(0).map(({ worker, exited }) => {
			worker.ref();
			return exited;
		}),
	);
}
process.stdout.write(JSON.stringify(out));
