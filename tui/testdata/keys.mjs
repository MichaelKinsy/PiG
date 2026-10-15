// Run Pi's packages/tui/src/keys.ts parseKey and matchesKey over terminal input from stdin:
// {ids, cases: [{data, kitty}]} -> [{parse, matches}], where parse is the parseKey result (null when undefined) and matches lists the indexes of the
// ids matchesKey accepts. The Kitty protocol flag is set per case; Windows Terminal detection reads WT_SESSION, which the caller leaves unset.
// usage: node keys.mjs <path to packages/tui/src/keys.ts>
import { pathToFileURL } from "node:url";
const { parseKey, matchesKey, setKittyProtocolActive } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { ids, cases } = JSON.parse(input);
const results = cases.map(({ data, kitty }) => {
	setKittyProtocolActive(kitty);
	const matches = [];
	ids.forEach((id, index) => {
		if (matchesKey(data, id)) matches.push(index);
	});
	return { parse: parseKey(data) ?? null, matches };
});
process.stdout.write(JSON.stringify(results));
