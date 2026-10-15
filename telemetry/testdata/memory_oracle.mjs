// Drive the installed Pi InMemoryTelemetryContext (memory.ts) with seeded random span programs and print the recorded spans, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-telemetry/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { InMemoryTelemetryContext } = await import(pathToFileURL(root + "dist/memory.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

// {"$undef":true} stands for undefined, which JSON cannot carry.
const revive = (attributes) => {
	if (attributes === null || attributes === undefined) return undefined;
	const out = {};
	for (const [name, value] of Object.entries(attributes)) out[name] = value !== null && typeof value === "object" && !Array.isArray(value) && value.$undef ? undefined : value;
	return out;
};
const fail = (outcome) => {
	if (outcome.kind === "error" || outcome.kind === "plain") {
		const error = new Error(outcome.message);
		if (outcome.name !== "Error") error.name = outcome.name;
		throw error;
	}
	if (outcome.kind === "nonError") throw outcome.value;
};
async function apply(span, op, kept) {
	switch (op.op) {
		case "attrs": span.setAttributes(revive(op.attrs)); break;
		case "event": span.addEvent(op.name, revive(op.attrs)); break;
		case "status": span.setStatus(op.status); break;
		case "keep": kept.push(span); break;
		case "useKept": if (kept.length > 0) await apply(kept[op.idx % kept.length], op.sub, kept); break;
		case "child": {
			const body = async (child) => { for (const sub of op.body) await apply(child, sub, kept); fail(op.outcome); };
			try { await span.startSpan({ name: op.name, attributes: revive(op.attrs) }, body); } catch {}
			break;
		}
	}
}
const results = [];
for (const program of JSON.parse(input)) {
	const context = new InMemoryTelemetryContext();
	const kept = [];
	for (const root of program) {
		try { await context.startSpan({ name: root.name, attributes: revive(root.attrs) }, async (span) => { for (const op of root.body) await apply(span, op, kept); fail(root.outcome); }); } catch {}
	}
	results.push(context.getSpans());
}
process.stdout.write(JSON.stringify(results));
