// Run Pi's packages/ai/src/utils/validation.ts validateToolArguments (typebox 1.3.27 Compile/Value/Check) over {schema, args} probes from stdin.
// usage: node validate_tool_args.mjs <repo root> <typebox version>
// Result: {ok: arguments} or {err: message}.
import module from "node:module";
import { pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
const root = process.argv[2];
const typeboxDir = `${root}/internal/typebox/testdata/npm/`;
const typeboxVersion = JSON.parse(readFileSync(typeboxDir + "node_modules/typebox/package.json", "utf8")).version;
if (typeboxVersion !== process.argv[3]) throw new Error(`Wrong typebox ${typeboxVersion}`);
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier === "typebox" || specifier.startsWith("typebox/")) return next(specifier, { ...context, parentURL: pathToFileURL(typeboxDir + "x.mjs").href });
		return next(specifier, context);
	},
});
const { validateToolArguments } = await import(pathToFileURL(`${root}/.upstream/current/packages/ai/src/utils/validation.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			const tool = { name: "probe", description: "", parameters: JSON.parse(p.schema) };
			try {
				return { ok: validateToolArguments(tool, { type: "toolCall", id: "1", name: "probe", arguments: JSON.parse(p.args) }) };
			} catch (error) {
				return { err: error.message };
			}
		}),
	),
);
