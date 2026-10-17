// Runs the pinned Pi executeCodemode with a session context whose branch is empty, on the scripts read from stdin, and returns the data of the codemode-store entry each one appends.
// A plain process, not the Pi oracle's eval: a Worker started from there inherits --input-type and refuses to start.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = process.env.PI_ORACLE_ROOT;
const { executeCodemode } = await import(pathToFileURL(root + "/pi-coding-agent/extensions/codemode/execute.js"));
const out = [];
for (const code of JSON.parse(readFileSync(0, "utf8"))) {
	const entries = [];
	const ctx = { sessionManager: { getBranch: () => [] }, tools: [], cwd: "/" };
	await executeCodemode("call", { code }, undefined, undefined, ctx, { appendEntry: (type, data) => entries.push({ type, data: JSON.stringify(data) }) });
	out.push(entries);
}
process.stdout.write(JSON.stringify(out));
