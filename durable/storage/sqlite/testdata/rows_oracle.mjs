// Run Pi's packages/durable/src/storage/sqlite/rows.ts (imported as-is, types stripped) over document records Pi builds itself, and JSON values.
// stdin: {"records": [{address, definition: {history?, fork?}, id, createdAt, retiredAt?}...], "values": [JSON value...]}
//   Each record is what SqliteStorage stores for a new document: documentCreate (documents.ts, imported as-is) of the address, then the
//   createdAt and retiredAt the storage stamps (sqlite/storage.ts, `{ ...action.create, createdAt: seq, ...(action.retire ? { retiredAt: seq } : {}) }`).
//   So the record's members and their order are Pi's, not the Go test's.
// stdout: {"records": [{record, row, current, address, scope}...], "values": [encodeJson(value)...]}
//   record   encodeJson(record), the text of the `record` column
//   row      documentRow(record) as the JSON text of its nine cells
//   current  isCurrentOnly(record)
//   address  addressKey(record), the one string per document address
//   scope    JSON text of scopeColumns(record.scope)
// usage: node rows_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const rows = await import(pathToFileURL(`${packages}durable/src/storage/sqlite/rows.ts`).href);
const documents = await import(pathToFileURL(`${packages}durable/src/documents.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { records, values } = JSON.parse(input);
const out = {
	records: records.map((described) => {
		const create = documents.documentCreate(described.definition, described.address, described.id);
		const record = { ...create, createdAt: described.createdAt, ...(described.retiredAt === undefined ? {} : { retiredAt: described.retiredAt }) };
		return {
			record: rows.encodeJson(record),
			row: JSON.stringify(rows.documentRow(record)),
			current: rows.isCurrentOnly(record),
			address: rows.addressKey(record),
			scope: JSON.stringify(rows.scopeColumns(record.scope)),
		};
	}),
	values: values.map((value) => rows.encodeJson(value)),
};
process.stdout.write(JSON.stringify(out));
