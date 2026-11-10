// Run Pi's JSON for values that hold a lone UTF-16 surrogate. stdin: a JSON array of ops, each
//   {op: "stringify", text}   JSON.stringify(JSON.parse(text)): the text Pi writes for the value `text` describes, in its member order
//   {op: "addressId", text}   addressId of the DocumentAddress `text` describes (packages/durable/src/documents.ts, imported as-is)
// stdout: one string per op.
// usage: node surrogate_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const documents = await import(pathToFileURL(`${packages}durable/src/documents.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = JSON.parse(input).map((op) => {
	const value = JSON.parse(op.text);
	return op.op === "addressId" ? documents.addressId(value) : JSON.stringify(value);
});
process.stdout.write(JSON.stringify(out));
