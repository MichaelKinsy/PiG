// Run Pi's packages/durable/src/search.ts (imported as-is with types stripped) over texts and ranking queries.
// Op: {op: "tokenize", text} | {op: "rank", query, documents: [{name, text}], limit}
// stdout: per op the terms, or the names Pi ranks.
// usage: node search_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const search = await import(pathToFileURL(`${packages}durable/src/search.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	out.push(op.op === "tokenize" ? search.tokenize(op.text) : search.rankDocuments(op.query, op.documents, op.limit));
}
process.stdout.write(JSON.stringify(out));
