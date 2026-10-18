// Drive the installed Pi InMemoryTelemetryContext (memory.ts) with seeded random span programs and print the recorded spans, never a translated oracle.
// The programs link spans, extract carriers and continue spans under the extracted parents. The recorder's random scope UUID is printed as SCOPE.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-telemetry/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { InMemoryTelemetryContext } = await import(pathToFileURL(root + "dist/memory.js"));
const { NOOP_TELEMETRY_CONTEXT } = await import(pathToFileURL(root + "dist/noop.js"));
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
// A link target: a kept span, nothing (undefined), the shared no-op context, or the recorder itself (a root, which has no carrier).
const target = (recorder, kept, link) => {
	switch (link.target) {
		case "kept": return kept.length > 0 ? kept[link.idx % kept.length] : undefined;
		case "noop": return NOOP_TELEMETRY_CONTEXT;
		case "recorder": return recorder;
	}
	return undefined;
};
const linksOf = (recorder, kept, links) => (links ? links.map((link) => ({ context: target(recorder, kept, link), attributes: revive(link.attrs) })) : undefined);
const FOREIGN = "0a0b0c0d-0e0f-4a0b-8c0d-0e0f0a0b0c0d";
// The carrier a remote op extracts: the injected carrier of a kept span, one of the malformed or foreign shapes, or none.
const carrierOf = (recorder, kept, op) => {
	const own = kept.length > 0 ? recorder.inject(kept[op.idx % kept.length]) : undefined;
	const scope = own?.["pi-memory-scope"];
	switch (op.carrier) {
		case "kept": return own;
		case "extra": return own && { ...own, extra: "key" };
		case "missingSpan": return own && { "pi-memory-scope": scope };
		case "badScope": return { "pi-memory-scope": "X", "pi-memory-span": "1" };
		case "upperScope": return { "pi-memory-scope": FOREIGN.toUpperCase(), "pi-memory-span": "1" };
		case "spanZero": return { "pi-memory-scope": FOREIGN, "pi-memory-span": "0" };
		case "spanLeadingZero": return { "pi-memory-scope": FOREIGN, "pi-memory-span": "01" };
		case "spanSign": return { "pi-memory-scope": FOREIGN, "pi-memory-span": "+1" };
		case "spanUnsafe": return { "pi-memory-scope": FOREIGN, "pi-memory-span": "9007199254740992" };
		case "spanHuge": return { "pi-memory-scope": FOREIGN, "pi-memory-span": "99999999999999999999999" };
		case "spanMaxSafe": return { "pi-memory-scope": FOREIGN, "pi-memory-span": "9007199254740991" };
		case "foreign": return { "pi-memory-scope": FOREIGN, "pi-memory-span": "1" };
		case "ownUnknown": return scope && { "pi-memory-scope": scope, "pi-memory-span": "99999" };
		case "empty": return {};
	}
	return undefined;
};
async function apply(recorder, span, op, kept) {
	switch (op.op) {
		case "attrs": span.setAttributes(revive(op.attrs)); break;
		case "event": span.addEvent(op.name, revive(op.attrs)); break;
		case "status": span.setStatus(op.status); break;
		case "keep": kept.push(span); break;
		case "useKept": if (kept.length > 0) await apply(recorder, kept[op.idx % kept.length], op.sub, kept); break;
		case "link": span.addLink({ context: target(recorder, kept, op), attributes: revive(op.attrs) }); break;
		case "child": {
			const body = async (child) => { for (const sub of op.body) await apply(recorder, child, sub, kept); fail(op.outcome); };
			try { await span.startSpan({ name: op.name, attributes: revive(op.attrs), links: linksOf(recorder, kept, op.links) }, body); } catch {}
			break;
		}
		case "remote": {
			const body = async (child) => { for (const sub of op.body) await apply(recorder, child, sub, kept); fail(op.outcome); };
			const parent = recorder.extract(carrierOf(recorder, kept, op));
			try { await parent.startSpan({ name: op.name, attributes: revive(op.attrs), links: linksOf(recorder, kept, op.links) }, body); } catch {}
			break;
		}
	}
}
const results = [];
for (const program of JSON.parse(input)) {
	const context = new InMemoryTelemetryContext();
	const kept = [];
	for (const root of program) {
		try { await context.startSpan({ name: root.name, attributes: revive(root.attrs), links: linksOf(context, kept, root.links) }, async (span) => { for (const op of root.body) await apply(context, span, op, kept); fail(root.outcome); }); } catch {}
	}
	results.push(JSON.parse(JSON.stringify(context.getSpans()).replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g, "SCOPE")));
}
process.stdout.write(JSON.stringify(results));
