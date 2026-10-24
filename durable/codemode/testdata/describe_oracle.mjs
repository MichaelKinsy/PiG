// Run Pi's packages/durable/src/codemode/describe.ts (imported as-is with types stripped) over tool sets and budgets.
// Op: {op: "constants"} | {op: "description", models} | {op: "section", tools, namespaces, budget} | {op: "namespace", name, namespaces}
//   | {op: "sample", tool}      a tool is {name, description, parameters, structuredOutputSchema?}
// stdout: per op {ok} or {err} (an Error's message).
// usage: node describe_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-durable)
import module from "node:module";
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const interop = process.env.PI_INTEROP_DIR.replace(/\/?$/, "/");
// Packages the source imports by name resolve as they do from the installed @earendil-works/pi-durable.
const parent = pathToFileURL(`${interop}node_modules/@earendil-works/pi-durable/dist/index.js`).href;
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier.startsWith("@earendil-works/") || specifier.startsWith("@silvia-odwyer/")) {
			return next(specifier, { ...context, parentURL: parent });
		}
		return next(specifier, context);
	},
});
const describe = await import(pathToFileURL(`${packages}durable/src/codemode/describe.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	try {
		switch (op.op) {
			case "constants":
				out.push({ ok: { budget: describe.DEFAULT_CODEMODE_INLINE_BUDGET } });
				break;
			case "description":
				out.push({ ok: describe.codemodeDescription(op.models) });
				break;
			case "section":
				out.push({ ok: describe.codemodeSection(op.tools, op.namespaces, op.budget) ?? null });
				break;
			case "namespace":
				out.push({ ok: describe.namespaceOf(op.name, op.namespaces) ?? null });
				break;
			case "sample":
				out.push({ ok: describe.sampleOf(op.tool) });
				break;
			default:
				throw new Error(`unknown op ${op.op}`);
		}
	} catch (error) {
		out.push({ err: error instanceof Error ? error.message : String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
