// Run Pi's planSystemEntries and replaySections (packages/durable/src/harness/prompt.ts, imported as-is with types stripped) over ops from stdin.
// op: {op: "plan", view: {head: bool, entries: [{id, kind}], messages}, desired: [[key, text]], tools, timestamp} | {op: "replay", messages}
// A view's head, when set, is entries[0]. stdout: one {value} or {err} per op.
// usage: node prompt_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
import module from "node:module";
import { pathToFileURL } from "node:url";
import { chordSourceURL } from "../../internal/oraclesupport/chord_resolve.mjs";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		const chordSource = chordSourceURL(packages, specifier);
		if (chordSource) return next(chordSource, context);
		if (specifier.startsWith("@earendil-works/pi-ai") || specifier === "typebox" || specifier.startsWith("typebox/")) return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
});
const { planSystemEntries, replaySections } = await import(pathToFileURL(`${packages}durable/src/harness/prompt.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	try {
		if (op.op === "replay") out.push({ value: [...replaySections(op.messages)] });
		else {
			const entries = op.view.entries;
			const view = { head: op.view.head ? entries[0] : undefined, entries, contributions: [], messages: op.view.messages };
			out.push({ value: planSystemEntries(view, new Map(op.desired), op.tools, op.timestamp) });
		}
	} catch (error) {
		out.push({ err: error?.message ?? String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
