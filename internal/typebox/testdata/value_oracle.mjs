// Run typebox 1.3.27 Value over cases from stdin: each case is {schema: "<Type.* expression>", value}. The answer per case
// is the schema as JSON (the data Go reads) and Value.Check, Value.Errors, Value.Clean and Value.Default of a clone.
// usage: node value_oracle.mjs <repo root> <typebox version>
import module from "node:module";
import { pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
const root = process.argv[2];
const typeboxDir = `${root}/internal/typebox/testdata/npm/`;
const version = JSON.parse(readFileSync(typeboxDir + "node_modules/typebox/package.json", "utf8")).version;
if (version !== process.argv[3]) throw new Error(`Wrong typebox ${version}`);
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier === "typebox" || specifier.startsWith("typebox/")) return next(specifier, { ...context, parentURL: pathToFileURL(typeboxDir + "x.mjs").href });
		return next(specifier, context);
	},
});
const { Type } = await import("typebox");
const { Value } = await import("typebox/value");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const answers = JSON.parse(input).map(({ schema: source, value }) => {
	const schema = new Function("Type", `return ${source}`)(Type);
	const absent = value === "<absent>" ? undefined : value;
	const defaulted = Value.Default(schema, Value.Clone(absent));
	return {
		schema: JSON.parse(JSON.stringify(schema)),
		check: Value.Check(schema, absent),
		errors: [...Value.Errors(schema, absent)].map((error) => ({ instancePath: error.instancePath, message: error.message })),
		clean: Value.Clean(schema, Value.Clone(absent)) ?? "<absent>",
		default: defaulted === undefined ? "<absent>" : defaulted,
	};
});
process.stdout.write(JSON.stringify(answers));
