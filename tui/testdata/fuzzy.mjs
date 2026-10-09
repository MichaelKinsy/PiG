// Run Pi's packages/tui/src/fuzzy.ts over probes from stdin: {op: "match", query, text} -> {matches, score} and {op: "filter", items, query} -> [item].
// usage: node fuzzy.mjs <path to packages/tui/src/fuzzy.ts>
import { pathToFileURL } from "node:url";
const { fuzzyMatch, fuzzyFilter } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) =>
	probe.op === "match" ? fuzzyMatch(probe.query, probe.text) : fuzzyFilter(probe.items, probe.query, (item) => item),
);
process.stdout.write(JSON.stringify(results));
