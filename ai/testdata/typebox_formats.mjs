// Run typebox 1.3.27's format registry through Value.Check({type: "string", format}) over {format, value} probes from stdin: the boolean answer per probe.
// usage: node typebox_formats.mjs <repo root> <typebox version>
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
const { Value } = await import("typebox/value");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((p) => Value.Check({ type: "string", format: p.format }, p.value))));
