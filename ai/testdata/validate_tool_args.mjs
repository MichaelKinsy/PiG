// Run Pi's packages/ai/src/utils/validation.ts validateToolArguments (pi-schema compile with coerce, formats and unresolvedRefs "reject") over {name, schema, args} probes from stdin.
// usage: node --disable-warning=ExperimentalWarning validate_tool_args.mjs <repo root>
// The probes are JSON texts: schema is the tool's parameters, args the call's arguments. Result per probe: {ok: JSON.stringify of the validated arguments} or {err: the error's message}.
// A bigint (Type.BigInt, which a JSON argument converts to) has no JSON form and JSON.stringify throws on it, so the arguments are written as Pi's validation-probes.test.ts outcome() writes them: a bigint as `${value}n`.
// The Pi tree is .upstream/current, which must be Pi b556cfe5 or later (validation.ts imports @earendil-works/pi-schema); the package resolves to its TypeScript source there.
import module from "node:module";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = process.argv[2];
const pi = `${root}/.upstream/current/packages/`;
const validationPath = `${pi}ai/src/utils/validation.ts`;
if (!readFileSync(validationPath, "utf8").includes('"@earendil-works/pi-schema"')) {
	throw new Error(`${validationPath} does not validate with pi-schema: mirror Pi b556cfe5 or later (make upstream-mirror PI_UPSTREAM_REF=b556cfe5)`);
}
const schemaEntries = {
	"@earendil-works/pi-schema": `${pi}schema/src/index.ts`,
	"@earendil-works/pi-schema/formats": `${pi}schema/src/formats/index.ts`,
};
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier in schemaEntries) return next(pathToFileURL(schemaEntries[specifier]).href, context);
		return next(specifier, context);
	},
});
const { validateToolArguments } = await import(pathToFileURL(validationPath).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			const tool = { name: p.name, description: "", parameters: JSON.parse(p.schema) };
			try {
				return { ok: JSON.stringify(validateToolArguments(tool, { type: "toolCall", id: "1", name: p.name, arguments: JSON.parse(p.args) }), (_key, value) => (typeof value === "bigint" ? `${value}n` : value)) };
			} catch (error) {
				return { err: error.message };
			}
		}),
	),
);
