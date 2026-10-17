// Run Pi's pure compaction functions (packages/durable/src/harness/compaction.ts, imported as-is with types stripped, plus the private summaryText and summaryFailure cut out of it) over ops from stdin.
// ops: {op: "cut", contributions, head, keep} | {op: "estimate", contributions, head, extra} | {op: "summarized", contributions, head, cut} | {op: "order", messages} | {op: "serialize", messages} | {op: "summary", message}
// A view's entries are {id} numbered from 1 (the head marker, when there is one, is entries[0]); its messages are its contributions flattened, sharing the message objects.
// stdout: one {value} or {err} per op.
// usage: node compaction_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
import module from "node:module";
import { readFileSync } from "node:fs";
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
const compactionURL = pathToFileURL(`${packages}durable/src/harness/compaction.ts`).href;
const { selectCut, summarizedMessages, estimateContext, serializeConversation } = await import(compactionURL);
const { orderToolResults } = await import(pathToFileURL(`${packages}durable/src/harness/context.ts`).href);
const source = readFileSync(`${packages}durable/src/harness/compaction.ts`, "utf8");
const cutFunction = (name) => /* the function from "function <name>" to the first "}" in column 0 */ new RegExp(`^function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source)[0];
const { stripTypeScriptTypes } = module;
const summary = new Function(`${stripTypeScriptTypes(`${cutFunction("summaryText")}\n${cutFunction("summaryFailure")}`)}; return { summaryText, summaryFailure };`)();
const view = (op) => {
	const contributions = op.contributions;
	const entries = contributions.map((_, index) => ({ id: index + 1 }));
	return { head: op.head ? entries[0] : undefined, entries, contributions, messages: contributions.flat() };
};
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	try {
		let value;
		if (op.op === "cut") value = selectCut(view(op), op.keep) ?? null;
		else if (op.op === "estimate") value = estimateContext(view(op), op.extra);
		else if (op.op === "summarized") value = summarizedMessages(view(op), op.cut);
		else if (op.op === "order") value = orderToolResults(op.messages);
		else if (op.op === "serialize") value = serializeConversation(op.messages);
		else if (op.op === "summary") value = { text: summary.summaryText(op.message) ?? null, failure: summary.summaryFailure(op.message) };
		else throw new Error(`unknown op ${op.op}`);
		out.push({ value });
	} catch (error) {
		out.push({ err: error?.message ?? String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
