// Run Pi's packages/durable/src/storage/sqlite/statements.ts insertRows (imported as-is, types stripped) over node:sqlite.
// stdin: JSON array of batches {ddl, table, columns, conflict, rows}; a row is cells: null, an integer, or {s: segments}.
// A segment is {b: base64 of UTF-8 bytes, decoded as WHATWG TextDecoder does (U+FFFD per maximal subpart)} or {u: UTF-16 units}.
// stdout: per batch the stored rows as typeof:hex(bytes), or {error}.
// usage: node insert_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/chord)
import module from "node:module";
import { DatabaseSync } from "node:sqlite";
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
const statements = await import(pathToFileURL(`${packages}durable/src/storage/sqlite/statements.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const decoder = new TextDecoder();
const cell = (c) => {
	if (c === null || typeof c === "number") return c;
	return c.s.map((p) => (p.b !== undefined ? decoder.decode(Buffer.from(p.b, "base64")) : String.fromCharCode(...p.u))).join("");
};
const out = [];
for (const batch of JSON.parse(input)) {
	const db = new DatabaseSync(":memory:");
	try {
		db.exec(batch.ddl);
		const executor = {
			run: async (sql, ...params) => void db.prepare(sql).run(...params),
			all: async (sql, ...params) => db.prepare(sql).all(...params),
		};
		await statements.insertRows(executor, batch.table, batch.columns, batch.rows.map((r) => r.map(cell)), batch.conflict);
		const cols = batch.columns.map((c) => `typeof(${c}) || ':' || hex(CAST(${c} AS BLOB))`).join(" || '|' || ");
		out.push(db.prepare(`SELECT ${cols} AS line FROM ${batch.table} ORDER BY ${batch.columns.join(", ")}`).all().map((r) => r.line));
	} catch (error) {
		out.push({ error: error instanceof Error ? error.message : String(error) });
	} finally {
		db.close();
	}
}
process.stdout.write(JSON.stringify(out));
