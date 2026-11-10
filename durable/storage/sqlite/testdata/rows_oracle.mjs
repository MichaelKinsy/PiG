// Run Pi's packages/durable/src/storage/sqlite/rows.ts (imported as-is, types stripped) over document records and JSON values.
// stdin: {"records": [DocumentRecord...], "values": [JSON value...]}
// stdout: {"records": [{row, current, address, scope}...], "values": [encodeJson(value)...]}
//   row      documentRow(record) as the JSON text of its nine cells
//   current  isCurrentOnly(record)
//   address  addressKey(record), the one string per document address
//   scope    JSON text of scopeColumns(record.scope)
// usage: node rows_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const rows = await import(pathToFileURL(`${packages}durable/src/storage/sqlite/rows.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { records, values } = JSON.parse(input);
const out = {
	records: records.map((record) => ({
		row: JSON.stringify(rows.documentRow(record)),
		current: rows.isCurrentOnly(record),
		address: rows.addressKey(record),
		scope: JSON.stringify(rows.scopeColumns(record.scope)),
	})),
	values: values.map((value) => rows.encodeJson(value)),
};
process.stdout.write(JSON.stringify(out));
