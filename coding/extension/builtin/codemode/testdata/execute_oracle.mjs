// Runs the pinned Pi executeCodemode (no session context) on the scripts read from stdin, in a plain process: a Worker started from the Pi oracle's eval inherits its --input-type and refuses to start.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { Worker } from "node:worker_threads";
const root = process.env.PI_ORACLE_ROOT;
const { executeCodemode } = await import(pathToFileURL(root + "/pi-coding-agent/extensions/codemode/execute.js"));
// A worker keeps its slot of the two executeCodemode admits until it has exited, which is after executeCodemode has returned (execute.ts acquireWorker), and a script that
// finds both slots taken fails at once ("Retry after they exit."). Each script therefore starts once the workers of the earlier ones have exited. The exit listeners are
// added after executeCodemode's own, which give the slot back. executeCodemode unrefs a worker it has asked to terminate, so the wait refs it again.
const workers = [];
const once = Worker.prototype.once;
Worker.prototype.once = function (event, listener) {
	const result = once.call(this, event, listener);
	if (event === "exit") workers.push({ worker: this, exited: new Promise((resolve) => once.call(this, "exit", resolve)) });
	return result;
};
/* An unpaired surrogate cannot cross the Go JSON decoder, so text names each one. */
const plain = (text) => text.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, (unit) => "<U+" + unit.charCodeAt(0).toString(16) + ">");
const out = [];
for (const p of JSON.parse(readFileSync(0, "utf8"))) {
	const result = await executeCodemode("call", { code: p.code }, undefined, undefined, undefined, p.maxOutputTokens ? {} : {});
	out.push({
		content: result.content.map((item) => (item.type === "text" ? { type: "text", text: plain(item.text) } : { type: item.type, mimeType: item.mimeType, data: item.data })),
		fullOutputPath: result.details?.fullOutputPath ? "yes" : "",
		calls: result.details?.calls?.length ?? 0,
	});
	await Promise.all(
		workers.splice(0).map(({ worker, exited }) => {
			worker.ref();
			return exited;
		}),
	);
}
process.stdout.write(JSON.stringify(out));
