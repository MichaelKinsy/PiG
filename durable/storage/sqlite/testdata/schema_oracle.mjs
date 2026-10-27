// Run Pi's packages/durable/src/storage/sqlite/schema.ts (imported as-is with types stripped) over schema prefixes.
// Case: {prefix: string | null} (null is no prefix, Pi's `undefined`), or {default: true} for DEFAULT_SQLITE_SCHEMA.
// stdout: per case {tables, indexes} or {error: message}.
// usage: node schema_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/chord)
import module from "node:module";
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const interop = process.env.PI_INTEROP_DIR.replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier === "@earendil-works/chord/sqlite") {
			return next(pathToFileURL(`${interop}node_modules/@earendil-works/chord/dist/sqlite/index.js`).href, context);
		}
		return next(specifier, context);
	},
});
const schema = await import(pathToFileURL(`${packages}durable/src/storage/sqlite/schema.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const c of JSON.parse(input)) {
	try {
		const built = c.default ? schema.DEFAULT_SQLITE_SCHEMA : schema.createSqliteSchema(c.prefix === null ? undefined : c.prefix);
		out.push({ tables: built.tables, indexes: built.indexes });
	} catch (error) {
		out.push({ error: error instanceof Error ? error.message : String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
